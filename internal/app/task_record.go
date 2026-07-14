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

const taskRecordDir = "storage/tasks"

// TaskMode 用于区分本次任务是完整下载还是失败项重试。
type TaskMode string

const (
	TaskModeBackup      TaskMode = "backup"
	TaskModeRetryFailed TaskMode = "retry_failed"
)

// TaskStatus 表示任务最终状态。
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusSuccess   TaskStatus = "success"
	TaskStatusPartial   TaskStatus = "partial"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// TaskConfigSnapshot 固化单次任务执行时使用的关键配置，便于后续复盘与失败重试。
type TaskConfigSnapshot struct {
	TaskLimit              int  `json:"task_limit"`
	EnableDynamicTaskLimit bool `json:"enable_dynamic_task_limit"`
	EnableTimeline         bool `json:"enable_timeline"`
	EnableMetadataExport   bool `json:"enable_metadata_export"`
	Exclude                bool `json:"exclude"`
}

// TaskSummary 记录单次任务的执行结果摘要。
type TaskSummary struct {
	Total      uint64 `json:"total"`
	Success    uint64 `json:"success"`
	NewAdded   uint64 `json:"new_added"`
	Skipped    uint64 `json:"skipped"`
	Failed     uint64 `json:"failed"`
	VideoCount uint64 `json:"video_count"`
	ImageCount uint64 `json:"image_count"`
	BytesDone  uint64 `json:"bytes_done"`
}

// TaskRecord 是单次备份任务的持久化记录。
type TaskRecord struct {
	ID              string             `json:"id"`
	SourceTaskID    string             `json:"source_task_id,omitempty"`
	Mode            TaskMode           `json:"mode"`
	Status          TaskStatus         `json:"status"`
	CreatedAt       time.Time          `json:"created_at"`
	FinishedAt      time.Time          `json:"finished_at,omitempty"`
	OperatorUin     string             `json:"operator_uin"`
	TargetUin       string             `json:"target_uin"`
	Albums          []string           `json:"albums,omitempty"`
	Config          TaskConfigSnapshot `json:"config"`
	Summary         TaskSummary        `json:"summary"`
	FailedItems     []FailedItem       `json:"failed_items,omitempty"`
	OpenFailedItems []FailedItem       `json:"open_failed_items,omitempty"`
	Error           string             `json:"error,omitempty"`
	ResolvedByTask  string             `json:"resolved_by_task,omitempty"`
	Path            string             `json:"-"`
}

// NewTaskRecord 创建一个新的任务记录骨架。
func NewTaskRecord(mode TaskMode, operatorUin string, targetUin string, albums []string, cfg *Config, exclude bool) *TaskRecord {
	now := time.Now()
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

// NewRetryTaskRecord 基于原始任务创建新的失败项重试记录。
func NewRetryTaskRecord(source *TaskRecord, cfg *Config) *TaskRecord {
	if source == nil {
		return nil
	}

	record := NewTaskRecord(TaskModeRetryFailed, source.OperatorUin, source.TargetUin, source.Albums, cfg, true)
	record.SourceTaskID = source.ID
	return record
}

// Finalize 将本次运行结果写回任务记录。
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
	r.FailedItems = cloneFailedItems(result.FailedItems)
	r.OpenFailedItems = cloneFailedItems(result.FailedItems)
}

// Save 将任务记录持久化到本地 JSON 文件。
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

// LoadTaskRecord 读取指定 ID 的任务记录。
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

// LoadTaskRecords 返回本地全部任务记录，按创建时间倒序排列。
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

// FindLatestRetryableTask 返回当前账号下最近一条仍存在待处理失败项的任务。
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

// ListRetryableTasks 返回当前账号下全部仍存在待处理失败项的任务，按创建时间倒序排列。
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

// ResolveOpenFailures 用新的失败列表回写源任务的“待处理失败项”状态，形成完整的重试闭环。
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

func cloneFailedItems(items []FailedItem) []FailedItem {
	if len(items) == 0 {
		return nil
	}

	cloned := make([]FailedItem, len(items))
	copy(cloned, items)
	return cloned
}
