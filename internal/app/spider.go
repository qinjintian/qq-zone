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
 * @LastEditTime: 2026-09-02 17:50:00
 * @FileName: spider.go
 * @Description: [QQ 空间媒体爬虫核心引擎，负责相册下载、失败项记录、断点续传与动态并发调度]
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
	nhttp "net/http"

	"github.com/fatih/color"
	ihttp "github.com/qinjintian/qq-zone/internal/net/http"
	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/qinjintian/qq-zone/internal/qzone"
	"github.com/tidwall/gjson"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// FailedItem 记录单个媒体文件失败时的完整上下文，既用于控制台展示，也用于后续失败重试。
type FailedItem struct {
	Album     string `json:"album"`
	Name      string `json:"name"`
	Error     string `json:"error"`
	TargetUin string `json:"target_uin,omitempty"`
	AlbumID   string `json:"album_id,omitempty"`
	AlbumRaw  string `json:"album_raw,omitempty"`
	PhotoRaw  string `json:"photo_raw,omitempty"`
	IsVideo   bool   `json:"is_video,omitempty"`
}

// DownloadResult 用于原子化地统计整个备份任务的最终成果与各项指标。
type DownloadResult struct {
	Total       uint64       // 任务规划要下载的媒体文件总数
	Success     uint64       // 成功下载落盘的文件数（包含全新下载和增量跳过）
	NewAdded    uint64       // 本次任务中全新下载的文件数（不含跳过）
	Skipped     uint64       // 触发增量策略被跳过的已存在文件数
	Failed      uint64       // 发生异常导致下载失败的文件数
	VideoCount  uint64       // 成功处理的视频文件（含实况图视频）数量
	ImageCount  uint64       // 成功处理的静态图片数量
	BytesDone   uint64       // 实时记录已成功写盘的网络字节数，用于动态并发调优
	FailedItems []FailedItem // 收集所有失败文件的上下文信息，用于生成最终错误报告和失败重试
	mu          sync.Mutex   // 并发写 FailedItems 时的互斥锁
}

// addFailedItem 并发安全地将一条失败记录追加到 FailedItems 队列中，并将失败计数器原子 +1。
func (r *DownloadResult) addFailedItem(item FailedItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.FailedItems = append(r.FailedItems, item)
	atomic.AddUint64(&r.Failed, 1)
}

// Spider 负责调度和执行整个相册备份的核心业务逻辑。
type Spider struct {
	client    *qzone.Client
	whitelist map[string]bool
	config    *Config
	logger    *zap.SugaredLogger

	results DownloadResult

	debugMu    sync.Mutex
	debugLines []string
}

type mediaTask struct {
	candidates []qzone.VideoCandidate
	videoID    string
	filename   string
	isVideo    bool
}

// NewSpider 实例化一个下载爬虫。
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

// Download 开始执行批量相册下载任务。
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

	s.resetVideoDebugLogs()
	s.logVideoDebugHint()
	p := mpb.NewWithContext(ctx)

	for i, album := range filteredAlbums {
		select {
		case <-ctx.Done():
			s.logger.Warn("任务已被用户取消")
			p.Wait()
			s.flushVideoDebugLogs()
			return &s.results, nil
		default:
		}

		if err := s.downloadAlbum(ctx, p, targetUin, album, i+1, len(filteredAlbums), exclude); err != nil {
			s.logger.Errorf("failed to download album [%s]: %v", album.Get("name").String(), err)
		}
	}

	p.Wait()
	s.flushVideoDebugLogs()
	return &s.results, nil
}

