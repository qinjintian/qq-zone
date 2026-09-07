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
 * @FileName: mood_viewer.go
 * @Description: [把规范化说说写成可双击打开的离线 HTML 时间线]
 */

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qinjintian/qq-zone/internal/app/viewer"
)

// moodViewerMeta 写入 data/meta.js，查看页页头和年份导航用它，不读 backup.json（file:// 下 fetch 会跨域）。
type moodViewerMeta struct {
	UIN        string   `json:"uin"`                // 被备份空间的 QQ，页头展示
	Nickname   string   `json:"nickname"`           // 昵称，页标题和页头用
	ExportedAt string   `json:"exported_at"`        // 本次生成查看页的时间
	Total      int      `json:"total"`              // 说说条数
	Years      []string `json:"years"`              // 有说说的年份，导航栏按这个渲染（新→旧）
	Archived   bool     `json:"archived,omitempty"` // 更早说说被封存时为 true
	Notice     string   `json:"notice,omitempty"`   // 页顶提示文案，例如更早说说被封存
}

// writeMoodViewer 写出 index.html、样式脚本，以及按年拆开的 posts-YYYY.js。
// 年份脚本用 <script src> 引入，双击 file:// 打开时不会踩到 fetch 跨域。
func writeMoodViewer(root string, file *MoodBackupFile) error {
	if file == nil {
		return fmt.Errorf("empty mood backup")
	}

	assetsDir := filepath.Join(root, "assets")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(assetsDir, os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, os.ModePerm); err != nil {
		return err
	}

	if err := copyViewerAsset("assets/viewer.css", filepath.Join(assetsDir, "viewer.css")); err != nil {
		return err
	}
	if err := copyViewerAsset("assets/viewer.js", filepath.Join(assetsDir, "viewer.js")); err != nil {
		return err
	}

	posts := stripMoodURLs(file.Posts)
	for i := range posts {
		posts[i] = repairMoodPost(posts[i]) // 生成查看页时再修一次旧备份里的表情和误包 @
	}
	byYear := map[string][]MoodPost{}
	for _, post := range posts {
		year := "未知"
		if post.Time > 0 {
			year = time.Unix(post.Time, 0).In(shanghaiLoc).Format("2006")
		}
		byYear[year] = append(byYear[year], post)
	}

	years := make([]string, 0, len(byYear))
	for year := range byYear {
		years = append(years, year)
	}
	sort.Slice(years, func(i, j int) bool { return years[i] > years[j] })

	var scriptTags strings.Builder
	for _, year := range years {
		filename := fmt.Sprintf("posts-%s.js", sanitizePath(year))
		payload, err := json.Marshal(byYear[year])
		if err != nil {
			return err
		}
		js := fmt.Sprintf("window.__QQZONE_YEARS__=window.__QQZONE_YEARS__||{};\nwindow.__QQZONE_YEARS__[%q]=%s;\n", year, payload)
		if err := os.WriteFile(filepath.Join(dataDir, filename), []byte(js), 0644); err != nil {
			return err
		}
		scriptTags.WriteString(`  <script src="data/` + filename + `"></script>` + "\n")
	}

	meta := moodViewerMeta{
		UIN:        file.UIN,
		Nickname:   file.Nickname,
		ExportedAt: file.ExportedAt,
		Total:      len(file.Posts),
		Years:      years,
		Archived:   file.Archived,
		Notice:     file.Notice,
	}
	if meta.ExportedAt == "" {
		meta.ExportedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	metaJS := "window.__QQZONE_META__=" + string(metaJSON) + ";\n"
	if err := os.WriteFile(filepath.Join(dataDir, "meta.js"), []byte(metaJS), 0644); err != nil {
		return err
	}

	index, err := viewer.Files.ReadFile("index.html")
	if err != nil {
		return err
	}
	index = bytes.Replace(index, []byte("<!--YEAR_SCRIPTS-->"), []byte(scriptTags.String()), 1)
	title := strings.TrimSpace(file.Nickname)
	if title == "" {
		title = "QQ 空间说说"
	} else {
		title = title + " 的说说备份"
	}
	index = bytes.Replace(index, []byte("QQ 空间说说备份"), []byte(title), 1)
	return os.WriteFile(filepath.Join(root, moodIndexFile), index, 0644)
}

// copyViewerAsset 把内嵌的 CSS/JS 模板拷到备份目录的 assets/ 下。
func copyViewerAsset(src, dest string) error {
	data, err := viewer.Files.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0644)
}

// stripMoodURLs 生成查看页数据前去掉原始下载地址和 vid，避免把接口链接写进可分享的 HTML。
func stripMoodURLs(posts []MoodPost) []MoodPost {
	raw, err := json.Marshal(posts)
	if err != nil {
		return posts
	}
	var cloned []MoodPost
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return posts
	}
	var stripList func([]MoodMedia)
	stripList = func(list []MoodMedia) {
		for i := range list {
			list[i].URL = ""
			list[i].URLs = nil
			list[i].VideoID = ""
		}
	}
	var stripComments func([]MoodComment)
	stripComments = func(cs []MoodComment) {
		for i := range cs {
			stripList(cs[i].Media)
			stripComments(cs[i].Replies)
		}
	}
	for i := range cloned {
		stripList(cloned[i].Media)
		if cloned[i].Repost != nil {
			stripList(cloned[i].Repost.Media)
		}
		stripComments(cloned[i].Comments)
	}
	return cloned
}
