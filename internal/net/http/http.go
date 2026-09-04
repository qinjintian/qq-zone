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
 * @FileName: http.go
 * @Description: [定制化 HTTP 客户端封装，支持带进度的大文件下载、安全续传与通用 GET/POST 请求]
 */

package http

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/go-resty/resty/v2"
	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/time/rate"
)

// Client 封装了基于 go-resty 的 HTTP 客户端。
type Client struct {
	resty   *resty.Client // 实际发请求；超时和重试由本包自己管，不走 resty 默认
	limiter *rate.Limiter // 全局限流，保护空间 CGI；Download 大文件不走这里，避免被 500ms 卡住
}

// resumeMetadata 写在目标文件旁的 sidecar（*.resume.json），用来判断半成品能不能接着 Range。
type resumeMetadata struct {
	URI          string    `json:"uri"`           // 这份半成品对应的下载 URL，换源后必须作废
	ETag         string    `json:"etag,omitempty"`          // 远端实体标签，续传时放进 If-Range
	LastModified string    `json:"last_modified,omitempty"` // 没有 ETag 时用修改时间做 If-Range
	UpdatedAt    time.Time `json:"updated_at"`              // sidecar 写入时间
}

// progressReader 包一层响应体，把每次读到的字节数回调给上层（动态并发算吞吐用）。
type progressReader struct {
	reader     io.Reader
	onProgress func(int64)
}

// StatusError 表示远端以明确 HTTP 状态码拒绝了这次下载。
type StatusError struct {
	Code   int    // HTTP 状态码，例如 403
	Status string // 状态文本，例如 Forbidden
}

func (e *StatusError) Error() string {
	if e == nil {
		return "download failed with status: unknown"
	}
	return fmt.Sprintf("download failed with status: %s", e.Status)
}

// HTTPStatus 从错误中提取 HTTP 状态码；无法识别时返回 0。
func HTTPStatus(err error) int {
	var se *StatusError
	if errors.As(err, &se) && se != nil {
		return se.Code
	}
	return 0
}

// IsDeadURL 判断该错误是否表示“这条 URL 本身不可用”，应立即换源而不是当作风控重试。
func IsDeadURL(err error) bool {
	switch HTTPStatus(err) {
	case http.StatusForbidden, http.StatusNotFound, http.StatusGone, http.StatusUnavailableForLegalReasons:
		return true
	default:
		if err == nil {
			return false
		}
		msg := err.Error()
		return strings.Contains(msg, "403") ||
			strings.Contains(msg, "404") ||
			strings.Contains(msg, "410") ||
			strings.Contains(msg, "Forbidden") ||
			strings.Contains(msg, "Not Found")
	}
}

type downloadOptions struct {
	fatalStatuses map[int]bool // 命中这些状态码立即返回，不再 waitRetry
	barLabel      string       // 进度条上的链路标签，例如 down / play
}

// DownloadOption 用于微调单次下载行为。
type DownloadOption func(*downloadOptions)

// WithFatalStatuses 指定遇到这些状态码时立即失败、不再退避重试。
// 视频多源兜底会把 403/404 视为当前 URL 失效，从而立刻切换到播放链。
func WithFatalStatuses(codes ...int) DownloadOption {
	return func(o *downloadOptions) {
		if o.fatalStatuses == nil {
			o.fatalStatuses = make(map[int]bool, len(codes))
		}
		for _, code := range codes {
			o.fatalStatuses[code] = true
		}
	}
}

// WithBarLabel 在进度条上标注当前拉取链路，例如 down / play / hls / getinfo。
func WithBarLabel(label string) DownloadOption {
	return func(o *downloadOptions) {
		o.barLabel = strings.TrimSpace(label)
	}
}

// progressSourceTag 拼进度条上的 [down] 一类标签；没传 label 时用 fallback。
func progressSourceTag(label, fallback string) string {
	if label != "" {
		return fmt.Sprintf("  [%s] ", label)
	}
	if fallback != "" {
		return fallback
	}
	return "  "
}

// applyDownloadOptions 把可变参数收成一份 downloadOptions。
func applyDownloadOptions(opts []DownloadOption) downloadOptions {
	var o downloadOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// Read 读底层响应体，并把本次读到的字节数回调给 onProgress。
func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 && r.onProgress != nil {
		r.onProgress(int64(n))
	}
	return n, err
}

