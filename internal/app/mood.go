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
	moodBackupVersion = 1             // backup.json 结构版本，升级字段时再加
	moodDirName       = "shuoshuo"    // 相对 storage/qzone/<QQ>/ 的说说目录名
	moodBackupFile    = "backup.json" // 规范化说说数据文件名
	moodIndexFile     = "index.html"  // 离线查看页文件名
)

// MoodPerson 是说说里出现的一个人：作者、评论者或点赞者。
type MoodPerson struct {
	UIN    string `json:"uin"`              // QQ 号，下载头像和去重用
	Name   string `json:"name"`             // 当时记下的昵称
	Avatar string `json:"avatar,omitempty"` // 相对查看页的头像路径，下载失败则为空
}

// MoodMedia 是说说或评论里的一张图、一段视频或一条语音。
type MoodMedia struct {
	ID      string   `json:"id,omitempty"`       // 空间侧 pic_id / 视频 id，失败重试时用来对上同一份文件
	Type    string   `json:"type"`               // image / video / voice
	Path    string   `json:"path,omitempty"`     // 相对查看页的本地路径；下载失败会清空，避免链到半截文件
	Poster  string   `json:"poster,omitempty"`   // 视频封面相对路径
	URL     string   `json:"url,omitempty"`      // 主下载地址，查看页生成时会去掉
	URLs    []string `json:"urls,omitempty"`     // 换源候选
	VideoID string   `json:"video_id,omitempty"` // 腾讯视频 vid，403 时走 getinfo 换源
	Width   int      `json:"width,omitempty"`    // 像素宽，查看页排版用
	Height  int      `json:"height,omitempty"`   // 像素高
}

// MoodComment 是一条评论，Replies 为楼中楼。
type MoodComment struct {
	ID       string        `json:"id,omitempty"`        // 评论 tid
	Author   MoodPerson    `json:"author"`              // 评论者
	Content  string        `json:"content"`             // 纯文本，搜索用
	HTML     string        `json:"html,omitempty"`      // 已转义的展示 HTML（换行、表情、@）
	Time     int64         `json:"time"`                // 发表时间 unix 秒
	TimeText string        `json:"time_text,omitempty"` // 展示用日期
	Media    []MoodMedia   `json:"media,omitempty"`     // 评论配图
	Replies  []MoodComment `json:"replies,omitempty"`   // 楼中楼
}

// MoodRepost 是转发的原说说摘要。
type MoodRepost struct {
	TID     string      `json:"tid,omitempty"`   // 原说说 tid
	Author  MoodPerson  `json:"author"`          // 原作者
	Content string      `json:"content"`         // 原说说纯文本
	HTML    string      `json:"html,omitempty"`  // 已转义的原说说正文
	Media   []MoodMedia `json:"media,omitempty"` // 原说说配图
}

// MoodPost 是给查看页用的一条规范化说说。
type MoodPost struct {
	TID          string        `json:"tid"`                   // 空间侧说说 id，增量合并和失败重试的主键
	Author       MoodPerson    `json:"author"`                // 发表者
	Time         int64         `json:"time"`                  // 发表时间 unix 秒
	TimeText     string        `json:"time_text,omitempty"`   // 展示用日期
	Content      string        `json:"content"`               // 纯文本，搜索用
	HTML         string        `json:"html,omitempty"`        // 已转义的展示 HTML
	Source       string        `json:"source,omitempty"`      // 来源，如手机 QQ
	Location     string        `json:"location,omitempty"`    // 定位地名
	ShareTitle   string        `json:"share_title,omitempty"` // 分享卡片标题
	ShareURL     string        `json:"share_url,omitempty"`   // 分享链接
	Media        []MoodMedia   `json:"media,omitempty"`       // 正文配图/视频/语音
	Repost       *MoodRepost   `json:"repost,omitempty"`      // 转发的原说说，原创则为空
	Likes        []MoodPerson  `json:"likes,omitempty"`       // 列表接口自带的点赞人，通常不完整
	LikeCount    int           `json:"like_count"`            // 点赞人数，可能比 Likes 列表更全
	Comments     []MoodComment `json:"comments,omitempty"`    // 评论（含楼中楼）
	CommentCount int           `json:"comment_count"`         // 空间侧声明的评论数
	HasMoreCon   bool          `json:"-"`                     // 列表里正文被截断，需要再拉详情
	PicTotal     int           `json:"-"`                     // 空间侧声明的配图总数，用来判断要不要补拉
}

// MoodBackupFile 是 backup.json 的完整结构，程序增量备份时读它。
type MoodBackupFile struct {
	Version    int        `json:"version"`            // backup.json 结构版本
	UIN        string     `json:"uin"`                // 被备份空间的 QQ
	Nickname   string     `json:"nickname"`           // 备份时记下的昵称
	ExportedAt string     `json:"exported_at"`        // 最近一次写出 backup.json / 查看页的时间
	Total      int        `json:"total"`              // Posts 条数
	Archived   bool       `json:"archived,omitempty"` // 更早的说说可能被空间封存
	Notice     string     `json:"notice,omitempty"`   // 写给查看页顶部的提示，例如更早说说被封存
	Posts      []MoodPost `json:"posts"`              // 已备份的说说，按时间倒序
}

// MoodViewerInfo 描述一份已经生成过的本地说说查看页，供菜单里挑选打开。
type MoodViewerInfo struct {
	UIN        string // 空间主人 QQ
	Nickname   string // 备份时记下的昵称
	Total      int    // backup.json 里的说说条数
	ExportedAt string // 上次生成查看页的时间
	IndexHTML  string // index.html 路径，供浏览器打开
}

// moodRoot 返回指定 QQ 的说说备份根目录：storage/qzone/<QQ>/shuoshuo。
func moodRoot(targetUin string) string {
	return filepath.Join("storage", "qzone", targetUin, moodDirName)
}

// moodBackupPath 返回规范化说说数据的落盘路径 data/backup.json。
func moodBackupPath(root string) string {
	return filepath.Join(root, "data", moodBackupFile)
}

// moodIndexPath 返回给用户双击打开的查看页路径。
func moodIndexPath(root string) string {
	return filepath.Join(root, moodIndexFile)
}

// loadMoodBackup 读取已有 backup.json；文件不存在或损坏时由调用方决定是否当全新备份。
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

// saveMoodBackup 按发表时间倒序写入 backup.json，并刷新导出时间和条数。
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

// sortMoodPosts 按发表时间从新到旧排；同一秒再用 tid 保证顺序稳定。
func sortMoodPosts(posts []MoodPost) {
	sort.SliceStable(posts, func(i, j int) bool {
		if posts[i].Time == posts[j].Time {
			return posts[i].TID > posts[j].TID
		}
		return posts[i].Time > posts[j].Time
	})
}

// mergeMoodPosts 把本轮新拉到的说说和本地已有记录按 tid 合并；相同 tid 保留 newer。
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

// ListMoodViewers 扫描 storage/qzone/*/shuoshuo/index.html，供「查看说说备份」菜单列出。
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

// IsShuoShuoTask 判断任务是否来自说说备份。
// 重试任务的 Mode 仍是 retry_failed，所以还要看失败项上的 Kind。
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
