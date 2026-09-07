/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-07-14
 * @FileName: task_record.go
 * @Description: [备份任务记录持久化管理，支持失败项追踪、任务回放与重试链路闭环]
 */

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qinjintian/qq-zone/internal/pkg/util"
)

// 任务记录目录，相对进程工作目录。每个任务一个独立 JSON，便于按 ID 直接定位。
const taskRecordDir = "storage/tasks"

// TaskMode 区分这次任务是相册完整备份、失败重试，还是说说备份。
type TaskMode string

const (
	TaskModeBackup      TaskMode = "backup"       // 按相册列表完整跑一遍
	TaskModeRetryFailed TaskMode = "retry_failed" // 只下载源任务里还没解决的失败项
	TaskModeShuoShuo    TaskMode = "shuoshuo"     // 备份说说并生成本地查看页
)

// TaskStatus 是任务落盘时的最终（或进行中）状态，由 CLI 根据下载结果判定后写入。
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"   // 任务已创建、下载尚未结束（启动时先写一条占位记录）
	TaskStatusSuccess   TaskStatus = "success"   // 全部成功，或重试后源任务的待处理失败项已清空
	TaskStatusPartial   TaskStatus = "partial"   // 有成功也有失败，通常还能再重试
	TaskStatusFailed    TaskStatus = "failed"    // 异常中断且几乎没有成功项
	TaskStatusCancelled TaskStatus = "cancelled" // 用户取消（例如 Ctrl+C）
)

// TaskConfigSnapshot 固化本次任务启动时用到的关键配置。
// 重试时会从源任务读回这些值，避免用户后来改了全局配置，导致重试行为和当初不一致。
type TaskConfigSnapshot struct {
	TaskLimit              int  `json:"task_limit"`                // 并发下载数
	EnableDynamicTaskLimit bool `json:"enable_dynamic_task_limit"` // 是否按网速自动加减并发
	EnableTimeline         bool `json:"enable_timeline"`           // 是否按年/月整理目录
	EnableMetadataExport   bool `json:"enable_metadata_export"`    // 是否额外导出原始 JSON（相册元数据或说说 raw 页）
	Exclude                bool `json:"exclude"`                   // true 表示增量：本地已有文件则跳过
}

// TaskSummary 是一次运行结束后的计数摘要，字段含义与 DownloadResult 对齐。
type TaskSummary struct {
	Total      uint64 `json:"total"`       // 计划处理数：相册=媒体文件；说说=说说条数
	Success    uint64 `json:"success"`     // 成功数（含增量跳过）：相册=文件；说说=说说条数
	NewAdded   uint64 `json:"new_added"`   // 本次新下载的文件数（不含跳过）
	Skipped    uint64 `json:"skipped"`     // 因本地已存在而跳过
	Failed     uint64 `json:"failed"`      // 本次失败数：相册=文件；说说=配图/视频
	VideoCount uint64 `json:"video_count"` // 成功处理的视频（含实况图视频）
	ImageCount uint64 `json:"image_count"` // 成功处理的静态图
	BytesDone  uint64 `json:"bytes_done"`  // 实际写入磁盘的字节数
}