// NewClient 初始化一个全局 HTTP 客户端。
// 关闭 resty 自带超时和重试，大文件下载由 Download 自己控；不使用 CookieJar，Cookie 一律由调用方传入。
func NewClient() *Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, // 部分空间 CDN 证书不规范
		TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		ForceAttemptHTTP2:     false, // 强制 HTTP/1.1，避免部分 CDN 的 HTTP/2 断流
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}

	return &Client{
		resty: resty.New().
			SetTransport(transport).
			SetTimeout(0).
			SetRetryCount(0).
			SetCookieJar(nil),
		limiter: rate.NewLimiter(rate.Every(500*time.Millisecond), 1), // CGI 默认约 2 次/秒
	}
}

// SetRateLimit 动态调整请求频率限制。
func (c *Client) SetRateLimit(r rate.Limit, b int) {
	c.limiter.SetLimit(r)
	c.limiter.SetBurst(b)
}

// Get 发起一个基础的 HTTP GET 请求，受全局速率限制保护。
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) (http.Header, []byte, int, error) {
	// 先走全局限流，保护空间 CGI；大文件 Download 不走这条。
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, nil, 0, err
	}
	resp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(headers).
		Get(url)

	if err != nil {
		return nil, nil, 0, err
	}
	return resp.Header(), resp.Body(), resp.StatusCode(), nil
}

// Head 发起一个 HTTP HEAD 请求，常用于获取文件大小而不下载实体。
func (c *Client) Head(ctx context.Context, url string, headers map[string]string) (http.Header, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	resp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(headers).
		Head(url)

	if err != nil {
		return nil, err
	}
	if resp.IsError() {
		return nil, fmt.Errorf("http request failed with status: %s", resp.Status())
	}
	return resp.Header(), nil
}

// PostForm 发起一个 HTTP POST 表单请求，用于提交数据。
func (c *Client) PostForm(ctx context.Context, url string, params map[string]string, headers map[string]string) ([]byte, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	resp, err := c.resty.R().
		SetContext(ctx).
		SetHeaders(headers).
		SetFormData(params).
		Post(url)

	if err != nil {
		return nil, err
	}
	if resp.IsError() {
		return nil, fmt.Errorf("http request failed with status: %s", resp.Status())
	}
	return resp.Body(), nil
}

