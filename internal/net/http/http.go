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
 * @LastEditTime: 2026-07-14 16:30:00
 * @FileName: http.go
 * @Description: [定制化 HTTP 客户端封装，支持带进度的大文件下载、安全续传与通用 GET/POST 请求]
 */

package http

import (
	"context"
	"crypto/tls"
	"encoding/json"
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
	resty   *resty.Client
	limiter *rate.Limiter
}

// resumeMetadata 用于记录断点续传所需的远端资源校验信息。
type resumeMetadata struct {
	URI          string    `json:"uri"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type progressReader struct {
	reader     io.Reader
	onProgress func(int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 && r.onProgress != nil {
		r.onProgress(int64(n))
	}
	return n, err
}

// NewClient 初始化一个全局 HTTP 客户端。
func NewClient() *Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
		TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		ForceAttemptHTTP2:     false,
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
		limiter: rate.NewLimiter(rate.Every(500*time.Millisecond), 1),
	}
}

// SetRateLimit 动态调整请求频率限制。
func (c *Client) SetRateLimit(r rate.Limit, b int) {
	c.limiter.SetLimit(r)
	c.limiter.SetBurst(b)
}

// Get 发起一个基础的 HTTP GET 请求，受全局速率限制保护。
func (c *Client) Get(ctx context.Context, url string, headers map[string]string) (http.Header, []byte, int, error) {
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

// Download 执行大文件流式下载任务。
// onProgress 会在每次成功读取到响应体字节后回调，用于上层吞吐量统计。
func (c *Client) Download(ctx context.Context, uri string, target string, headers map[string]string, retry int, timeout int, p *mpb.Progress, name string, originalName string, onProgress func(int64)) (res map[string]interface{}, err error) {
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

		req := c.resty.R().
			SetContext(context.WithoutCancel(ctx)).
			SetHeaders(headers).
			SetDoNotParseResponse(true)

		if startBytes > 0 {
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
		if statusCode == http.StatusRequestedRangeNotSatisfiable && startBytes > 0 {
			rawBody.Close()
			_ = os.Remove(currentTarget)
			_ = removeResumeMetadata(metaPath)
			maxAttempts++
			continue
		}

		if statusCode != http.StatusOK && statusCode != http.StatusPartialContent {
			rawBody.Close()
			err = fmt.Errorf("download failed with status: %s", resp.Status())
			if attempt < maxAttempts-1 {
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
				contentLength += startBytes
			}

			startTime := time.Now()
			bar = p.AddBar(contentLength,
				mpb.BarRemoveOnComplete(),
				mpb.PrependDecorators(
					decor.Name(fmt.Sprintf("原文件: %s -> 保存为: %s", originalName, currentName), decor.WC{W: 55, C: decor.DindentRight}),
					decor.Name("  "),
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

		_ = removeResumeMetadata(metaPath)
		return map[string]interface{}{
			"filename": filepath.Base(currentTarget),
			"dir":      targetDir,
			"path":     currentTarget,
		}, nil
	}

	return nil, fmt.Errorf("download failed after %d attempts", maxAttempts)
}

// waitRetry 负责在下载失败后按不同场景进行短暂退避。
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

func resumeMetadataPath(target string) string {
	return target + ".resume.json"
}

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

func removeResumeMetadata(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func renameResumeMetadata(oldPath string, newPath string) error {
	if _, err := os.Stat(oldPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Rename(oldPath, newPath)
}
