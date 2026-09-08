/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-08
 * @FileName: board.go
 * @Description: [留言板备份的规范化数据模型与本地 backup.json 读写]
 */

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	boardBackupVersion = 1             // backup.json 结构版本
	boardDirName       = "liuyanban"   // 相对 storage/qzone/<QQ>/ 的留言板目录名
	boardBackupFile    = "backup.json" // 规范化留言数据文件名
	boardIndexFile     = "index.html"  // 离线查看页文件名
)

// BoardIntro 是留言板顶部的主人寄语。
type BoardIntro struct {
	Author  MoodPerson `json:"author"`         // 空间主人
	Content string     `json:"content"`        // 纯文本
	HTML    string     `json:"html,omitempty"` // 已转义的展示 HTML
}

// BoardBackupFile 是留言板 backup.json 的完整结构。
// Posts 复用 MoodPost：tid 为留言 id，comments 为回复。
type BoardBackupFile struct {
	Version    int         `json:"version"`          // backup.json 结构版本
	UIN        string      `json:"uin"`              // 被备份空间的 QQ
	Nickname   string      `json:"nickname"`         // 备份时记下的昵称
	ExportedAt string      `json:"exported_at"`      // 最近一次写出 backup.json / 查看页的时间
	Total      int         `json:"total"`            // Posts 条数
	Notice     string      `json:"notice,omitempty"` // 写给查看页顶部的提示，例如无权查看
	Intro      *BoardIntro `json:"intro,omitempty"`  // 主人寄语，查看页置顶
	Posts      []MoodPost  `json:"posts"`            // 已备份的留言，按时间倒序
}

// BoardViewerInfo 描述一份已经生成过的本地留言板查看页，供菜单里挑选打开。
type BoardViewerInfo struct {
	UIN        string // 空间主人 QQ
	Nickname   string // 备份时记下的昵称
	Total      int    // backup.json 里的留言条数
	ExportedAt string // 上次生成查看页的时间
	IndexHTML  string // index.html 路径，供浏览器打开
}

// boardRoot 返回指定 QQ 的留言板备份根目录：storage/qzone/<QQ>/liuyanban。
func boardRoot(targetUin string) string {
	return filepath.Join("storage", "qzone", targetUin, boardDirName)
}

// boardBackupPath 返回规范化留言数据的落盘路径 data/backup.json。
func boardBackupPath(root string) string {
	return filepath.Join(root, "data", boardBackupFile)
}

// boardIndexPath 返回查看页 index.html 的路径。
func boardIndexPath(root string) string {
	return filepath.Join(root, boardIndexFile)
}

// loadBoardBackup 读取已有 backup.json；文件不存在或损坏时由调用方决定是否当空备份。
func loadBoardBackup(root string) (*BoardBackupFile, error) {
	data, err := os.ReadFile(boardBackupPath(root))
	if err != nil {
		return nil, err
	}
	var file BoardBackupFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return &file, nil
}

// saveBoardBackup 写出 backup.json：补版本号和导出时间，留言按时间倒序。
func saveBoardBackup(root string, file *BoardBackupFile) error {
	if file == nil {
		return nil
	}
	file.Version = boardBackupVersion
	file.ExportedAt = time.Now().Format("2006-01-02 15:04:05")
	file.Total = len(file.Posts)
	sortMoodPosts(file.Posts)

	path := boardBackupPath(root)
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// ListBoardViewers 扫描 storage/qzone/*/liuyanban/index.html，供「查看留言板备份」菜单列出。
func ListBoardViewers() []BoardViewerInfo {
	matches, err := filepath.Glob(filepath.Join("storage", "qzone", "*", boardDirName, boardIndexFile))
	if err != nil {
		return nil
	}

	out := make([]BoardViewerInfo, 0, len(matches))
	for _, index := range matches {
		root := filepath.Dir(index)
		info := BoardViewerInfo{
			IndexHTML: index,
			UIN:       filepath.Base(filepath.Dir(root)),
		}
		if file, err := loadBoardBackup(root); err == nil && file != nil {
			info.UIN = file.UIN
			info.Nickname = file.Nickname
			info.Total = file.Total
			info.ExportedAt = file.ExportedAt
		}
		out = append(out, info)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].ExportedAt == out[j].ExportedAt {
			return out[i].UIN > out[j].UIN
		}
		return out[i].ExportedAt > out[j].ExportedAt
	})
	return out
}

// IsBoardTask 判断任务是否来自留言板备份。
// 重试任务的 Mode 仍是 retry_failed，所以还要看失败项上的 Kind。
func IsBoardTask(record *TaskRecord) bool {
	if record == nil {
		return false
	}
	if record.Mode == TaskModeBoard {
		return true
	}
	if len(record.OpenFailedItems) > 0 && record.OpenFailedItems[0].Kind == FailedKindBoard {
		return true
	}
	if len(record.FailedItems) > 0 && record.FailedItems[0].Kind == FailedKindBoard {
		return true
	}
	return false
}

// BoardRoot 返回留言板备份目录，给 CLI 打印路径用。
func BoardRoot(targetUin string) string {
	return boardRoot(targetUin)
}

// BoardIndexPath 返回留言板查看页 index.html 路径，给 CLI 打开浏览器用。
func BoardIndexPath(targetUin string) string {
	return boardIndexPath(boardRoot(targetUin))
}
