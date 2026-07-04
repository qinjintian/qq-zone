/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-07-02
 * @LastEditors: qinjintian<514092640@qq.com>
 * @LastEditTime: 2026-07-03 17:30:00
 * @FileName: spider.go
 * @Description: [QQ 空间媒体爬虫核心引擎，基于 errgroup 实现多协程并发下载与进度统计]
 */

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/qinjintian/qq-zone/internal/qzone"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type DownloadResult struct {
	Total      uint64
	Success    uint64
	NewAdded   uint64
	Skipped    uint64
	Failed     uint64
	VideoCount uint64
	ImageCount uint64
}

type Spider struct {
	client    *qzone.Client
	whitelist map[string]bool
	taskLimit int
	logger    *zap.SugaredLogger

	results DownloadResult
	mu      sync.Mutex
}

func NewSpider(client *qzone.Client, taskLimit int, albums []string, logger *zap.SugaredLogger) *Spider {
	wl := make(map[string]bool)
	for _, a := range albums {
		wl[a] = true
	}
	return &Spider{
		client:    client,
		whitelist: wl,
		taskLimit: taskLimit,
		logger:    logger,
	}
}

func (s *Spider) Download(targetUin string, exclude bool) (*DownloadResult, error) {
	s.results = DownloadResult{}

	albums, err := s.client.GetAlbumList(targetUin)
	if err != nil {
		return nil, err
	}

	if len(albums) == 0 {
		s.logger.Warnf("未发现任何相册，请确认账号 [%s] 空间是否开放或登录是否失效", targetUin)
	}

	filteredAlbums := make([]gjson.Result, 0)
	for _, album := range albums {
		name := album.Get("name").String()
		allow := album.Get("allowAccess").Int()

		if allow == 0 {
			s.logger.Debugf("跳过相册 [%s]: 无访问权限 (allowAccess=0)", name)
			continue
		}
		if len(s.whitelist) > 0 && !s.whitelist[name] {
			continue
		}
		filteredAlbums = append(filteredAlbums, album)
	}

	for i, album := range filteredAlbums {
		if err := s.downloadAlbum(targetUin, album, i+1, len(filteredAlbums), exclude); err != nil {
			s.logger.Errorf("failed to download album [%s]: %v", album.Get("name").String(), err)
		}
	}

	return &s.results, nil
}

