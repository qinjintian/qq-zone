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
	"sync/atomic"
	"time"

	iurl "net/url"

	"github.com/fatih/color"
	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/qinjintian/qq-zone/internal/qzone"
	"github.com/tidwall/gjson"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type FailedItem struct {
	Album string
	Name  string
	Error string
}

// DownloadResult 用于原子化地统计整个备份任务的最终成果与各项指标
// 由于存在多协程并发写入，结构体内包含了互斥锁 (Mutex) 和原子操作 (atomic) 以保证数据一致性
type DownloadResult struct {
	Total       uint64       // 任务规划要下载的媒体文件总数
	Success     uint64       // 成功下载落盘的文件数（包含全新下载和增量跳过）
	NewAdded    uint64       // 本次任务中全新下载的文件数（不含跳过）
	Skipped     uint64       // 触发增量策略被跳过的已存在文件数
	Failed      uint64       // 发生异常导致下载失败的文件数
	VideoCount  uint64       // 成功处理的视频文件（含实况图视频）数量
	ImageCount  uint64       // 成功处理的静态图片数量
	BytesDone   uint64       // 记录已下载的累积字节数，用于动态计算平均下载速度
	FailedItems []FailedItem // 收集所有失败文件的上下文信息，用于生成最终的错误报告
	mu          sync.Mutex   // 并发写 FailedItems 时的互斥锁
}

// addFailedItem 并发安全地将一条失败记录追加到 FailedItems 队列中，并将失败计数器原子 +1
func (r *DownloadResult) addFailedItem(item FailedItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.FailedItems = append(r.FailedItems, item)
	atomic.AddUint64(&r.Failed, 1)
}

// Spider 负责调度和执行整个相册备份的核心业务逻辑
// 包含并发控制、断点续传、文件分类与统计等功能
type Spider struct {
	client    *qzone.Client
	whitelist map[string]bool
	config    *Config
	logger    *zap.SugaredLogger

	results DownloadResult
}

// NewSpider 实例化一个下载爬虫
func NewSpider(client *qzone.Client, config *Config, albums []string, logger *zap.SugaredLogger) *Spider {
	wl := make(map[string]bool)
	for _, a := range albums {
		wl[a] = true
	}
	return &Spider{
		client:    client,
		whitelist: wl,
		config:    config,
		logger:    logger,
	}
}

// Download 开始执行批量相册下载任务
// targetUin: 目标好友 QQ 号 (或自己的 QQ 号)
// exclude: 是否开启增量下载 (跳过本地已存在的完整文件)
func (s *Spider) Download(ctx context.Context, targetUin string, exclude bool) (*DownloadResult, error) {
	s.results = DownloadResult{}

	albums, err := s.client.GetAlbumList(ctx, targetUin)
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

	// 初始化 mpb
	p := mpb.NewWithContext(ctx)

	for i, album := range filteredAlbums {
		select {
		case <-ctx.Done():
			s.logger.Warn("任务已被用户取消")
			p.Wait()
			return &s.results, nil
		default:
		}
		if err := s.downloadAlbum(ctx, p, targetUin, album, i+1, len(filteredAlbums), exclude); err != nil {
			s.logger.Errorf("failed to download album [%s]: %v", album.Get("name").String(), err)
		}
	}

	p.Wait()

	return &s.results, nil
}

