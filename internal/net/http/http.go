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
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/time/rate"
)

type Client struct {
	resty   *resty.Client
	limiter *rate.Limiter
}

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

func (c *Client) Download(ctx context.Context, uri string, target string, headers map[string]string, retry int, timeout int, p *mpb.Progress, name string, originalName string) (map[string]interface{}, error) {
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
		SetContext(ctx).
		SetHeaders(headers).
		SetDoNotParseResponse(true)

	if startBytes > 0 {
		req.SetHeader("Range", fmt.Sprintf("bytes=%d-", startBytes))
	}

	resp, err := req.Get(uri)
	if err != nil {
		return nil, err
	}
	defer resp.RawBody().Close()

	if resp.StatusCode() != 200 && resp.StatusCode() != 206 {
		return nil, fmt.Errorf("download failed with status: %s", resp.Status())
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
		bar := p.AddBar(contentLength,
			mpb.BarFillerClearOnComplete(), // 完成后自动移除子进度条
			mpb.PrependDecorators(
				decor.Name(fmt.Sprintf("%s -> %s", originalName, name), decor.WC{W: 40, C: decor.DindentRight}),
				decor.Name("  "), // 增加显式空格，防止粘连
				decor.CountersKibiByte("% .2f / % .2f"),
			),
			mpb.AppendDecorators(
				decor.EwmaETA(decor.ET_STYLE_GO, 90),
				decor.Name(" ] "),
				decor.Any(func(st decor.Statistics) string {
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
					return util.FormatBytes(int64(speed)) + "/s"
				}, decor.WC{W: 15, C: decor.DindentRight}),
			),
		)
		if startBytes > 0 {
			bar.SetCurrent(startBytes)
		}
		reader = bar.ProxyReader(resp.RawBody())
		defer bar.SetTotal(-1, true) // 确保结束
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
