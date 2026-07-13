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
 * @FileName: http.go
 * @Description: [定制化 HTTP 客户端封装，支持带进度的文件下载及通用的 GET/POST 请求]
 */

package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/go-resty/resty/v2"
	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/time/rate"
)

// Client 封装了基于 go-resty 的 HTTP 客户端
// 包含连接池、超时控制以及全局并发限流器
type Client struct {
	resty   *resty.Client
	limiter *rate.Limiter
}

// NewClient 初始化一个全局 HTTP 客户端
// 配置了连接超时、KeepAlive 以及重试机制
func NewClient() *Client {
	return &Client{
		resty: resty.New().
			SetTimeout(60 * time.Second).
			SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true}).
			SetRetryCount(3).
			SetRetryWaitTime(2 * time.Second).
			SetCookieJar(nil), // 禁用自动 Cookie 管理，防止多账号或好友权限查询时的 Cookie 污染
		limiter: rate.NewLimiter(rate.Every(500*time.Millisecond), 1), // 默认每 500ms 一个请求
	}
}

// SetRateLimit 动态调整请求频率限制
func (c *Client) SetRateLimit(r rate.Limit, b int) {
	c.limiter.SetLimit(r)
	c.limiter.SetBurst(b)
}

// Get 发起一个基础的 HTTP GET 请求，受全局速率限制保护
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

// Head 发起一个 HTTP HEAD 请求，常用于获取文件大小 (Content-Length) 而不下载实体
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

// PostForm 发起一个 HTTP POST 表单请求，用于提交数据
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

