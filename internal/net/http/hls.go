/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-01
 * @LastEditors: qinjintian<514092640@qq.com>
 * @LastEditTime: 2026-09-04 09:57:00
 * @FileName: hls.go
 * @Description: [HLS/m3u8 播放链兜底下载，用于 download_url 失效时按网页播放器同样的分片方式拉取视频]
 */

package http

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// hlsVariant 表示主播放列表（master playlist）里的一条码率档。
// 下载时会选 BANDWIDTH 最高的那档，再递归去拉对应的媒体播放列表。
type hlsVariant struct {
	url       string // 该档对应的子播放列表绝对地址
	bandwidth int    // EXT-X-STREAM-INF 中的 BANDWIDTH，越大通常越清晰
}

// IsHLSURL 判断 URL 是否为 HLS 播放列表（路径含 .m3u8，或查询串含 m3u8?）。
func IsHLSURL(raw string) bool {
	u := strings.ToLower(raw)
	return strings.Contains(u, ".m3u8") || strings.Contains(u, "m3u8?")
}

// DownloadHLS 下载 m3u8 播放列表及其分片，并按顺序拼接为本地文件。
// 若远端实际返回的不是播放列表（例如仍是 mp4），则回退到普通 Download。
func (c *Client) DownloadHLS(ctx context.Context, playlistURL, target string, headers map[string]string, p *mpb.Progress, name, originalName string, onProgress func(int64), opts ...DownloadOption) (map[string]interface{}, error) {
	return c.downloadHLS(ctx, playlistURL, target, headers, p, name, originalName, onProgress, 0, opts...)
}

// downloadHLS 实际执行 HLS 拉取。depth 用于限制主播放列表嵌套层数，防止死循环。
func (c *Client) downloadHLS(ctx context.Context, playlistURL, target string, headers map[string]string, p *mpb.Progress, name, originalName string, onProgress func(int64), depth int, opts ...DownloadOption) (map[string]interface{}, error) {
	dopts := applyDownloadOptions(opts)
	if depth > 3 {
		return nil, fmt.Errorf("hls playlist nested too deep")
	}
	body, status, respHeader, err := c.getUnthrottled(ctx, playlistURL, headers)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK && status != http.StatusPartialContent {
		return nil, &StatusError{Code: status, Status: http.StatusText(status)}
	}

	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "#EXTM3U") {
		contentType := ""
		if respHeader != nil {
			contentType = respHeader.Get("Content-Type")
		}
		if !strings.Contains(strings.ToLower(contentType), "mpegurl") {
			// 有些「m3u8」地址实际仍返回 mp4，按普通文件下载即可。
			dlOpts := []DownloadOption{WithFatalStatuses(http.StatusForbidden, http.StatusNotFound, http.StatusGone)}
			if dopts.barLabel != "" {
				dlOpts = append(dlOpts, WithBarLabel(dopts.barLabel))
			}
			return c.Download(ctx, playlistURL, target, headers, 2, 600, p, name, originalName, onProgress, dlOpts...)
		}
		return nil, fmt.Errorf("invalid m3u8 playlist")
	}

	base, err := url.Parse(playlistURL)
	if err != nil {
		return nil, fmt.Errorf("invalid playlist url: %w", err)
	}

	variants, segments, initURI := parseM3U8(trimmed, base)
	if len(variants) > 0 {
		// 主播放列表只含多码率入口，选最高带宽那档继续解析媒体列表。
		best := variants[0]
		for _, v := range variants[1:] {
			if v.bandwidth > best.bandwidth {
				best = v
			}
		}
		return c.downloadHLS(ctx, best.url, target, headers, p, name, originalName, onProgress, depth+1, opts...)
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("m3u8 playlist has no segments")
	}

	targetDir := filepath.Dir(target)
	if !util.IsDir(targetDir) {
		if mkdirErr := os.MkdirAll(targetDir, os.ModePerm); mkdirErr != nil {
			return nil, fmt.Errorf("failed to create target directory: %w", mkdirErr)
		}
	}

	out, err := os.Create(target)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	// fMP4 的初始化段必须写在媒体分片前面，否则本地文件无法播放。
	parts := make([]string, 0, len(segments)+1)
	if initURI != "" {
		parts = append(parts, initURI)
	}
	parts = append(parts, segments...)

	var (
		bar       *mpb.Bar
		lastSpeed string
		copied    int64
	)
	startTime := time.Now()
	if p != nil {
		bar = p.AddBar(int64(len(parts)),
			mpb.BarRemoveOnComplete(),
			mpb.PrependDecorators(
				decor.Name(fmt.Sprintf("原文件: %s -> 保存为: %s", originalName, name), decor.WC{W: 55, C: decor.DindentRight}),
				decor.Name(progressSourceTag(dopts.barLabel, "  HLS ")),
				decor.CountersNoUnit("%d / %d"),
			),
			mpb.AppendDecorators(
				decor.Name(" | 速度: "),
				decor.Any(func(st decor.Statistics) string {
					if st.Completed && lastSpeed != "" {
						return lastSpeed
					}
					elapsed := time.Since(startTime)
					if elapsed < 100*time.Millisecond || copied <= 0 {
						return "0 B/s"
					}
					speed := float64(copied) / elapsed.Seconds()
					lastSpeed = util.FormatBytes(int64(speed)) + "/s"
					return lastSpeed
				}, decor.WC{W: 15, C: decor.DindentRight}),
			),
		)
	}

	for _, partURL := range parts {
		select {
		case <-ctx.Done():
			if bar != nil {
				bar.Abort(true)
			}
			return nil, ctx.Err()
		default:
		}

		segBody, segStatus, _, segErr := c.getUnthrottled(ctx, partURL, headers)
		if segErr != nil {
			if bar != nil {
				bar.Abort(true)
			}
			return nil, fmt.Errorf("hls segment download failed: %w", segErr)
		}
		if segStatus != http.StatusOK && segStatus != http.StatusPartialContent {
			if bar != nil {
				bar.Abort(true)
			}
			return nil, &StatusError{Code: segStatus, Status: http.StatusText(segStatus)}
		}
		n, writeErr := out.Write(segBody)
		if writeErr != nil {
			if bar != nil {
				bar.Abort(true)
			}
			return nil, writeErr
		}
		copied += int64(n)
		if onProgress != nil && n > 0 {
			onProgress(int64(n))
		}
		if bar != nil {
			bar.Increment()
		}
	}

	if bar != nil {
		bar.SetTotal(-1, true)
	}

	return map[string]interface{}{
		"filename": filepath.Base(target),
		"dir":      targetDir,
		"path":     target,
	}, nil
}

