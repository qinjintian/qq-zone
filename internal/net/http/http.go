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
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cheggaaa/pb/v3"
	"github.com/go-resty/resty/v2"
	"github.com/qinjintian/qq-zone/internal/pkg/util"
)

type Client struct {
	resty *resty.Client
}

func NewClient() *Client {
	return &Client{
		resty: resty.New().
			SetTimeout(60 * time.Second).
			SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true}).
			SetRetryCount(3).
			SetRetryWaitTime(2 * time.Second).
			SetCookieJar(nil), // 禁用自动 Cookie 管理，防止多账号或好友权限查询时的 Cookie 污染
	}
}

func (c *Client) Get(url string, headers map[string]string) (http.Header, []byte, int, error) {
	resp, err := c.resty.R().
		SetHeaders(headers).
		Get(url)

	if err != nil {
		return nil, nil, 0, err
	}
	return resp.Header(), resp.Body(), resp.StatusCode(), nil
}

func (c *Client) Head(url string, headers map[string]string) (http.Header, error) {
	resp, err := c.resty.R().
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

func (c *Client) PostForm(url string, params map[string]string, headers map[string]string) ([]byte, error) {
	resp, err := c.resty.R().
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

func (c *Client) Download(uri string, target string, headers map[string]string, retry int, timeout int, showProgress bool) (map[string]interface{}, error) {
	targetDir := filepath.Dir(target)
	if !util.IsDir(targetDir) {
		_ = os.MkdirAll(targetDir, os.ModePerm)
	}

	req := c.resty.R().
		SetHeaders(headers).
		SetDoNotParseResponse(true)

	resp, err := req.Get(uri)
	if err != nil {
		return nil, err
	}
	defer resp.RawBody().Close()

	if resp.IsError() {
		return nil, fmt.Errorf("download failed with status: %s", resp.Status())
	}

	out, err := os.Create(target)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	var reader io.Reader = resp.RawBody()
	if showProgress {
		bar := pb.Full.Start64(resp.RawResponse.ContentLength)
		reader = bar.NewProxyReader(resp.RawBody())
		defer bar.Finish()
	}

	_, err = io.Copy(out, reader)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"filename": filepath.Base(target),
		"dir":      targetDir,
		"path":     target,
	}, nil
}