// RetryFailed 基于历史任务记录中保存的失败上下文，仅重试仍未解决的失败文件。
// 失败重试始终强制使用增量模式，避免误删已经成功下载的数据。
func (s *Spider) RetryFailed(ctx context.Context, targetUin string, failedItems []FailedItem) (*DownloadResult, error) {
	s.results = DownloadResult{}
	if len(failedItems) == 0 {
		return &s.results, nil
	}

	atomic.StoreUint64(&s.results.Total, uint64(len(failedItems)))
	s.resetVideoDebugLogs()
	s.logVideoDebugHint()
	p := mpb.NewWithContext(ctx)
	retryBar := p.AddBar(int64(len(failedItems)),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name("Retry Failed Items ", decor.WC{W: 20, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(
			decor.Percentage(),
			decor.Name(" ] "),
			decor.OnComplete(decor.Name("", decor.WC{W: 5}), "Done!"),
		),
	)

	localFileCache := make(map[string]map[string]string)
	for _, item := range failedItems {
		select {
		case <-ctx.Done():
			p.Wait()
			s.flushVideoDebugLogs()
			return &s.results, nil
		default:
		}

		album := gjson.Parse(item.AlbumRaw)
		photo := gjson.Parse(item.PhotoRaw)
		if !album.Exists() || !photo.Exists() {
			s.results.addFailedItem(FailedItem{
				Album:     item.Album,
				Name:      item.Name,
				Error:     "任务记录缺少原始相册/照片数据，无法自动重试，请重新执行备份任务",
				TargetUin: targetUin,
				AlbumID:   item.AlbumID,
				AlbumRaw:  item.AlbumRaw,
				PhotoRaw:  item.PhotoRaw,
				IsVideo:   item.IsVideo,
			})
			retryBar.Increment()
			continue
		}

		albumPath := s.buildAlbumPath(targetUin, album.Get("name").String())
		localFiles, ok := localFileCache[albumPath]
		if !ok {
			localFiles = s.buildLocalFileIndex(albumPath, true)
			localFileCache[albumPath] = localFiles
		}

		_ = s.downloadItem(ctx, p, targetUin, photo, album, albumPath, true, localFiles)
		retryBar.Increment()
	}

	retryBar.SetTotal(-1, true)
	p.Wait()
	s.flushVideoDebugLogs()
	return &s.results, nil
}

// downloadAlbum 负责下载单个相册内的所有照片和视频。
func (s *Spider) downloadAlbum(ctx context.Context, p *mpb.Progress, targetUin string, album gjson.Result, albumIdx, albumTotal int, exclude bool) error {
	albumName := album.Get("name").String()
	albumID := album.Get("id").String()
	albumPath := s.buildAlbumPath(targetUin, albumName)

	if s.config.EnableMetadataExport {
		metaPath := filepath.Join(albumPath, "album_metadata.json")
		_ = os.WriteFile(metaPath, []byte(album.Raw), 0644)
	}

	photos, err := s.client.GetPhotoList(ctx, targetUin, albumID)
	if err != nil {
		return err
	}

	atomic.AddUint64(&s.results.Total, uint64(len(photos)))

	// 空相册没有可下载的照片，直接返回，避免创建 total=0 的进度条。
	// mpb 会把 total<=0 视为“未知总量”，该进度条永远不会被标记为完成，
	// 导致外层 Download 里的 p.Wait() 永久阻塞，任务记录也一直停留在 pending。
	if len(photos) == 0 {
		return nil
	}

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

	localFiles := s.buildLocalFileIndex(albumPath, exclude)

	g, gCtx := errgroup.WithContext(ctx)

	var active int32
	baseLimit := int32(s.config.TaskLimit)
	if baseLimit < 1 {
		baseLimit = 10
	}
	currentLimit := baseLimit
	maxLimit := baseLimit * 3
	minLimit := int32(1)

	taskCh := make(chan int, len(photos))
	for i := range photos {
		taskCh <- i
	}
	close(taskCh)

	g.Go(func() error {
		for i := range taskCh {
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
				return s.downloadItem(gCtx, p, targetUin, photo, album, albumPath, exclude, localFiles)
			})
		}
		return nil
	})

	if s.config.EnableDynamicTaskLimit {
		g.Go(func() error {
			var (
				lastBytes      uint64
				lastFailed     uint64
				lastThroughput float64
				stagnantTicks  int
			)
			sampleWindow := 2 * time.Second

			for {
				select {
				case <-gCtx.Done():
					return nil
				case <-time.After(sampleWindow):
					cl := atomic.LoadInt32(&currentLimit)
					act := atomic.LoadInt32(&active)
					if len(taskCh) == 0 && act == 0 {
						return nil
					}

					currBytes := atomic.LoadUint64(&s.results.BytesDone)
					bytesDiff := currBytes - lastBytes
					lastBytes = currBytes

					currFailed := atomic.LoadUint64(&s.results.Failed)
					failedDiff := currFailed - lastFailed
					lastFailed = currFailed

					throughput := float64(bytesDiff) / sampleWindow.Seconds()
					if failedDiff > 0 && cl > minLimit {
						atomic.AddInt32(&currentLimit, -1)
						stagnantTicks = 0
						lastThroughput = throughput
						continue
					}

					if throughput == 0 {
						stagnantTicks++
						if stagnantTicks >= 2 && act >= cl && cl > minLimit {
							atomic.AddInt32(&currentLimit, -1)
							stagnantTicks = 0
						}
						continue
					}

					stagnantTicks = 0
					if act >= cl && cl < maxLimit {
						if lastThroughput == 0 || throughput >= lastThroughput*0.9 {
							atomic.AddInt32(&currentLimit, 1)
						}
					}
					lastThroughput = throughput
				}
			}
		})
	}

	waitErr := g.Wait()
	albumBar.SetCurrent(int64(len(photos)))
	albumBar.SetTotal(int64(len(photos)), true)
	return waitErr
}