// downloadAlbum 负责下载单个相册内的所有照片和视频
// 内部实现了智能动态并发协程池 (Dynamic Worker Pool)
func (s *Spider) downloadAlbum(ctx context.Context, p *mpb.Progress, targetUin string, album gjson.Result, albumIdx, albumTotal int, exclude bool) error {
	albumName := album.Get("name").String()
	albumID := album.Get("id").String()

	baseDir := filepath.Join("storage", "qzone", targetUin, "album")
	safeName := sanitizePath(albumName)
	albumPath := filepath.Join(baseDir, safeName)

	if err := os.MkdirAll(albumPath, os.ModePerm); err != nil {
		albumPath = filepath.Join(baseDir, util.MD5(albumName)[8:24])
		_ = os.MkdirAll(albumPath, os.ModePerm)
	}

	// 导出相册元数据
	if s.config.EnableMetadataExport {
		metaPath := filepath.Join(albumPath, "album_metadata.json")
		_ = os.WriteFile(metaPath, []byte(album.Raw), 0644)
	}

	photos, err := s.client.GetPhotoList(ctx, targetUin, albumID)
	if err != nil {
		return err
	}

	atomic.AddUint64(&s.results.Total, uint64(len(photos)))

	// 为当前相册创建一个总进度条
	albumBar := p.AddBar(int64(len(photos)),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name(fmt.Sprintf("Album [%s] ", albumName), decor.WC{W: 20, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(
			decor.Percentage(),
			decor.Name(" ] "),
			decor.OnComplete(decor.Name("", decor.WC{W: 5}), "Done!"),
		),
	)

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

	g, gCtx := errgroup.WithContext(ctx)

	// 并发协程动态扩缩容 (Dynamic Worker Pool)
	var active int32
	baseLimit := int32(s.config.TaskLimit)
	if baseLimit < 1 {
		baseLimit = 10
	}
	var currentLimit int32 = baseLimit
	var maxLimit int32 = baseLimit * 3 // 允许突发到3倍并发
	var minLimit int32 = 1             // 最小并发数

	taskCh := make(chan int, len(photos))
	for i := range photos {
		taskCh <- i
	}
	close(taskCh)

	// 任务分发器
	g.Go(func() error {
		for i := range taskCh {
			// 等待可用并发槽
			for {
				if atomic.LoadInt32(&active) < atomic.LoadInt32(&currentLimit) {
					break
				}
				select {
				case <-gCtx.Done():
					return gCtx.Err()
				case <-time.After(100 * time.Millisecond):
				}
			}

			atomic.AddInt32(&active, 1)
			i := i
			photo := photos[i]

			g.Go(func() error {
				defer atomic.AddInt32(&active, -1)
				defer albumBar.Increment()
				return s.downloadItem(gCtx, p, targetUin, i+1, photo, album, albumIdx, albumTotal, albumPath, len(photos), exclude, localFiles)
			})
		}
		return nil
	})

	// 监控与动态扩缩容算法
	if s.config.EnableDynamicTaskLimit {
		g.Go(func() error {
			var lastSuccess uint64
			for {
				select {
				case <-gCtx.Done():
					return nil
				case <-time.After(2 * time.Second):
					// 启发式网络带宽探测：
					currSuccess := atomic.LoadUint64(&s.results.Success)
					diff := currSuccess - lastSuccess
					lastSuccess = currSuccess

					cl := atomic.LoadInt32(&currentLimit)
					act := atomic.LoadInt32(&active)

					// 任务全部分发且执行完毕时退出监控
					if len(taskCh) == 0 && act == 0 {
						return nil
					}

					if diff > 0 && act >= cl && cl < maxLimit {
						// 网络空闲且处理迅速，增加并发
						atomic.AddInt32(&currentLimit, 1)
					} else if diff == 0 && act >= cl && cl > minLimit {
						// 出现拥堵或遇到大文件，减少并发以防被风控或占满带宽
						atomic.AddInt32(&currentLimit, -1)
					}
				}
			}
		})
	}

	return g.Wait()
}

// downloadItem 负责处理单个文件 (照片/实况图/普通视频) 的分析、路径拼接、断点续传检查与下载调用
func (s *Spider) downloadItem(ctx context.Context, p *mpb.Progress, targetUin string, idx int, photo, album gjson.Result, albumIdx, albumTotal int, albumPath string, total int, exclude bool, localFiles map[string]string) error {
	select {
	case <-ctx.Done():
		return nil // 优雅停机：不返回错误，避免触发 errgroup 级联取消，只跳过未开始的任务
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

	shootDate := ""
	if !t.IsZero() {
		shootDate = t.Format("20060102150405")
	}

	filenameDate := shootDate
	if filenameDate == "" {
		filenameDate = "00000000000000"
	}

	var tasks []struct {
		url      string
		filename string
		isVideo  bool
	}

	isVideo := photo.Get("is_video").Bool()

	// 1. 获取图片组件信息
	imgSource := photo.Get("raw").String()
	if imgSource == "" {
		imgSource = photo.Get("origin_url").String()
	}
	if imgSource == "" {
		imgSource = photo.Get("url").String()
	}
	if strings.Contains(imgSource, "b&bo=") {
		imgSource = strings.Replace(imgSource, "b&bo=", "o&bo=", 1)
	}

	imgPrefix := "IMG_"
	if isVideo {
		imgPrefix = "VID_"
	}
	imgFilename := fmt.Sprintf("%s%s_%s_%s", imgPrefix, filenameDate[:8], filenameDate[8:], util.MD5(sloc)[8:24])
	ext := ".jpg"
	if strings.Contains(imgSource, ".png") {
		ext = ".png"
	} else if strings.Contains(imgSource, ".gif") {
		ext = ".gif"
	}
	imgFilename += ext

	// 2. 获取视频组件信息 (针对 视频 和 实况图)
	// 根据用户反馈，实况图在 QQ 空间本质上是以 MP4 格式存储的，因此将其视为视频处理
	if isVideo {
		videoURL, err := s.client.GetVideoDownloadURL(ctx, targetUin, album.Get("id").String(), sloc)
		if err == nil && videoURL != "" {
			vidFilename := fmt.Sprintf("VID_%s_%s_%s.mp4", filenameDate[:8], filenameDate[8:], util.MD5(sloc)[8:24])
			tasks = append(tasks, struct {
				url      string
				filename string
				isVideo  bool
			}{videoURL, vidFilename, true})
		} else {
			// 如果获取视频地址失败，则报错并继续
			s.results.addFailedItem(FailedItem{
				Album: albumName,
				Name:  sloc,
				Error: fmt.Sprintf("failed to get video download URL: %v", err),
			})
			return nil // 忽略单个视频错误，继续下载后续文件
		}
	} else {
		// 3. 纯图片任务
		tasks = append(tasks, struct {
			url      string
			filename string
			isVideo  bool
		}{imgSource, imgFilename, false})
	}

	for _, task := range tasks {
		isSkip := false
		if exclude {
			base := strings.TrimSuffix(task.filename, filepath.Ext(task.filename))
			if p, ok := localFiles[base]; ok {
				// 修正 task.filename 为本地已存在的实际文件名，确保 Download 能够正确识别并进行断点续传
				task.filename = filepath.Base(p)
				head, err := s.client.Http.Head(ctx, task.url, map[string]string{"cookie": s.client.Cookie})
				if err == nil {
					cLen, _ := strconv.ParseInt(head.Get("Content-Length"), 10, 64)
					fi, _ := os.Stat(p)
					if cLen <= fi.Size() {
						isSkip = true
					}
					// 注意：此处不再 os.Remove(p)，以保留文件给 http.go 进行断点续传
				}
			}
		}

		if !isSkip {
			savePath := albumPath
			if s.config.EnableTimeline && shootDate != "" {
				year := shootDate[:4]
				month := shootDate[4:6]
				savePath = filepath.Join(albumPath, year, month)
				_ = os.MkdirAll(savePath, os.ModePerm)
			}

			target := filepath.Join(savePath, task.filename)
			headers := map[string]string{
				"cookie":     s.client.Cookie,
				"user-agent": "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/78.0.3904.108 Safari/537.36",
			}

			if task.isVideo {
				headers["Referer"] = fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", targetUin)
				headers["Accept"] = "*/*"
				headers["Accept-Encoding"] = "identity;q=1, *;q=0"
				headers["Connection"] = "keep-alive"
				headers["Sec-Fetch-Dest"] = "video"
				headers["Sec-Fetch-Mode"] = "no-cors"
				headers["Sec-Fetch-Site"] = "cross-site"
				headers["Range"] = "bytes=0-"

				// 严格模拟 v1.0.0 的请求头，防止部分 CDN 节点校验 Host
				if u, err := iurl.Parse(task.url); err == nil {
					headers["Host"] = u.Host
				}
			}

			res, err := s.client.Http.Download(ctx, task.url, target, headers, 3, 600, p, task.filename, originalName)
			// 如果是视频且下载失败（可能是因为 f0 链接 404，或者是大文件下载中途断流）
			// 这里增加外层重试机制，支持大文件在龟速网络下被 CDN 强踢后的无限次（受限于 retry 参数）断点续传
			if err != nil && task.isVideo {
				// 如果是 404，尝试降级画质
				if strings.Contains(err.Error(), "404") && strings.Contains(task.url, ".f0.mp4") {
					originalURL := strings.Replace(task.url, ".f0.mp4", ".f20.mp4", 1)
					res, err = s.client.Http.Download(ctx, originalURL, target, headers, 3, 600, p, task.filename, originalName)
				} else if strings.Contains(err.Error(), "connection broken") || strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "EOF") {
					// 如果是因为网速太慢，被腾讯 CDN 主动断开连接 (TCP Reset / EOF)
					// 我们需要进行休眠退避，然后重新发起请求（底层 Http.Download 会自动读取本地已下载的文件大小，并附带 Range 头）
					s.logger.Warnf("网络波动导致 [%s] 下载中断，准备触发断点续传...", task.filename)
					time.Sleep(3 * time.Second) // 稍微冷却一下，防止被彻底拉黑
					// 再次发起下载，底层会自动补上 Range: bytes=已下载大小-
					res, err = s.client.Http.Download(ctx, task.url, target, headers, 5, 600, p, task.filename, originalName)
				}
			}

			if err != nil {
				s.results.addFailedItem(FailedItem{
					Album: albumName,
					Name:  task.filename,
					Error: fmt.Sprintf("download failed: %v", err),
				})
				// 这里不直接 return，尝试下载该项目的其他部分（如果有）
				continue
			} else if res != nil {
				if finalName, ok := res["filename"].(string); ok {
					task.filename = finalName
				}
			}
		}

		finalSavePath := albumPath
		if s.config.EnableTimeline && shootDate != "" {
			finalSavePath = filepath.Join(albumPath, shootDate[:4], shootDate[4:6])
		}
		actualTarget := filepath.Join(finalSavePath, task.filename)

		// 修复本地相册时间线 (OS Time 注入)
		if !t.IsZero() {
			if err := os.Chtimes(actualTarget, t, t); err != nil {
				s.logger.Debugf("failed to set OS time for %s: %v", actualTarget, err)
			}
		}

		s.updateResults(isSkip, task.isVideo)

		// 只在调试模式下输出详细日志
		if s.logger.Level().Enabled(zap.DebugLevel) {
			// 获取文件大小
			fileSizeStr := "未知"
			if fi, err := os.Stat(actualTarget); err == nil {
				fileSizeStr = util.FormatBytes(fi.Size())
			}

			blue := color.New(color.FgCyan).SprintFunc()
			green := color.New(color.FgGreen).SprintFunc()
			yellow := color.New(color.FgYellow).SprintFunc()
			gray := color.New(color.FgWhite, color.Faint).SprintFunc()
			bold := color.New(color.Bold).SprintFunc()

			status := green("SUCCESS")
			if isSkip {
				status = yellow("SKIPPED")
			}

			output := fmt.Sprintf("[%s] %s %s -> %s (%s)",
				gray(time.Now().Format("15:04:05")),
				bold(status),
				blue(originalName),
				yellow(task.filename),
				green(fileSizeStr),
			)
			s.logger.Debug(output)
		}
	}

	return nil
}

func (s *Spider) updateResults(isSkip, isVideo bool) {
	atomic.AddUint64(&s.results.Success, 1)
	if isSkip {
		atomic.AddUint64(&s.results.Skipped, 1)
	} else {
		atomic.AddUint64(&s.results.NewAdded, 1)
	}
	if isVideo {
		atomic.AddUint64(&s.results.VideoCount, 1)
	} else {
		atomic.AddUint64(&s.results.ImageCount, 1)
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