// Download 执行大文件流式下载，支持 Range 续传、换源时作废半成品、以及按状态码退避重试。
// onProgress 会在每次成功读取到响应体字节后回调，用于上层吞吐量统计。
// timeout 参数保留是为了兼容旧调用方，当前不再生效（传输超时由连接层控制）。
func (c *Client) Download(ctx context.Context, uri string, target string, headers map[string]string, retry int, timeout int, p *mpb.Progress, name string, originalName string, onProgress func(int64), opts ...DownloadOption) (res map[string]interface{}, err error) {
	dopts := applyDownloadOptions(opts)
	targetDir := filepath.Dir(target)
	if !util.IsDir(targetDir) {
		if mkdirErr := os.MkdirAll(targetDir, os.ModePerm); mkdirErr != nil {
			return nil, fmt.Errorf("failed to create target directory: %w", mkdirErr)
		}
	}

	_ = timeout

	currentTarget := target
	currentName := name
	maxAttempts := retry + 1

	for attempt := 0; attempt < maxAttempts; attempt++ {
		startBytes := int64(0)
		if fi, statErr := os.Stat(currentTarget); statErr == nil {
			startBytes = fi.Size()
		}

		metaPath := resumeMetadataPath(currentTarget)
		meta, _ := loadResumeMetadata(metaPath)

		// 有半成品但 sidecar 缺失，或 sidecar 里的 URL 和当前源不一致：不能接着 Range，否则会把两段码流拼在一起。
		if startBytes > 0 && (meta == nil || meta.URI != uri) {
			_ = os.Remove(currentTarget)
			_ = removeResumeMetadata(metaPath)
			startBytes = 0
			meta = nil
		}

		// WithoutCancel：本次 GET 不跟外层 Context 一起取消。第一次 Ctrl+C 会等当前文件尽量下完；
		// 重试前的 waitRetry 仍看外层 ctx，所以取消后不会再开下一轮。
		req := c.resty.R().
			SetContext(context.WithoutCancel(ctx)).
			SetHeaders(headers).
			SetDoNotParseResponse(true)

		if startBytes > 0 {
			// 从本地已有长度接着下。sidecar 里有 ETag/Last-Modified 时带 If-Range：远端没变回 206，变了则整段 200 重下。
			req.SetHeader("Range", fmt.Sprintf("bytes=%d-", startBytes))
			if meta != nil && meta.URI == uri {
				if meta.ETag != "" {
					req.SetHeader("If-Range", meta.ETag)
				} else if meta.LastModified != "" {
					req.SetHeader("If-Range", meta.LastModified)
				}
			}
		} else if val, ok := headers["Range"]; ok {
			req.SetHeader("Range", val)
			req.Header.Del("If-Range")
		} else {
			req.Header.Del("Range")
			req.Header.Del("If-Range")
		}

		resp, reqErr := req.Get(uri)
		if reqErr != nil {
			err = reqErr
			if attempt < maxAttempts-1 {
				if waitErr := c.waitRetry(ctx, p, attempt, currentName, false); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, err
		}

		rawBody := resp.RawBody()
		statusCode := resp.StatusCode()
		// 416：本地半成品比远端还长或偏移无效，删掉重来，并额外给一次机会。
		if statusCode == http.StatusRequestedRangeNotSatisfiable && startBytes > 0 {
			rawBody.Close()
			_ = os.Remove(currentTarget)
			_ = removeResumeMetadata(metaPath)
			maxAttempts++
			continue
		}

		if statusCode != http.StatusOK && statusCode != http.StatusPartialContent {
			rawBody.Close()
			err = &StatusError{Code: statusCode, Status: resp.Status()}
			// 调用方若把该状态码标成 fatal（视频 403/404），立刻返回给上层换源，不要在这里退避。
			if dopts.fatalStatuses[statusCode] {
				return nil, err
			}
			if attempt < maxAttempts-1 {
				// 未标 fatal 的 403/429/503 当作风控，退避更长；其它错误只等 1 秒。
				isRiskControl := statusCode == http.StatusForbidden || statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable
				if waitErr := c.waitRetry(ctx, p, attempt, currentName, isRiskControl); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, err
		}

		contentType := resp.Header().Get("Content-Type")
		newExt := detectFileExt(contentType)
		// 响应 Content-Type 和当前后缀不一致时改后续路径。只有本地已有半成品时才真正改磁盘文件名，并连 sidecar 一起改。
		if newExt != "" && !strings.EqualFold(filepath.Ext(currentTarget), newExt) {
			oldTarget := currentTarget
			newTarget := strings.TrimSuffix(currentTarget, filepath.Ext(currentTarget)) + newExt
			if startBytes > 0 {
				_ = os.Rename(oldTarget, newTarget)
				_ = renameResumeMetadata(resumeMetadataPath(oldTarget), resumeMetadataPath(newTarget))
			}
			currentTarget = newTarget
			currentName = strings.TrimSuffix(currentName, filepath.Ext(currentName)) + newExt

			if fi, statErr := os.Stat(currentTarget); statErr == nil {
				startBytes = fi.Size()
			} else {
				startBytes = 0
			}
			metaPath = resumeMetadataPath(currentTarget)
		}

		// 206 必须从本地已有长度接着写；起始偏移对不上就拒绝拼接。
		if statusCode == http.StatusPartialContent && startBytes > 0 {
			rangeStart, parseErr := parseContentRangeStart(resp.Header().Get("Content-Range"))
			if parseErr != nil {
				rawBody.Close()
				return nil, fmt.Errorf("invalid Content-Range for resume: %w", parseErr)
			}
			if rangeStart != startBytes {
				rawBody.Close()
				return nil, fmt.Errorf("resume offset mismatch: local=%d, remote=%d", startBytes, rangeStart)
			}
		}

		meta = &resumeMetadata{
			URI:          uri,
			ETag:         resp.Header().Get("ETag"),
			LastModified: resp.Header().Get("Last-Modified"),
			UpdatedAt:    time.Now(),
		}
		_ = saveResumeMetadata(metaPath, meta)

		var out *os.File
		if statusCode == http.StatusPartialContent {
			out, err = os.OpenFile(currentTarget, os.O_WRONLY|os.O_CREATE, 0644)
			if err == nil {
				_, err = out.Seek(startBytes, io.SeekStart)
			}
		} else {
			// 200 整段返回，覆盖写本地，避免半成品后面再拼一截。
			out, err = os.Create(currentTarget)
			startBytes = 0
		}
		if err != nil {
			rawBody.Close()
			return nil, err
		}

		var (
			bar       *mpb.Bar
			reader    io.Reader = rawBody
			copyErr   error
			lastSpeed string
		)

		if p != nil {
			contentLength := resp.RawResponse.ContentLength
			if contentLength > 0 {
				contentLength += startBytes // 进度条总量 = 已有本地字节 + 本次还要下的长度
			}

			startTime := time.Now()
			bar = p.AddBar(contentLength,
				mpb.BarRemoveOnComplete(),
				mpb.PrependDecorators(
					decor.Name(fmt.Sprintf("原文件: %s -> 保存为: %s", originalName, currentName), decor.WC{W: 55, C: decor.DindentRight}),
					decor.Name(progressSourceTag(dopts.barLabel, "")),
					decor.OnComplete(decor.Name("进度: "), "进度: "),
					decor.CountersKibiByte("% .2f / % .2f"),
				),
				mpb.AppendDecorators(
					decor.Name(" | 剩余: "),
					decor.Any(func(st decor.Statistics) string {
						if st.Completed || st.Total <= 0 {
							return "0s"
						}
						elapsed := time.Since(startTime)
						if elapsed < 100*time.Millisecond {
							return "0s"
						}
						currentDownloaded := st.Current - startBytes
						if currentDownloaded <= 0 {
							return "0s"
						}
						speed := float64(currentDownloaded) / elapsed.Seconds()
						if speed == 0 {
							return "0s"
						}
						remaining := st.Total - st.Current
						if remaining < 0 {
							remaining = 0
						}
						eta := time.Duration(float64(remaining)/speed) * time.Second
						return eta.Round(time.Second).String()
					}, decor.WC{W: 5, C: decor.DindentRight}),
					decor.Name(" | 速度: "),
					decor.Any(func(st decor.Statistics) string {
						if st.Completed && lastSpeed != "" {
							return lastSpeed
						}
						elapsed := time.Since(startTime)
						if elapsed < 100*time.Millisecond {
							return "0 B/s"
						}
						currentDownloaded := st.Current - startBytes
						if currentDownloaded < 0 {
							currentDownloaded = 0
						}
						speed := float64(currentDownloaded) / elapsed.Seconds()
						lastSpeed = util.FormatBytes(int64(speed)) + "/s"
						return lastSpeed
					}, decor.WC{W: 15, C: decor.DindentRight}),
				),
			)
			if startBytes > 0 {
				bar.SetCurrent(startBytes)
			}
			reader = bar.ProxyReader(rawBody)
		}

		if onProgress != nil {
			reader = &progressReader{
				reader:     reader,
				onProgress: onProgress,
			}
		}

		_, copyErr = io.Copy(out, reader)
		closeErr := out.Close()
		rawBody.Close()

		if bar != nil {
			if copyErr != nil || closeErr != nil {
				bar.Abort(true)
			} else {
				bar.SetTotal(-1, true)
			}
		}

		if copyErr != nil {
			err = fmt.Errorf("connection broken during download: %w", copyErr)
			if attempt < maxAttempts-1 {
				if waitErr := c.waitRetry(ctx, p, attempt, currentName, false); waitErr != nil {
					return nil, waitErr
				}
				continue
			}
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}

		// 下完后核对本地大小是否达到「续传起点 + Content-Length」。
		if resp.RawResponse.ContentLength > 0 {
			fi, statErr := os.Stat(currentTarget)
			if statErr == nil {
				expectedSize := resp.RawResponse.ContentLength + startBytes
				if fi.Size() < expectedSize {
					err = fmt.Errorf("file integrity check failed: expected %d bytes, got %d bytes", expectedSize, fi.Size())
					if attempt < maxAttempts-1 {
						if waitErr := c.waitRetry(ctx, p, attempt, currentName, false); waitErr != nil {
							return nil, waitErr
						}
						continue
					}
					return nil, err
				}
			}
		}

		// 完整落盘后删掉 sidecar，下次不会误续传。
		_ = removeResumeMetadata(metaPath)
		return map[string]interface{}{
			"filename": filepath.Base(currentTarget),
			"dir":      targetDir,
			"path":     currentTarget,
		}, nil
	}

	return nil, fmt.Errorf("download failed after %d attempts", maxAttempts)
}

// waitRetry 在重试前等待。普通失败等 1 秒；风控按 3、6、12… 秒指数退避。有进度条时会画出等待进度。
func (c *Client) waitRetry(ctx context.Context, p *mpb.Progress, attempt int, name string, isRiskControl bool) error {
	sleepSec := 1
	if isRiskControl {
		sleepSec = (1 << attempt) * 3
	}

	if p != nil {
		yellow := color.New(color.FgYellow).SprintFunc()
		msg := yellow(fmt.Sprintf("[重试等待 %ds...] %s", sleepSec, name))
		tempBar := p.AddBar(int64(sleepSec),
			mpb.BarRemoveOnComplete(),
			mpb.PrependDecorators(
				decor.Name(msg, decor.WC{W: 55, C: decor.DindentRight}),
			),
			mpb.AppendDecorators(decor.CountersNoUnit("%d / %d s")),
		)
		for s := 0; s < sleepSec; s++ {
			select {
			case <-ctx.Done():
				tempBar.Abort(true)
				return ctx.Err()
			case <-time.After(time.Second):
				tempBar.Increment()
			}
		}
		tempBar.SetTotal(-1, true)
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(sleepSec) * time.Second):
		return nil
	}
}

// detectFileExt 根据响应头推断目标文件后缀。
func detectFileExt(contentType string) string {
	switch {
	case strings.Contains(contentType, "image/jpeg"):
		return ".jpg"
	case strings.Contains(contentType, "image/png"):
		return ".png"
	case strings.Contains(contentType, "image/gif"):
		return ".gif"
	case strings.Contains(contentType, "image/webp"):
		return ".webp"
	case strings.Contains(contentType, "image/heic"):
		return ".heic"
	case strings.Contains(contentType, "image/bmp"):
		return ".bmp"
	case strings.Contains(contentType, "video/mp4"):
		return ".mp4"
	default:
		return ""
	}
}

// parseContentRangeStart 解析形如 "bytes 1024-2047/4096" 的响应头，返回起始偏移。
func parseContentRangeStart(contentRange string) (int64, error) {
	if contentRange == "" {
		return 0, fmt.Errorf("missing Content-Range header")
	}

	parts := strings.SplitN(contentRange, " ", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("malformed Content-Range: %s", contentRange)
	}

	rangePart := strings.SplitN(parts[1], "/", 2)[0]
	bounds := strings.SplitN(rangePart, "-", 2)
	if len(bounds) != 2 {
		return 0, fmt.Errorf("malformed Content-Range bounds: %s", contentRange)
	}

	start, err := strconv.ParseInt(bounds[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid Content-Range start %q: %w", bounds[0], err)
	}
	return start, nil
}

// resumeMetadataPath 返回目标文件旁 sidecar 的路径，即 target + ".resume.json"。
func resumeMetadataPath(target string) string {
	return target + ".resume.json"
}

// loadResumeMetadata 读取 sidecar。文件不存在或 JSON 损坏时返回 error，调用方按「不能续传」处理。
func loadResumeMetadata(path string) (*resumeMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var meta resumeMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// saveResumeMetadata 在开始拷贝响应体之前，把当前 URL / ETag / Last-Modified 写入 sidecar。
func saveResumeMetadata(path string, meta *resumeMetadata) error {
	if meta == nil {
		return nil
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// removeResumeMetadata 删除 sidecar。下载成功、换源作废半成品、以及 416 清文件时都会调用；文件本来就不存在视为成功。
func removeResumeMetadata(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// renameResumeMetadata 目标文件因 Content-Type 改后缀时，把 sidecar 一起改名。旧文件不存在则直接返回。
func renameResumeMetadata(oldPath string, newPath string) error {
	if _, err := os.Stat(oldPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Rename(oldPath, newPath)
}