// downloadItem 负责处理单个文件 (照片/实况图/普通视频) 的分析、路径拼接、断点续传检查与下载调用。
func (s *Spider) downloadItem(ctx context.Context, p *mpb.Progress, targetUin string, photo, album gjson.Result, albumPath string, exclude bool, localFiles map[string]string) error {
	select {
	case <-ctx.Done():
		return nil
	default:
	}

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

	var tasks []mediaTask
	isVideo := photo.Get("is_video").Bool()

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

	if isVideo {
		source, videoErr := s.client.GetVideoSource(ctx, targetUin, album.Get("id").String(), sloc, photo)
		if videoErr != nil || source == nil || len(source.Candidates) == 0 {
			s.results.addFailedItem(s.makeFailedItem(targetUin, album, photo, sloc, videoErr, true))
			return nil
		}
		vidFilename := fmt.Sprintf("VID_%s_%s_%s.mp4", filenameDate[:8], filenameDate[8:], util.MD5(sloc)[8:24])
		tasks = append(tasks, mediaTask{
			candidates: source.Candidates,
			videoID:    source.VideoID,
			filename:   vidFilename,
			isVideo:    true,
		})
	} else {
		tasks = append(tasks, mediaTask{
			candidates: []qzone.VideoCandidate{{URL: imgSource, Kind: "image"}},
			filename:   imgFilename,
			isVideo:    false,
		})
	}

	for _, task := range tasks {
		isSkip := false
		var route videoRoute
		if exclude {
			base := strings.TrimSuffix(task.filename, filepath.Ext(task.filename))
			if existingPath, ok := localFiles[base]; ok {
				task.filename = filepath.Base(existingPath)
				if s.shouldSkipExisting(ctx, existingPath, task.candidates) {
					isSkip = true
				}
			}
		}

		if !isSkip {
			savePath := albumPath
			if s.config.EnableTimeline && shootDate != "" {
				savePath = filepath.Join(albumPath, shootDate[:4], shootDate[4:6])
				_ = os.MkdirAll(savePath, os.ModePerm)
			}

			target := filepath.Join(savePath, task.filename)
			res, downloadedRoute, downloadErr := s.downloadMedia(ctx, p, targetUin, task, target, originalName)
			route = downloadedRoute

			if downloadErr != nil {
				if task.isVideo {
					s.logVideoSourceResult(route, originalName, task.filename, "", downloadErr)
				}
				s.results.addFailedItem(s.makeFailedItem(targetUin, album, photo, task.filename, fmt.Errorf("download failed: %w", downloadErr), task.isVideo))
				continue
			}

			if res != nil {
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

		if !t.IsZero() {
			if chtimesErr := os.Chtimes(actualTarget, t, t); chtimesErr != nil {
				s.logger.Debugf("failed to set OS time for %s: %v", actualTarget, chtimesErr)
			}
		}

		s.updateResults(isSkip, task.isVideo)

		if !isSkip && task.isVideo {
			s.logVideoSourceResult(route, originalName, task.filename, actualTarget, nil)
		} else if s.logger.Level().Enabled(zap.DebugLevel) {
			fileSizeStr := "未知"
			if fi, statErr := os.Stat(actualTarget); statErr == nil {
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

func (s *Spider) shouldSkipExisting(ctx context.Context, existingPath string, candidates []qzone.VideoCandidate) bool {
	fi, err := os.Stat(existingPath)
	if err != nil || fi.Size() <= 0 {
		return false
	}
	for _, cand := range candidates {
		if cand.Kind == qzone.VideoSourceGetInfo || ihttp.IsHLSURL(cand.URL) {
			continue
		}
		head, headErr := s.client.Http.Head(ctx, cand.URL, map[string]string{"cookie": s.client.Cookie})
		if headErr != nil {
			continue
		}
		cLen, _ := strconv.ParseInt(head.Get("Content-Length"), 10, 64)
		if cLen > 0 && cLen <= fi.Size() {
			return true
		}
	}
	return false
}

type videoSourceAttempt struct {
	kind string
	err  error
}

type videoRoute struct {
	winner string
	failed []videoSourceAttempt
}

func (s *Spider) downloadMedia(ctx context.Context, p *mpb.Progress, targetUin string, task mediaTask, target, originalName string) (map[string]interface{}, videoRoute, error) {
	candidates := append([]qzone.VideoCandidate(nil), task.candidates...)
	route := videoRoute{}
	var (
		res     map[string]interface{}
		lastErr error
		tried   []string
		triedID bool
	)

	tryOne := func(cand qzone.VideoCandidate) (map[string]interface{}, error) {
		kind := cand.Kind
		if kind == "" {
			kind = "unknown"
		}
		out, err := s.downloadCandidate(ctx, p, targetUin, cand, target, task.filename, originalName, task.isVideo)
		if err != nil {
			route.failed = append(route.failed, videoSourceAttempt{kind: kind, err: err})
			tried = append(tried, fmt.Sprintf("%s: %v", kind, err))
			s.logger.Debugf("video source %s failed for %s: %v", kind, originalName, err)
			return nil, err
		}
		route.winner = kind
		return out, nil
	}

	for i := 0; i < len(candidates); i++ {
		select {
		case <-ctx.Done():
			return nil, route, ctx.Err()
		default:
		}

		res, lastErr = tryOne(candidates[i])
		if lastErr == nil {
			return res, route, nil
		}

		if task.isVideo && !triedID && task.videoID != "" && ihttp.IsDeadURL(lastErr) && i == len(candidates)-1 {
			triedID = true
			extra := s.client.ResolveTencentVideo(ctx, task.videoID)
			candidates = append(candidates, extra...)
		}
	}

	if task.isVideo && !triedID && task.videoID != "" {
		for _, extra := range s.client.ResolveTencentVideo(ctx, task.videoID) {
			res, lastErr = tryOne(extra)
			if lastErr == nil {
				return res, route, nil
			}
		}
	}

	if lastErr == nil {
		return nil, route, fmt.Errorf("no downloadable url")
	}
	if len(tried) <= 1 {
		return nil, route, lastErr
	}
	return nil, route, fmt.Errorf("all %d sources failed; last: %w; tried: %s", len(tried), lastErr, strings.Join(tried, " | "))
}

func (s *Spider) downloadCandidate(ctx context.Context, p *mpb.Progress, targetUin string, cand qzone.VideoCandidate, target, filename, originalName string, isVideo bool) (map[string]interface{}, error) {
	headers := s.buildDownloadHeaders(targetUin, cand.URL, isVideo, cand.Kind)
	res, err := s.doDownload(ctx, p, cand, target, filename, originalName, headers, isVideo)
	if err != nil && isVideo && ihttp.IsDeadURL(err) && headers["cookie"] != "" {
		// 部分播放 CDN 不接受跨域 Cookie，去掉后再试一次，更接近浏览器播放请求。
		anon := cloneHeaders(headers)
		delete(anon, "cookie")
		res, err = s.doDownload(ctx, p, cand, target, filename, originalName, anon, isVideo)
	}
	return res, err
}

func (s *Spider) doDownload(ctx context.Context, p *mpb.Progress, cand qzone.VideoCandidate, target, filename, originalName string, headers map[string]string, isVideo bool) (map[string]interface{}, error) {
	opts := s.videoBarOptions(cand, isVideo)
	if cand.Kind == qzone.VideoSourceHLS || ihttp.IsHLSURL(cand.URL) {
		return s.client.Http.DownloadHLS(ctx, cand.URL, target, headers, p, filename, originalName, s.trackWrittenBytes, opts...)
	}

	retry := 3
	if isVideo {
		retry = 2
		opts = append(opts, ihttp.WithFatalStatuses(
			nhttp.StatusForbidden,
			nhttp.StatusNotFound,
			nhttp.StatusGone,
		))
	}

	return s.client.Http.Download(
		ctx,
		cand.URL,
		target,
		headers,
		retry,
		600,
		p,
		filename,
		originalName,
		s.trackWrittenBytes,
		opts...,
	)
}

func (s *Spider) videoBarOptions(cand qzone.VideoCandidate, isVideo bool) []ihttp.DownloadOption {
	if !s.debugVideoEnabled() || !isVideo || cand.Kind == "" || cand.Kind == "image" {
		return nil
	}
	return []ihttp.DownloadOption{ihttp.WithBarLabel(videoSourceTag(cand.Kind))}
}

func (s *Spider) buildDownloadHeaders(targetUin, rawURL string, isVideo bool, kind string) map[string]string {
	headers := map[string]string{
		"cookie":     s.client.Cookie,
		"user-agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
	if !isVideo {
		return headers
	}
	if kind == qzone.VideoSourceGetInfo {
		headers["Referer"] = "https://v.qq.com/"
	} else {
		headers["Referer"] = fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", targetUin)
	}
	headers["Accept"] = "*/*"
	headers["Accept-Encoding"] = "identity;q=1, *;q=0"
	headers["Connection"] = "keep-alive"
	headers["Sec-Fetch-Dest"] = "video"
	headers["Sec-Fetch-Mode"] = "no-cors"
	headers["Sec-Fetch-Site"] = "cross-site"
	headers["Range"] = "bytes=0-"
	if u, parseErr := iurl.Parse(rawURL); parseErr == nil {
		headers["Host"] = u.Host
	}
	return headers
}

func cloneHeaders(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (s *Spider) debugVideoEnabled() bool {
	return s.config != nil && s.config.EnableDebug
}

func (s *Spider) resetVideoDebugLogs() {
	s.debugMu.Lock()
	s.debugLines = nil
	s.debugMu.Unlock()
}

func (s *Spider) enqueueVideoDebugLog(line string) {
	s.debugMu.Lock()
	s.debugLines = append(s.debugLines, line)
	s.debugMu.Unlock()
}

func (s *Spider) flushVideoDebugLogs() {
	s.debugMu.Lock()
	lines := s.debugLines
	s.debugLines = nil
	s.debugMu.Unlock()
	for _, line := range lines {
		s.logger.Info(line)
	}
}

func (s *Spider) logVideoDebugHint() {
	if !s.debugVideoEnabled() {
		return
	}
	s.logger.Info(fmt.Sprintf("🔍 调试模式：视频将标注实际拉取链路  %s %s %s %s",
		colorVideoSource(qzone.VideoSourceDownload, "["+videoSourceTag(qzone.VideoSourceDownload)+"]"),
		colorVideoSource(qzone.VideoSourcePlay, "[play]"),
		colorVideoSource(qzone.VideoSourceHLS, "[hls]"),
		colorVideoSource(qzone.VideoSourceGetInfo, "[getinfo]"),
	))
}

func (s *Spider) logVideoSourceResult(route videoRoute, originalName, filename, filePath string, downloadErr error) {
	if !s.debugVideoEnabled() {
		return
	}

	gray := color.New(color.FgWhite, color.Faint).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed, color.Bold).SprintFunc()
	chain := formatVideoFallbackChain(route.failed)

	if downloadErr != nil {
		tried := chain
		if tried == "" {
			tried = downloadErr.Error()
		}
		s.enqueueVideoDebugLog(fmt.Sprintf("🎬 %s  原文件: %s  已尝试: %s",
			red("视频拉取失败"),
			cyan(originalName),
			gray(tried),
		))
		return
	}

	sizeStr := "未知"
	if filePath != "" {
		if fi, err := os.Stat(filePath); err == nil {
			sizeStr = util.FormatBytes(fi.Size())
		}
	}

	winner := route.winner
	if winner == "" {
		winner = "unknown"
	}
	fallback := ""
	if chain != "" {
		fallback = "  " + gray("↩ "+chain)
	}

	s.enqueueVideoDebugLog(fmt.Sprintf("🎬 %s %s  原文件: %s → %s  (%s)%s",
		colorVideoSource(winner, "["+videoSourceTag(winner)+"]"),
		videoSourceTitle(winner),
		cyan(originalName),
		yellow(filename),
		green(sizeStr),
		fallback,
	))
}

func videoSourceTag(kind string) string {
	switch kind {
	case qzone.VideoSourceDownload:
		return "down"
	case "":
		return "unknown"
	default:
		return kind
	}
}

func videoSourceTitle(kind string) string {
	switch kind {
	case qzone.VideoSourceDownload:
		return "下载链"
	case qzone.VideoSourcePlay:
		return "播放链"
	case qzone.VideoSourceHLS:
		return "HLS"
	case qzone.VideoSourceGetInfo:
		return "vid 解析"
	default:
		if kind == "" {
			return "未知"
		}
		return kind
	}
}

func colorVideoSource(kind, text string) string {
	switch kind {
	case qzone.VideoSourceDownload:
		return color.New(color.FgGreen, color.Bold).Sprint(text)
	case qzone.VideoSourcePlay:
		return color.New(color.FgCyan, color.Bold).Sprint(text)
	case qzone.VideoSourceHLS:
		return color.New(color.FgMagenta, color.Bold).Sprint(text)
	case qzone.VideoSourceGetInfo:
		return color.New(color.FgYellow, color.Bold).Sprint(text)
	default:
		return color.New(color.Bold).Sprint(text)
	}
}

func formatVideoFallbackChain(failed []videoSourceAttempt) string {
	var parts []string
	last := ""
	for _, a := range failed {
		kind := a.kind
		if kind == "" {
			kind = "unknown"
		}
		part := videoSourceTag(kind)
		if reason := shortVideoErr(a.err); reason != "" {
			part += " " + reason
		}
		if part == last {
			continue
		}
		last = part
		parts = append(parts, part)
	}
	return strings.Join(parts, " → ")
}

func shortVideoErr(err error) string {
	if err == nil {
		return ""
	}
	if code := ihttp.HTTPStatus(err); code > 0 {
		return strconv.Itoa(code)
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.TrimPrefix(msg, "download failed: ")
	msg = strings.TrimPrefix(msg, "hls segment download failed: ")
	if len(msg) > 48 {
		return msg[:48] + "…"
	}
	return msg
}

func (s *Spider) buildAlbumPath(targetUin string, albumName string) string {
	baseDir := filepath.Join("storage", "qzone", targetUin, "album")
	safeName := sanitizePath(albumName)
	albumPath := filepath.Join(baseDir, safeName)

	if err := os.MkdirAll(albumPath, os.ModePerm); err != nil {
		albumPath = filepath.Join(baseDir, util.MD5(albumName)[8:24])
		_ = os.MkdirAll(albumPath, os.ModePerm)
	}

	return albumPath
}

func (s *Spider) buildLocalFileIndex(albumPath string, exclude bool) map[string]string {
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
		return localFiles
	}

	_ = os.RemoveAll(albumPath)
	_ = os.MkdirAll(albumPath, os.ModePerm)
	return localFiles
}

func (s *Spider) makeFailedItem(targetUin string, album, photo gjson.Result, name string, err error, isVideo bool) FailedItem {
	errMsg := "unknown error"
	if err != nil {
		errMsg = err.Error()
	}

	return FailedItem{
		Album:     album.Get("name").String(),
		Name:      name,
		Error:     errMsg,
		TargetUin: targetUin,
		AlbumID:   album.Get("id").String(),
		AlbumRaw:  album.Raw,
		PhotoRaw:  photo.Raw,
		IsVideo:   isVideo,
	}
}

func (s *Spider) trackWrittenBytes(delta int64) {
	if delta <= 0 {
		return
	}
	atomic.AddUint64(&s.results.BytesDone, uint64(delta))
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
