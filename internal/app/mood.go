/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-07
 * @FileName: mood.go
 * @Description: [说说备份的规范化数据模型与本地 backup.json 读写]
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
	moodBackupVersion = 1
	moodDirName       = "shuoshuo"
	moodBackupFile    = "backup.json"
	moodIndexFile     = "index.html"
)

// MoodPerson 是说说里出现的一个人：作者、评论者或点赞者。
type MoodPerson struct {
	UIN    string `json:"uin"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"` // 相对查看页的头像路径
}

// MoodMedia 是说说或评论里的一张图、一段视频或一条语音。
type MoodMedia struct {
	ID      string   `json:"id,omitempty"`
	Type    string   `json:"type"` // image / video / voice
	Path    string   `json:"path,omitempty"`
	Poster  string   `json:"poster,omitempty"` // 视频封面相对路径
	URL     string   `json:"url,omitempty"`    // 主下载地址，查看页生成时会去掉
	URLs    []string `json:"urls,omitempty"`   // 换源候选
	VideoID string   `json:"video_id,omitempty"`
	Width   int      `json:"width,omitempty"`
	Height  int      `json:"height,omitempty"`
}

// MoodComment 是一条评论，Replies 为楼中楼。
type MoodComment struct {
	ID       string        `json:"id,omitempty"`
	Author   MoodPerson    `json:"author"`
	Content  string        `json:"content"`
	HTML     string        `json:"html,omitempty"`
	Time     int64         `json:"time"`
	TimeText string        `json:"time_text,omitempty"`
	Media    []MoodMedia   `json:"media,omitempty"`
	Replies  []MoodComment `json:"replies,omitempty"`
}

// MoodRepost 是转发的原说说摘要。
type MoodRepost struct {
	TID     string      `json:"tid,omitempty"`
	Author  MoodPerson  `json:"author"`
	Content string      `json:"content"`
	HTML    string      `json:"html,omitempty"`
	Media   []MoodMedia `json:"media,omitempty"`
}

// MoodPost 是给查看页用的一条规范化说说。
type MoodPost struct {
	TID          string        `json:"tid"`
	Author       MoodPerson    `json:"author"`
	Time         int64         `json:"time"`
	TimeText     string        `json:"time_text,omitempty"`
	Content      string        `json:"content"`
	HTML         string        `json:"html,omitempty"`
	Source       string        `json:"source,omitempty"`
	Location     string        `json:"location,omitempty"`
	ShareTitle   string        `json:"share_title,omitempty"`
	ShareURL     string        `json:"share_url,omitempty"`
	Media        []MoodMedia   `json:"media,omitempty"`
	Repost       *MoodRepost   `json:"repost,omitempty"`
	Likes        []MoodPerson  `json:"likes,omitempty"`
	LikeCount    int           `json:"like_count"`
	Comments     []MoodComment `json:"comments,omitempty"`
	CommentCount int           `json:"comment_count"`
	HasMoreCon   bool          `json:"-"`
	PicTotal     int           `json:"-"`
}

// MoodBackupFile 是 backup.json 的完整结构，程序增量备份时读它。
type MoodBackupFile struct {
	Version    int        `json:"version"`
	UIN        string     `json:"uin"`
	Nickname   string     `json:"nickname"`
	ExportedAt string     `json:"exported_at"`
	Total      int        `json:"total"`
	Archived   bool       `json:"archived,omitempty"` // 更早的说说可能被空间封存
	Notice     string     `json:"notice,omitempty"`
	Posts      []MoodPost `json:"posts"`
}

// MoodViewerInfo 描述一份已经生成过的本地说说查看页，供菜单里挑选打开。
type MoodViewerInfo struct {
	UIN        string
	Nickname   string
	Total      int
	ExportedAt string
	IndexHTML  string
}

func moodRoot(targetUin string) string {
	return filepath.Join("storage", "qzone", targetUin, moodDirName)
}

func moodBackupPath(root string) string {
	return filepath.Join(root, "data", moodBackupFile)
}

func moodIndexPath(root string) string {
	return filepath.Join(root, moodIndexFile)
}

func loadMoodBackup(root string) (*MoodBackupFile, error) {
	data, err := os.ReadFile(moodBackupPath(root))
	if err != nil {
		return nil, err
	}
	var file MoodBackupFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return &file, nil
}

func saveMoodBackup(root string, file *MoodBackupFile) error {
	if file == nil {
		return nil
	}
	file.Version = moodBackupVersion
	file.ExportedAt = time.Now().Format("2006-01-02 15:04:05")
	file.Total = len(file.Posts)
	sortMoodPosts(file.Posts)

	path := moodBackupPath(root)
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func sortMoodPosts(posts []MoodPost) {
	sort.SliceStable(posts, func(i, j int) bool {
		if posts[i].Time == posts[j].Time {
			return posts[i].TID > posts[j].TID
		}
		return posts[i].Time > posts[j].Time
	})
}

func mergeMoodPosts(newer, older []MoodPost) []MoodPost {
	seen := make(map[string]int, len(newer)+len(older))
	out := make([]MoodPost, 0, len(newer)+len(older))
	appendUnique := func(list []MoodPost) {
		for _, p := range list {
			if p.TID == "" {
				out = append(out, p)
				continue
			}
			if idx, ok := seen[p.TID]; ok {
				out[idx] = p
				continue
			}
			seen[p.TID] = len(out)
			out = append(out, p)
		}
	}
	appendUnique(newer)
	appendUnique(older)
	sortMoodPosts(out)
	return out
}

// ListMoodViewers 扫描本地已生成的说说查看页。
func ListMoodViewers() []MoodViewerInfo {
	matches, err := filepath.Glob(filepath.Join("storage", "qzone", "*", moodDirName, moodIndexFile))
	if err != nil {
		return nil
	}

	out := make([]MoodViewerInfo, 0, len(matches))
	for _, index := range matches {
		root := filepath.Dir(index)
		info := MoodViewerInfo{
			IndexHTML: index,
			UIN:       filepath.Base(filepath.Dir(root)),
		}
		if file, err := loadMoodBackup(root); err == nil && file != nil {
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

// IsShuoShuoTask 判断任务（或其失败项）是否属于说说备份。
func IsShuoShuoTask(record *TaskRecord) bool {
	if record == nil {
		return false
	}
	if record.Mode == TaskModeShuoShuo {
		return true
	}
	if len(record.OpenFailedItems) > 0 && record.OpenFailedItems[0].Kind == FailedKindShuoShuo {
		return true
	}
	if len(record.FailedItems) > 0 && record.FailedItems[0].Kind == FailedKindShuoShuo {
		return true
	}
	return false
}