func (s *Spider) downloadAlbum(targetUin string, album gjson.Result, albumIdx, albumTotal int, exclude bool) error {
	albumName := album.Get("name").String()
	albumID := album.Get("id").String()

	baseDir := filepath.Join("storage", "qzone", targetUin, "album")
	safeName := sanitizePath(albumName)
	albumPath := filepath.Join(baseDir, safeName)

	if err := os.MkdirAll(albumPath, os.ModePerm); err != nil {
		albumPath = filepath.Join(baseDir, util.MD5(albumName)[8:24])
		_ = os.MkdirAll(albumPath, os.ModePerm)
	}

	photos, err := s.client.GetPhotoList(targetUin, albumID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.results.Total += uint64(len(photos))
	s.mu.Unlock()

	localFiles := make(map[string]string)
	if exclude {
		files, _ := util.ListFiles(albumPath)
		for _, f := range files {
			name := filepath.Base(f)
			if idx := strings.LastIndex(name, "."); idx != -1 {
				name = name[:idx]
			}
			localFiles[name] = f
		}
	} else {
		_ = os.RemoveAll(albumPath)
		_ = os.MkdirAll(albumPath, os.ModePerm)
	}

	g, ctx := errgroup.WithContext(context.Background())
	g.SetLimit(s.taskLimit)

	for i, photo := range photos {
		i, photo := i, photo
		g.Go(func() error {
			return s.downloadItem(ctx, targetUin, i+1, photo, album, albumIdx, albumTotal, albumPath, len(photos), exclude, localFiles)
		})
	}

	return g.Wait()
}

func (s *Spider) downloadItem(ctx context.Context, targetUin string, idx int, photo, album gjson.Result, albumIdx, albumTotal int, albumPath string, total int, exclude bool, localFiles map[string]string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	albumName := album.Get("name").String()
	sloc := photo.Get("sloc").String()
	originalName := photo.Get("name").String()
	if originalName == "" {
		originalName = sloc
	}

	shootTime := photo.Get("rawshoottime").String()
	if shootTime == "" || shootTime == "0" {
		shootTime = photo.Get("uploadtime").String()
	}

	loc, _ := time.LoadLocation("Local")
	t, err := time.ParseInLocation("2006-01-02 15:04:05", shootTime, loc)
	if err != nil {
		// 如果解析失败，尝试解析上传时间
		uploadTime := photo.Get("uploadtime").String()
		t, _ = time.ParseInLocation("2006-01-02 15:04:05", uploadTime, loc)
	}
	shootDate := t.Format("20060102150405")

	var source, filename string
	isVideo := photo.Get("is_video").Bool()
	picrefer := photo.Get("picrefer").Int()
	// 实况图特征：是视频类型，且 picrefer 为 0
	isLivePhoto := isVideo && picrefer == 0

	// 1. 优先处理视频逻辑 (正规视频 和 实况图的视频部分)
	if isVideo {
		videoURL, err := s.client.GetVideoDownloadURL(targetUin, album.Get("id").String(), sloc)
		if err == nil && videoURL != "" {
			prefix := "VID_"
			if isLivePhoto {
				prefix = "MVIMG_"
			}
			filename = fmt.Sprintf("%s%s_%s_%s.mp4", prefix, shootDate[:8], shootDate[8:], util.MD5(sloc)[8:24])
			source = videoURL
		} else {
			if !isLivePhoto {
				// 纯视频获取地址失败才报错
				s.mu.Lock()
				s.results.Failed++
				s.mu.Unlock()
				return err
			}
			s.logger.Warnf("Could not get video component for Live Photo %s, falling back to image only", sloc)
		}
	}

	// 2. 处理照片逻辑 (仅针对 静态图 或 视频获取失败的实况图)
	if filename == "" {
		source = photo.Get("raw").String()
		if source == "" {
			source = photo.Get("origin_url").String()
		}
		if source == "" {
			source = photo.Get("url").String()
		}

		// 强制获取“原图”版本
		if strings.Contains(source, "b&bo=") {
			source = strings.Replace(source, "b&bo=", "o&bo=", 1)
		}

		prefix := "IMG_"
		if isLivePhoto {
			prefix = "MVIMG_"
		}

		filename = fmt.Sprintf("%s%s_%s_%s", prefix, shootDate[:8], shootDate[8:], util.MD5(sloc)[8:24])
		ext := ".jpg"
		if strings.Contains(source, ".png") {
			ext = ".png"
		} else if strings.Contains(source, ".gif") {
			ext = ".gif"
		}
		filename += ext
	}

	isSkip := false
	if exclude {
		base := strings.TrimSuffix(filename, filepath.Ext(filename))
		if p, ok := localFiles[base]; ok {
			head, err := s.client.Http.Head(source, map[string]string{"cookie": s.client.Cookie})
			if err == nil {
				cLen, _ := strconv.ParseInt(head.Get("Content-Length"), 10, 64)
				fi, _ := os.Stat(p)
				if cLen <= fi.Size() {
					isSkip = true
				} else {
					_ = os.Remove(p)
				}
			}
		}
	}

	if !isSkip {
		target := filepath.Join(albumPath, filename)
		headers := map[string]string{
			"cookie":     s.client.Cookie,
			"user-agent": "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/78.0.3904.108 Safari/537.36",
		}

		if isVideo {
			headers["Range"] = "bytes=0-"
			headers["Referer"] = fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", targetUin)
		}

		_, err := s.client.Http.Download(source, target, headers, 3, 60, false)
		if err != nil {
			s.mu.Lock()
			s.results.Failed++
			s.mu.Unlock()
			return err
		}
	}

	s.updateResults(isSkip, isVideo)

	// 获取文件大小
	fileSizeStr := "未知"
	if fi, err := os.Stat(filepath.Join(albumPath, filename)); err == nil {
		fileSizeStr = util.FormatBytes(fi.Size())
	}

	// 使用颜色和卡片式布局美化输出
	blue := color.New(color.FgCyan).SprintFunc()
	green := color.New(color.FgGreen).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	gray := color.New(color.FgWhite, color.Faint).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	status := green("SUCCESS")
	if isSkip {
		status = yellow("SKIPPED")
	}

	output := fmt.Sprintf("\n%s %s [%d/%d] 相册 %s 第 %d 个文件下载完成\n",
		gray(time.Now().Format("15:04:05")),
		bold(status),
		albumIdx, albumTotal,
		blue("["+albumName+"]"),
		idx,
	)
	output += fmt.Sprintf(" %s %-12s %s\n", gray("├─"), "当前账号:", targetUin)
	output += fmt.Sprintf(" %s %-12s %s\n", gray("├─"), "完成时间:", time.Now().Format("2006/01/02 15:04:05"))
	output += fmt.Sprintf(" %s %-12s %s\n", gray("├─"), "原始名称:", originalName)
	output += fmt.Sprintf(" %s %-12s %s\n", gray("├─"), "本地名称:", yellow(filename))
	output += fmt.Sprintf(" %s %-12s %s\n", gray("├─"), "文件大小:", green(fileSizeStr))
	output += fmt.Sprintf(" %s %-12s %s\n", gray("└─"), "文件地址:", gray(source))

	s.logger.Info(output)

	return nil
}

func (s *Spider) updateResults(isSkip, isVideo bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results.Success++
	if isSkip {
		s.results.Skipped++
	} else {
		s.results.NewAdded++
	}
	if isVideo {
		s.results.VideoCount++
	} else {
		s.results.ImageCount++
	}
}

func sanitizePath(name string) string {
	name = strings.TrimSuffix(name, ".")
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	for _, char := range invalid {
		name = strings.ReplaceAll(name, char, "_")
	}
	return strings.TrimSpace(name)
}