// TaskRecord 是单次备份或重试任务的完整落盘结构。
type TaskRecord struct {
	ID           string     `json:"id"`                       // 任务 ID，也是文件名（不含 .json）
	SourceTaskID string     `json:"source_task_id,omitempty"` // 仅重试任务有值：指向被重试的那条源任务
	Mode         TaskMode   `json:"mode"`
	Status       TaskStatus `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	FinishedAt   time.Time  `json:"finished_at,omitempty"` // pending 时为空；结束后才写入

	// OperatorUin 是当前登录的 QQ；TargetUin 是被备份的空间主人。
	// 自己备份自己时两者相同，备份好友空间时不同。查找可重试任务按 OperatorUin 过滤。
	OperatorUin string `json:"operator_uin"`
	TargetUin   string `json:"target_uin"`

	// 本次勾选的相册名。空切片表示当时选了「全部相册」。说说备份固定写「说说」。
	Albums []string `json:"albums,omitempty"`

	Config  TaskConfigSnapshot `json:"config"`
	Summary TaskSummary        `json:"summary"`

	// FailedItems 是本次运行产生的失败清单，只做历史记录，Finalize 写完后不再改。
	FailedItems []FailedItem `json:"failed_items,omitempty"`
	// OpenFailedItems 是「还没重试成功」的失败项。菜单里「重试失败」读的就是这个字段。
	// 后续重试任务会通过 ResolveOpenFailures 把它改成「仍然失败」的子集。
	OpenFailedItems []FailedItem `json:"open_failed_items,omitempty"`

	Error          string `json:"error,omitempty"`            // 任务级异常（不是单个文件失败）
	ResolvedByTask string `json:"resolved_by_task,omitempty"` // 最近一次回写 OpenFailedItems 的重试任务 ID

	// Path 是本地 JSON 路径，只在内存里用，不写入文件。
	Path string `json:"-"`
}

// NewTaskRecord 创建一条尚未开始下载的任务骨架（status=pending），并预先算好落盘路径。
// albums 会拷贝一份，避免调用方后续改切片影响到已创建的记录。
func NewTaskRecord(mode TaskMode, operatorUin string, targetUin string, albums []string, cfg *Config, exclude bool) *TaskRecord {
	now := time.Now()
	// ID = 可读时间戳 + 短哈希，既方便按时间扫文件，又避免同一秒内撞名。
	idSeed := operatorUin + "_" + targetUin + "_" + now.Format(time.RFC3339Nano)

	record := &TaskRecord{
		ID:          now.Format("20060102_150405") + "_" + util.MD5(idSeed)[:8],
		Mode:        mode,
		Status:      TaskStatusPending,
		CreatedAt:   now,
		OperatorUin: operatorUin,
		TargetUin:   targetUin,
		Albums:      append([]string(nil), albums...),
		Config: TaskConfigSnapshot{
			Exclude: exclude,
		},
	}

	if cfg != nil {
		record.Config.TaskLimit = cfg.TaskLimit
		record.Config.EnableDynamicTaskLimit = cfg.EnableDynamicTaskLimit
		record.Config.EnableTimeline = cfg.EnableTimeline
		record.Config.EnableMetadataExport = cfg.EnableMetadataExport
	}

	record.Path = taskRecordPath(record.ID)
	return record
}

// NewRetryTaskRecord 为「重试源任务的 OpenFailedItems」新建一条独立记录。
// 账号和相册沿用源任务；exclude 固定为 true，因为重试只补失败文件，不应整相册重下。
func NewRetryTaskRecord(source *TaskRecord, cfg *Config) *TaskRecord {
	if source == nil {
		return nil
	}

	record := NewTaskRecord(TaskModeRetryFailed, source.OperatorUin, source.TargetUin, source.Albums, cfg, true)
	record.SourceTaskID = source.ID
	return record
}

// Finalize 把一次运行的结果写回内存中的记录，调用方通常紧接着 Save。
// status=pending 表示任务刚创建、还没跑完：只清 FinishedAt/Error，不覆盖摘要。
func (r *TaskRecord) Finalize(result *DownloadResult, runErr error, status TaskStatus) {
	if r == nil {
		return
	}

	r.Status = status
	if status == TaskStatusPending {
		r.FinishedAt = time.Time{}
		r.Error = ""
		return
	}

	r.FinishedAt = time.Now()
	if runErr != nil {
		r.Error = runErr.Error()
	} else {
		r.Error = ""
	}

	if result == nil {
		return
	}

	r.Summary = TaskSummary{
		Total:      result.Total,
		Success:    result.Success,
		NewAdded:   result.NewAdded,
		Skipped:    result.Skipped,
		Failed:     result.Failed,
		VideoCount: result.VideoCount,
		ImageCount: result.ImageCount,
		BytesDone:  result.BytesDone,
	}
	// 任务刚结束时，历史失败清单和待重试清单相同；之后只有 OpenFailedItems 会被重试回写改掉。
	r.FailedItems = cloneFailedItems(result.FailedItems)
	r.OpenFailedItems = cloneFailedItems(result.FailedItems)
}

// Save 把当前记录写成 storage/tasks/<id>.json。目录不存在时会自动创建。
func (r *TaskRecord) Save() error {
	if r == nil {
		return nil
	}

	if r.Path == "" {
		r.Path = taskRecordPath(r.ID)
	}

	if err := os.MkdirAll(filepath.Dir(r.Path), os.ModePerm); err != nil {
		return err
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.Path, data, 0644)
}

// LoadTaskRecord 按任务 ID 读取单条记录。文件不存在或 JSON 损坏时直接返回错误。
func LoadTaskRecord(taskID string) (*TaskRecord, error) {
	path := taskRecordPath(taskID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var record TaskRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	record.Path = path
	return &record, nil
}

// LoadTaskRecords 扫描目录下全部任务记录，按 CreatedAt 从新到旧排序。
// 单条文件读失败或 JSON 损坏会跳过，避免一条坏记录让整个历史列表不可用。
func LoadTaskRecords() ([]*TaskRecord, error) {
	entries, err := os.ReadDir(taskRecordDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	records := make([]*TaskRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(taskRecordDir, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}

		var record TaskRecord
		if jsonErr := json.Unmarshal(data, &record); jsonErr != nil {
			continue
		}
		record.Path = path
		records = append(records, &record)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	return records, nil
}

// FindLatestRetryableTask 返回当前登录账号下、仍有 OpenFailedItems 的最新一条任务。
// 列表已按时间倒序，所以第一个命中的就是最近可重试的。
func FindLatestRetryableTask(operatorUin string) (*TaskRecord, error) {
	records, err := LoadTaskRecords()
	if err != nil {
		return nil, err
	}

	for _, record := range records {
		if record.OperatorUin != operatorUin {
			continue
		}
		if len(record.OpenFailedItems) == 0 {
			continue
		}
		return record, nil
	}

	return nil, nil
}

// ListRetryableTasks 返回当前登录账号下所有还能重试的任务（OpenFailedItems 非空），同样按时间倒序。
func ListRetryableTasks(operatorUin string) ([]*TaskRecord, error) {
	records, err := LoadTaskRecords()
	if err != nil {
		return nil, err
	}

	retryable := make([]*TaskRecord, 0, len(records))
	for _, record := range records {
		if record.OperatorUin != operatorUin {
			continue
		}
		if len(record.OpenFailedItems) == 0 {
			continue
		}
		retryable = append(retryable, record)
	}

	return retryable, nil
}

// ResolveOpenFailures 在重试结束后回写「源任务」的待处理失败项。
// remaining 是这次重试后仍然失败的文件；resolvedByTaskID 是刚跑完的那条重试任务。
// 全部成功时把源任务标成 success，这样它不会再出现在可重试列表里。
func ResolveOpenFailures(taskID string, remaining []FailedItem, resolvedByTaskID string) error {
	record, err := LoadTaskRecord(taskID)
	if err != nil {
		return err
	}

	record.OpenFailedItems = cloneFailedItems(remaining)
	record.ResolvedByTask = resolvedByTaskID
	if len(remaining) == 0 {
		record.Status = TaskStatusSuccess
	}

	return record.Save()
}

func taskRecordPath(taskID string) string {
	return filepath.Join(taskRecordDir, taskID+".json")
}

// cloneFailedItems 拷贝失败列表，避免调用方后续改切片时连带改掉已落盘/已保存的记录。
// 空列表返回 nil，这样 JSON 里可以 omitempty 掉这个字段。
func cloneFailedItems(items []FailedItem) []FailedItem {
	if len(items) == 0 {
		return nil
	}

	cloned := make([]FailedItem, len(items))
	copy(cloned, items)
	return cloned
}