// Download 执行大文件流式下载任务
// 核心功能：断点续传 (HTTP Range)、文件后缀嗅探、指数退避风控防御、进度条渲染
func (c *Client) Download(ctx context.Context, uri string, target string, headers map[string]string, retry int, timeout int, p *mpb.Progress, name string, originalName string) (res map[string]interface{}, err error) {
	// 下载大文件时不建议加全局 QPS 限制，否则会拖慢下载速度
	// 但如果是获取下载链接等小请求，可以考虑。这里暂时不给 Download 加 Wait
	targetDir := filepath.Dir(target)
	if !util.IsDir(targetDir) {
		if err := os.MkdirAll(targetDir, os.ModePerm); err != nil {
			return nil, fmt.Errorf("failed to create target directory: %w", err)
		}
	}

	// 检查本地文件是否已存在，支持断点续传
	var startBytes int64 = 0
	if fi, err := os.Stat(target); err == nil {
		startBytes = fi.Size()
	}

	req := c.resty.R().
		SetContext(context.WithoutCancel(ctx)).
		SetHeaders(headers).
		SetDoNotParseResponse(true)

	var resp *resty.Response
	for i := 0; i <= retry; i++ {
		if startBytes > 0 {
			req.SetHeader("Range", fmt.Sprintf("bytes=%d-", startBytes))
		} else {
			req.Header.Del("Range")
		}

		var reqErr error
		resp, reqErr = req.Get(uri)
		if reqErr != nil {
			err = reqErr
			continue
		}

		if resp.StatusCode() == 200 || resp.StatusCode() == 206 {
			err = nil
			break
		}

		status := resp.StatusCode()
		resp.RawBody().Close()
		err = fmt.Errorf("download failed with status: %s", resp.Status())

		// 如果检测到 403/429 等可能是风控的错误，进行退避
		if status == 403 || status == 429 || status == 503 {
			if i < retry {
				// 指数退避，基础等待 3 秒 (3s, 6s, 12s...)
				sleepSec := (1 << i) * 3

				if p != nil {
					yellow := color.New(color.FgYellow).SprintFunc()
					msg := yellow(fmt.Sprintf("[风控拦截, 暂停 %ds...] %s", sleepSec, name))
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
							return nil, ctx.Err()
						case <-time.After(time.Second):
							tempBar.Increment()
						}
					}
					tempBar.SetTotal(-1, true) // 完成并移除
				} else {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(time.Duration(sleepSec) * time.Second):
					}
				}
				continue
			}
		} else {
			// 普通 HTTP 错误也重试，但不加长退避
			if i < retry {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(1 * time.Second):
				}
			}
		}
	}

	if err != nil {
		return nil, err
	}
	defer resp.RawBody().Close()

	// 动态判断并修正文件扩展名 (通过 Content-Type)
	contentType := resp.Header().Get("Content-Type")
	var newExt string
	switch {
	case strings.Contains(contentType, "image/jpeg"):
		newExt = ".jpg"
	case strings.Contains(contentType, "image/png"):
		newExt = ".png"
	case strings.Contains(contentType, "image/gif"):
		newExt = ".gif"
	case strings.Contains(contentType, "image/webp"):
		newExt = ".webp"
	case strings.Contains(contentType, "image/heic"):
		newExt = ".heic"
	case strings.Contains(contentType, "image/bmp"):
		newExt = ".bmp"
	case strings.Contains(contentType, "video/mp4"):
		newExt = ".mp4"
	}

	if newExt != "" && !strings.EqualFold(filepath.Ext(target), newExt) {
		newTarget := strings.TrimSuffix(target, filepath.Ext(target)) + newExt
		if startBytes > 0 {
			// 本地存在部分下载的文件，重命名以匹配新的后缀
			_ = os.Rename(target, newTarget)
		}
		target = newTarget
		name = strings.TrimSuffix(name, filepath.Ext(name)) + newExt
	}

	// 如果服务器支持 Range 或者返回 200，说明需要重新开始
	var out *os.File
	if resp.StatusCode() == 206 {
		// 使用 O_CREATE 以防万一，尽管通常 206 意味着文件已存在
		out, err = os.OpenFile(target, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	} else {
		out, err = os.Create(target)
		startBytes = 0
	}

	if err != nil {
		return nil, err
	}
	defer out.Close()

	var reader io.Reader = resp.RawBody()
	if p != nil {
		contentLength := resp.RawResponse.ContentLength
		if contentLength > 0 {
			contentLength += startBytes
		}

		startTime := time.Now()
		var lastSpeed string

		bar := p.AddBar(contentLength,
			mpb.BarRemoveOnComplete(), // 完成后自动彻底移除子进度条
			mpb.PrependDecorators(
				// 例如：原文件: 2026-07-08 -> 保存为: IMG_20260707.jpg
				decor.Name(fmt.Sprintf("原文件: %s -> 保存为: %s", originalName, name), decor.WC{W: 55, C: decor.DindentRight}),
				decor.Name("  "), // 增加显式空格，防止粘连
				// 添加说明: 已下载/总大小
				decor.OnComplete(decor.Name("进度: "), "进度: "),
				decor.CountersKibiByte("% .2f / % .2f"),
			),
			mpb.AppendDecorators(
				decor.Name(" | 剩余: "),
				decor.Any(func(st decor.Statistics) string {
					if st.Completed {
						return "0s"
					}
					if st.Total <= 0 {
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
					// 仅计算本次下载的字节数以获得实时速度
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
		reader = bar.ProxyReader(resp.RawBody())
		defer func() {
			if err != nil {
				bar.Abort(true)
			} else {
				bar.SetTotal(-1, true)
			}
		}()
	}

	_, err = io.Copy(out, reader)
	if err != nil {
		return nil, err
	}

	// 校验文件完整性 (基于 Content-Length)
	if resp.RawResponse.ContentLength > 0 {
		fi, err := os.Stat(target)
		if err == nil {
			expectedSize := resp.RawResponse.ContentLength + startBytes
			if fi.Size() < expectedSize {
				return nil, fmt.Errorf("file integrity check failed: expected %d bytes, got %d bytes", expectedSize, fi.Size())
			}
		}
	}

	return map[string]interface{}{
		"filename": filepath.Base(target),
		"dir":      targetDir,
		"path":     target,
	}, nil
}
