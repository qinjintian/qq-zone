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
 * @LastEditTime: 2026-09-04 17:10:00
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

	nhttp "net/http"
	iurl "net/url"

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

// FailedKindShuoShuo 标记这条失败记录来自说说备份，重试时走说说链路而不是相册链路。
const FailedKindShuoShuo = "shuoshuo"

// FailedItem 记录单个媒体文件失败时的完整上下文，既用于控制台展示，也用于后续失败重试。
type FailedItem struct {
	Album     string `json:"album"`                // 相册名称，失败清单和重试菜单展示用；说说固定为「说说」
	Name      string `json:"name"`                 // 本地保存文件名（失败时也可能是 sloc）
	Error     string `json:"error"`                // 失败原因
	TargetUin string `json:"target_uin,omitempty"` // 被备份的 QQ 号
	AlbumID   string `json:"album_id,omitempty"`   // 空间侧相册 ID，用来重新定位相册
	AlbumRaw  string `json:"album_raw,omitempty"`  // 当时的相册 JSON，重试时还原字段
	PhotoRaw  string `json:"photo_raw,omitempty"`  // 当时的照片 JSON，重试时重新解析 URL
	IsVideo   bool   `json:"is_video,omitempty"`   // 是否视频（含按视频链路保存的实况图）
	Kind      string `json:"kind,omitempty"`       // 空=相册；shuoshuo=说说
	MoodTID   string `json:"mood_tid,omitempty"`   // 说说 tid，重试时重新拉详情换新地址
	MediaURL  string `json:"media_url,omitempty"`  // 上次失败时用过的下载地址，详情失败时作兜底
	MediaID   string `json:"media_id,omitempty"`   // 配图/视频在该条说说里的稳定 id
}

// DownloadResult 用于原子化地统计整个备份任务的最终成果与各项指标。
type DownloadResult struct {
	Total       uint64       // 计划处理数：相册=媒体文件；说说=说说条数
	Success     uint64       // 成功数（含增量跳过）：相册=文件；说说=说说条数
	NewAdded    uint64       // 本次任务中全新下载的文件数（不含跳过）
	Skipped     uint64       // 触发增量策略被跳过的已存在文件数
	Failed      uint64       // 失败数：相册=文件；说说=配图/视频失败
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
	client    *qzone.Client      // 已登录的空间客户端
	whitelist map[string]bool    // 用户勾选的相册名；空 map 表示备份全部
	config    *Config            // 并发、时间线、调试等任务配置
	logger    *zap.SugaredLogger // 本次任务日志
	results   DownloadResult     // 整次任务的成功/失败统计

	debugMu    sync.Mutex      // 保护下面调试日志队列和链路计数，下载协程会并发写入
	debugLines []string        // 回退/失败明细，等当前相册进度条结束后再打印，避免和 mpb 抢终端
	albumStats videoDebugStats // 当前相册内各视频链路的命中次数
	taskStats  videoDebugStats // 整次备份任务的链路命中次数，用于最后一行「全部视频」汇总
}

// videoDebugStats 统计调试模式下各视频实际走了哪条拉取链路。
type videoDebugStats struct {
	down, play, hls, getinfo, fail uint64
}

// videos 返回计入统计的视频条数（含失败）。
func (st videoDebugStats) videos() uint64 {
	return st.down + st.play + st.hls + st.getinfo + st.fail
}

type mediaTask struct {
	candidates []qzone.VideoCandidate // 按优先级排列的下载地址：download → play → hls，getinfo 后补
	videoID    string                 // 腾讯视频 vid，下载链和播放链都失效时用来换 CDN
	filename   string                 // 本地保存文件名（IMG_/VID_ + 时间 + 哈希）
	isVideo    bool
}

// NewSpider 实例化一个下载爬虫。albums 为用户勾选的相册名列表，空切片表示全部备份。
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