// getUnthrottled 直接 GET 指定 URL，不走全局限流。
// HLS 分片数量多、体积小，若走 Client.limiter 会把下载拖得极慢。
func (c *Client) getUnthrottled(ctx context.Context, rawURL string, headers map[string]string) ([]byte, int, http.Header, error) {
	resp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(headers).
		Get(rawURL)
	if err != nil {
		return nil, 0, nil, err
	}
	return resp.Body(), resp.StatusCode(), resp.Header(), nil
}

// parseM3U8 解析一份 m3u8 文本。
// 返回值依次为主播放列表变体、媒体分片绝对地址、以及 EXT-X-MAP 初始化段地址（fMP4 才有）。
func parseM3U8(body string, base *url.URL) ([]hlsVariant, []string, string) {
	var (
		variants  []hlsVariant
		segments  []string
		initURI   string
		bandwidth int
		isMaster  bool
	)

	scanner := bufio.NewScanner(strings.NewReader(body))
	// 默认 Scanner 缓冲对超长 URI 行不够，这里放到 1MB。
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			isMaster = true
			bandwidth = parseM3U8AttrInt(line, "BANDWIDTH")
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-MAP:") {
			if uri := parseM3U8QuotedURI(line); uri != "" {
				initURI = resolvePlaylistURL(base, uri)
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		abs := resolvePlaylistURL(base, line)
		if abs == "" {
			continue
		}
		if isMaster {
			variants = append(variants, hlsVariant{url: abs, bandwidth: bandwidth})
			bandwidth = 0
			continue
		}
		segments = append(segments, abs)
	}
	return variants, segments, initURI
}

// parseM3U8AttrInt 从标签行里取出整型属性，例如 BANDWIDTH=2500000。
func parseM3U8AttrInt(line, key string) int {
	upper := strings.ToUpper(line)
	idx := strings.Index(upper, key+"=")
	if idx < 0 {
		return 0
	}
	rest := line[idx+len(key)+1:]
	end := strings.IndexAny(rest, ",\r\n")
	if end >= 0 {
		rest = rest[:end]
	}
	n, _ := strconv.Atoi(strings.TrimSpace(rest))
	return n
}

// parseM3U8QuotedURI 从 EXT-X-MAP:URI="..." 这类标签中取出 URI。
func parseM3U8QuotedURI(line string) string {
	idx := strings.Index(strings.ToUpper(line), "URI=")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(line[idx+4:])
	if strings.HasPrefix(rest, "\"") {
		rest = rest[1:]
		if end := strings.Index(rest, "\""); end >= 0 {
			return rest[:end]
		}
	}
	end := strings.IndexAny(rest, ",\r\n")
	if end >= 0 {
		rest = rest[:end]
	}
	return strings.Trim(rest, "\"")
}

// resolvePlaylistURL 把播放列表里的相对路径解析成绝对 URL。
func resolvePlaylistURL(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || base == nil {
		return ref
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return base.ResolveReference(u).String()
}