// Download 开始执行批量相册下载任务。相册串行处理，单个相册内部再并发下文件。
func (s *Spider) Download(ctx context.Context, targetUin string, exclude bool) (*DownloadResult, error) {
	s.results = DownloadResult{}

	p := mpb.NewWithContext(ctx)
	wait := waitSpinner(p, "正在拉取相册列表")
	albums, err := s.client.GetAlbumList(ctx, targetUin)
	stopWaitSpinner(wait)
	if err != nil {
		p.Wait()
		return nil, err
	}

	if len(albums) == 0 {
		s.logger.Warnf("未发现任何相册，请确认账号 [%s] 空间是否开放或登录是否失效", targetUin)
	}

	// 丢掉无权访问的，以及用户没勾选的相册。
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

	// 调试模式：任务开始打印图例，相册结束打回退明细和汇总，全部结束后再打总汇总。
	s.resetVideoDebugLogs()
	s.logVideoDebugHint()

	// 相册必须一个下完再下下一个，避免同时打太多相册列表接口触发风控。
	for i, album := range filteredAlbums {
		select {
		case <-ctx.Done():
			s.logger.Warn("任务已被用户取消")
			p.Wait()
			s.flushVideoDebugLogs()
			s.flushTaskVideoSummary()
			return &s.results, nil
		default:
		}

		if err := s.downloadAlbum(ctx, p, targetUin, album, i+1, len(filteredAlbums), exclude); err != nil {
			s.logger.Errorf("failed to download album [%s]: %v", album.Get("name").String(), err)
		}
	}

	p.Wait()
	s.flushVideoDebugLogs()
	s.flushTaskVideoSummary()
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
	// 失败项逐条重试，不再走相册级动态并发，避免一次把所有失败 URL 打爆。
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

	localFileCache := make(map[string]map[string]string) // 同一相册只扫一遍本地文件
	for _, item := range failedItems {
		select {
		case <-ctx.Done():
			p.Wait()
			s.flushVideoDebugLogs()
			s.flushTaskVideoSummary()
			return &s.results, nil
		default:
		}

		// 重试依赖当时保存的 album_raw / photo_raw；缺了无法还原 URL，只能记失败让用户重新备份。
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

		// 始终按增量下，避免把已经成功的文件清掉重来。
		_ = s.downloadItem(ctx, p, targetUin, photo, album, albumPath, true, localFiles)
		retryBar.Increment()
	}

	retryBar.SetTotal(-1, true)
	p.Wait()
	s.flushVideoDebugLogs()
	s.flushTaskVideoSummary()
	return &s.results, nil
}

// downloadAlbum 负责下载单个相册内的所有照片和视频。
// 并发由 active（正在下的文件数）和 currentLimit（同时允许几路）卡住；
// 开启智能动态并发时，另有观察协程每 2 秒按吞吐和失败数加减上限。
func (s *Spider) downloadAlbum(ctx context.Context, p *mpb.Progress, targetUin string, album gjson.Result, albumIdx, albumTotal int, exclude bool) error {
	albumName := album.Get("name").String()
	albumID := album.Get("id").String()
	albumPath := s.buildAlbumPath(targetUin, albumName)
	s.resetAlbumVideoStats()

	// 把相册原始 JSON 落到目录里，方便以后对照空间侧字段。
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

	// active：此刻正在下载的文件数。
	// currentLimit：同时最多允许多少路；auto 模式下观察协程会改这个数，已开工的下载不会被杀掉。
	var active int32
	baseLimit := int32(s.config.TaskLimit)
	if baseLimit < 1 {
		baseLimit = 10
	}
	currentLimit := baseLimit
	maxLimit := baseLimit * 3 // auto 时上限最多涨到起步值的 3 倍（默认 10→30）
	minLimit := int32(1)

	taskCh := make(chan int, len(photos))
	for i := range photos {
		taskCh <- i
	}
	close(taskCh)

	// 派活：队列里还有文件，且 active 未顶到 currentLimit 时才新开一路下载。
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

	// 智能动态并发：每 2 秒看吞吐和失败，只改 currentLimit（+1 / -1 / 不动）。
	// 「还行」不是和某个固定 KB/s 比，而是这一轮速度 ≥ 上一轮速度的 90%。
	if s.config.EnableDynamicTaskLimit {
		g.Go(func() error {
			var (
				lastBytes      uint64  // 上一轮结束时的累计写盘字节（BytesDone 的记号）
				lastFailed     uint64  // 上一轮结束时的累计失败数
				lastThroughput float64 // 上一轮速度（字节/秒）；0 表示还没有上一轮
				stagnantTicks  int     // 连续多少轮吞吐为 0
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

					// 这一轮 2 秒写了多少 = 现在的累计写盘量 − 上一轮记号。
					currBytes := atomic.LoadUint64(&s.results.BytesDone)
					bytesDiff := currBytes - lastBytes
					lastBytes = currBytes

					currFailed := atomic.LoadUint64(&s.results.Failed)
					failedDiff := currFailed - lastFailed
					lastFailed = currFailed

					// 当前速度 = 这两秒写入的字节数 ÷ 2。
					throughput := float64(bytesDiff) / sampleWindow.Seconds()

					// 这两秒里有新失败：上限 -1，先降压（解析失败、超时、403 等都会进来）。
					if failedDiff > 0 && cl > minLimit {
						atomic.AddInt32(&currentLimit, -1)
						stagnantTicks = 0
						lastThroughput = throughput
						continue
					}

					// 吞吐为 0：连续约 4 秒（2 轮）且名额已用满，再 -1。这一轮不更新 lastThroughput。
					if throughput == 0 {
						stagnantTicks++
						if stagnantTicks >= 2 && act >= cl && cl > minLimit {
							atomic.AddInt32(&currentLimit, -1)
							stagnantTicks = 0
						}
						continue
					}

					stagnantTicks = 0
					// 加并发必须同时：没新失败、人已经满、还没到 maxLimit。
					// lastThroughput==0：第一轮没有对照，人满就可以试着 +1。
					// 之后：当前速度 ≥ 上一轮 × 90% 才叫还行；掉超过 10% 则不加不减。
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
	// 进度条完成后再打调试日志，避免 Windows 上和 mpb 抢同一块终端。
	s.flushVideoDebugLogs()
	s.flushAlbumVideoSummary(albumName)
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

	// 拍摄时间优先，没有再用上传时间；后面用来起文件名、按年/月归档、回写本地修改时间。
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

	// 图片地址：raw → origin_url → url；b&bo= 改成 o&bo= 尽量拿原图而不是预览。
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
	// 文件名：前缀 + 日期_时分秒 + sloc 哈希，避免中文原名和重名冲突。
	imgFilename := fmt.Sprintf("%s%s_%s_%s", imgPrefix, filenameDate[:8], filenameDate[8:], util.MD5(sloc)[8:24])
	ext := ".jpg"
	if strings.Contains(imgSource, ".png") {
		ext = ".png"
	} else if strings.Contains(imgSource, ".gif") {
		ext = ".gif"
	}
	imgFilename += ext

	if isVideo {
		// 下载时再解析视频多源，避免相册列表里的 URL 排队后过期。
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
		// 增量模式：本地已有同名（无扩展名）文件，且体积看起来完整，则跳过下载。
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
			// 按拍摄时间归档到 年/月 子目录。
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

			// 下载层可能根据 Content-Type 改过扩展名，后面回写时间和统计要用最终文件名。
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

		// 把本地修改时间改成拍摄时间，导入系统相册后时间线才是当年那一天。
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

// shouldSkipExisting 用 HEAD 的 Content-Length 对比本地文件大小，判断增量模式下能否跳过。
func (s *Spider) shouldSkipExisting(ctx context.Context, existingPath string, candidates []qzone.VideoCandidate) bool {
	fi, err := os.Stat(existingPath)
	if err != nil || fi.Size() <= 0 {
		return false
	}
	for _, cand := range candidates {
		// getinfo / HLS 的 HEAD 往往拿不到可靠体积，不能用来判断本地文件是否已经完整。
		if cand.Kind == qzone.VideoSourceGetInfo || ihttp.IsHLSURL(cand.URL) {
			continue
		}
		head, headErr := s.client.Http.Head(ctx, cand.URL, map[string]string{"cookie": s.client.Cookie})
		if headErr != nil {
			continue
		}
		cLen, _ := strconv.ParseInt(head.Get("Content-Length"), 10, 64)
		// 远端声明的长度不超过本地已有大小，认为已经下全了。
		if cLen > 0 && cLen <= fi.Size() {
			return true
		}
	}
	return false
}

// videoSourceAttempt 记录某条候选地址失败时的来源类型和错误，用于调试输出「↩ down 403」。
type videoSourceAttempt struct {
	kind string
	err  error
}

// videoRoute 描述一次视频下载最终命中了哪条链路，以及前面失败过哪些源。
type videoRoute struct {
	winner string               // 成功那条源的 Kind：download / play / hls / getinfo
	failed []videoSourceAttempt // 成功前已失败的源，为空表示 download_url 一次成功
}

// downloadMedia 按候选列表依次尝试下载。视频遇到 403/404 会立刻换源，而不是当作风控重试死链。
func (s *Spider) downloadMedia(ctx context.Context, p *mpb.Progress, targetUin string, task mediaTask, target, originalName string) (map[string]interface{}, videoRoute, error) {
	candidates := append([]qzone.VideoCandidate(nil), task.candidates...)
	route := videoRoute{}
	var (
		res     map[string]interface{}
		lastErr error
		tried   []string
		triedID bool // 是否已经用 vid 调过腾讯 getinfo，避免每个失败都重复打接口
	)

	// tryOne 尝试一条候选；失败记入 route.failed，成功则记下 winner。
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

	// 按候选顺序试：前一条死链（403/404）就立刻换下一条，成功则记下 winner。
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

		// 列表里的 download/play/hls 都失效后，再按 vid 解析真实 CDN，追加到候选末尾继续试。
		if task.isVideo && !triedID && task.videoID != "" && ihttp.IsDeadURL(lastErr) && i == len(candidates)-1 {
			triedID = true
			extra := s.client.ResolveTencentVideo(ctx, task.videoID)
			candidates = append(candidates, extra...)
		}
	}

	if task.isVideo && !triedID && task.videoID != "" {
		// 最后一次失败不是 403/404 时上面不会预取 getinfo，这里再兜底一次。
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

// downloadCandidate 下载单条候选地址；视频若带 Cookie 被 403/404，会去掉 Cookie 再试一次。
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

// doDownload 按候选类型走 HLS 分片下载或普通直链下载。
func (s *Spider) doDownload(ctx context.Context, p *mpb.Progress, cand qzone.VideoCandidate, target, filename, originalName string, headers map[string]string, isVideo bool) (map[string]interface{}, error) {
	opts := s.videoBarOptions(cand, isVideo)
	if cand.Kind == qzone.VideoSourceHLS || ihttp.IsHLSURL(cand.URL) {
		return s.client.Http.DownloadHLS(ctx, cand.URL, target, headers, p, filename, originalName, s.trackWrittenBytes, opts...)
	}

	retry := 3
	if isVideo {
		retry = 2
		// 视频 403/404 视为当前 URL 失效，立即失败以便换下一条源，不要当作风控冷却。
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

// videoBarOptions 调试模式下在进度条上标注 [down]/[play]/[hls]/[getinfo]。
func (s *Spider) videoBarOptions(cand qzone.VideoCandidate, isVideo bool) []ihttp.DownloadOption {
	if !s.debugVideoEnabled() || !isVideo || cand.Kind == "" || cand.Kind == "image" {
		return nil
	}
	return []ihttp.DownloadOption{ihttp.WithBarLabel(videoSourceTag(cand.Kind))}
}

// buildDownloadHeaders 构造下载请求头。getinfo 解析出的 CDN 需要腾讯视频 Referer，其余走 QQ 空间页。
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

// cloneHeaders 浅拷贝请求头，用于去掉 Cookie 后再试一次。
func cloneHeaders(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// debugVideoEnabled 是否开启了菜单里的调试模式（会标注视频拉取链路）。
func (s *Spider) debugVideoEnabled() bool {
	return s.config != nil && s.config.EnableDebug
}

// resetVideoDebugLogs 任务开始时清空链路明细和计数。
func (s *Spider) resetVideoDebugLogs() {
	s.debugMu.Lock()
	s.debugLines = nil
	s.albumStats = videoDebugStats{}
	s.taskStats = videoDebugStats{}
	s.debugMu.Unlock()
}

// resetAlbumVideoStats 进入下一个相册前，只清相册级计数和待打印明细，总汇总继续累加。
func (s *Spider) resetAlbumVideoStats() {
	s.debugMu.Lock()
	s.albumStats = videoDebugStats{}
	s.debugLines = nil
	s.debugMu.Unlock()
}

// enqueueVideoDebugLog 把一条回退/失败明细放进队列，等相册进度条结束后再输出。
func (s *Spider) enqueueVideoDebugLog(line string) {
	s.debugMu.Lock()
	s.debugLines = append(s.debugLines, line)
	s.debugMu.Unlock()
}

// recordVideoDebugLocked 按最终命中的链路给相册和任务计数器 +1。调用方必须已持有 debugMu。
func (s *Spider) recordVideoDebugLocked(route videoRoute, downloadErr error) {
	if downloadErr != nil {
		s.albumStats.fail++
		s.taskStats.fail++
		return
	}
	switch route.winner {
	case qzone.VideoSourcePlay:
		s.albumStats.play++
		s.taskStats.play++
	case qzone.VideoSourceHLS:
		s.albumStats.hls++
		s.taskStats.hls++
	case qzone.VideoSourceGetInfo:
		s.albumStats.getinfo++
		s.taskStats.getinfo++
	default:
		s.albumStats.down++
		s.taskStats.down++
	}
}

// flushVideoDebugLogs 把队列里的回退/失败明细打印到终端和日志。
func (s *Spider) flushVideoDebugLogs() {
	s.debugMu.Lock()
	lines := s.debugLines
	s.debugLines = nil
	s.debugMu.Unlock()
	for _, line := range lines {
		s.logger.Info(line)
	}
}

// flushAlbumVideoSummary 输出当前相册的链路汇总，例如：🎬 相册 [我的家人]  [down] 980  [play] 15 ...
func (s *Spider) flushAlbumVideoSummary(albumName string) {
	if !s.debugVideoEnabled() {
		return
	}
	s.debugMu.Lock()
	st := s.albumStats
	s.albumStats = videoDebugStats{}
	s.debugMu.Unlock()
	if line := formatVideoDebugSummary(fmt.Sprintf("相册 [%s]", albumName), st); line != "" {
		s.logger.Info(line)
	}
}

// flushTaskVideoSummary 输出整次任务的链路总汇总，例如：🎬 全部视频  [down] 981  [play] 15 ...
func (s *Spider) flushTaskVideoSummary() {
	if !s.debugVideoEnabled() {
		return
	}
	s.debugMu.Lock()
	st := s.taskStats
	s.taskStats = videoDebugStats{}
	s.debugMu.Unlock()
	if line := formatVideoDebugSummary("全部视频", st); line != "" {
		s.logger.Info(line)
	}
}

// formatVideoDebugSummary 生成一行链路计数文案；本次没有视频则返回空串。
func formatVideoDebugSummary(label string, st videoDebugStats) string {
	if st.videos() == 0 {
		return ""
	}
	fail := ""
	if st.fail > 0 {
		fail = "  " + color.New(color.FgRed).Sprintf("失败 %d", st.fail)
	}
	return fmt.Sprintf("🎬 %s  %s %d  %s %d  %s %d  %s %d%s",
		label,
		colorVideoSource(qzone.VideoSourceDownload, "[down]"),
		st.down,
		colorVideoSource(qzone.VideoSourcePlay, "[play]"),
		st.play,
		colorVideoSource(qzone.VideoSourceHLS, "[hls]"),
		st.hls,
		colorVideoSource(qzone.VideoSourceGetInfo, "[getinfo]"),
		st.getinfo,
		fail,
	)
}

// logVideoDebugHint 任务开始时打印图例，说明直链成功只进汇总、回退/失败才逐条打印。
func (s *Spider) logVideoDebugHint() {
	if !s.debugVideoEnabled() {
		return
	}
	s.logger.Info(fmt.Sprintf("🔍 调试模式：直链成功只计入汇总；回退/失败会逐条打印  %s %s %s %s",
		colorVideoSource(qzone.VideoSourceDownload, "["+videoSourceTag(qzone.VideoSourceDownload)+"]"),
		colorVideoSource(qzone.VideoSourcePlay, "[play]"),
		colorVideoSource(qzone.VideoSourceHLS, "[hls]"),
		colorVideoSource(qzone.VideoSourceGetInfo, "[getinfo]"),
	))
}

// logVideoSourceResult 记录一条视频的链路结果。download_url 一次成功只计数；回退或失败才入队待打印。
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

	s.debugMu.Lock()
	s.recordVideoDebugLocked(route, downloadErr)
	s.debugMu.Unlock()

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

	// 直链一次成功：不刷屏，只体现在相册/任务汇总里。
	isFallback := len(route.failed) > 0 || (route.winner != "" && route.winner != qzone.VideoSourceDownload)
	if !isFallback {
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

// videoSourceTag 终端标签：download 显示为更短的 [down]，其余 Kind 原样输出。
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

// videoSourceTitle 链路中文名，跟在彩色标签后面。
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

// colorVideoSource 按链路类型给标签上色，便于扫一眼区分。
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

// formatVideoFallbackChain 把失败过的源压成「down 403 → play 404」，相邻相同项会合并。
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

// shortVideoErr 调试后缀里优先打 HTTP 状态码，避免把整段错误堆到一行。
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

// buildAlbumPath 生成本地相册目录 storage/qzone/<QQ>/album/<相册名>；非法路径名则改用哈希。
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

// buildLocalFileIndex 增量时按「去掉扩展名的文件名」建索引，用来匹配即将下载的 IMG_/VID_ 文件。
// 非增量则清空相册目录，准备全量重下。
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

// makeFailedItem 把当前相册/照片的原始 JSON 一并记下，失败重试时才能重新解析 URL。
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

// trackWrittenBytes 把本次写出的字节累加进 BytesDone，动态并发每 2 秒用它算吞吐。
func (s *Spider) trackWrittenBytes(delta int64) {
	if delta <= 0 {
		return
	}
	atomic.AddUint64(&s.results.BytesDone, uint64(delta))
}

// updateResults 更新成功计数；跳过的记入 Skipped，新下的记入 NewAdded，并区分图片/视频。
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

// sanitizePath 去掉相册名里不能当文件夹名的字符，避免 Windows 建目录失败。
func sanitizePath(name string) string {
	name = strings.TrimSuffix(name, ".")
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	for _, char := range invalid {
		name = strings.ReplaceAll(name, char, "_")
	}
	return strings.TrimSpace(name)
}
