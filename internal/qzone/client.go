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
 * @FileName: client.go
 * @Description: [QQ 空间核心 API 客户端，封装相册、照片及视频下载地址的获取逻辑]
 */

package qzone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qinjintian/qq-zone/internal/net/http"
	"github.com/qinjintian/qq-zone/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Client 封装了与 QQ 空间 API 交互的客户端
// 包含 HTTP 客户端实例、用户凭证 (Cookie/GTK) 以及日志记录器
type Client struct {
	QQ        string             // 当前登录用户的 QQ 号码
	Nickname  string             // 当前登录用户的昵称
	GTK       string             // 算出的 QQ 空间 CSRF 校验 Token (g_tk)
	Cookie    string             // 用户登录状态的 Cookie 字符串
	Http      *http.Client       // 底层 HTTP 客户端，负责发送网络请求
	APILogger *zap.SugaredLogger // 专门用于记录 API 请求和响应的日志记录器
}

// NewClient 初始化并返回一个 QQ 空间 API 客户端实例 (优先尝试 Session，无效则扫码登录)
func NewClient(ctx context.Context, httpClient *http.Client, logFact *logger.Factory) (*Client, error) {
	// 1. 尝试从本地加载最近使用的 Session
	if sess, err := GetLastSession(); err == nil && sess != nil {
		c, err := NewClientWithSession(ctx, sess, httpClient, logFact)
		if err == nil {
			return c, nil
		}
	}
	// 2. 无效或不存在，执行新登录流程
	return NewClientWithQR(ctx, httpClient, logFact)
}

// NewClientWithSession 从已有的 Session 实例化一个客户端实例
// 校验 Session 是否有效，若无效则清理掉该账号的 Session
func NewClientWithSession(ctx context.Context, sess *Session, httpClient *http.Client, logFact *logger.Factory) (*Client, error) {
	apiLogger, _ := logFact.CreateAPILogger(sess.QQ)
	c := &Client{
		QQ:        sess.QQ,
		Nickname:  sess.Nickname,
		GTK:       sess.GTK,
		Cookie:    sess.Cookie,
		Http:      httpClient,
		APILogger: apiLogger,
	}
	// 校验 Session 是否有效
	if _, err := c.GetAlbumList(ctx, sess.QQ); err != nil {
		_ = RemoveSession(sess.QQ) // Session 失效，清理掉该账号
		return nil, err
	}
	// 有效则更新最后使用时间
	_ = SaveSession(sess)
	return c, nil
}

// NewClientWithQR 发起二维码登录流程
// 它会获取登录二维码，保存到本地，轮询登录状态，最后解析凭证并初始化客户端信息
func NewClientWithQR(ctx context.Context, httpClient *http.Client, logFact *logger.Factory) (*Client, error) {
	loginRes, err := NewLoginHandler(httpClient).Login(ctx)
	if err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}

	cookie := loginRes["cookie"]
	qq := extractCookieValue(cookie, "uin")
	qq = strings.TrimPrefix(qq, "o")
	qq = strings.TrimLeft(qq, "0")

	apiLogger, _ := logFact.CreateAPILogger(qq)

	c := &Client{
		QQ:        qq,
		Nickname:  loginRes["nickname"],
		GTK:       loginRes["g_tk"],
		Cookie:    cookie,
		Http:      httpClient,
		APILogger: apiLogger,
	}

	// 登录成功，保存 Session
	_ = SaveSession(&Session{
		QQ:       c.QQ,
		Nickname: c.Nickname,
		GTK:      c.GTK,
		Cookie:   c.Cookie,
	})

	return c, nil
}

// NewClientFromSession 通过已有的 Session 会话直接恢复客户端状态（不进行有效性校验）
func NewClientFromSession(sess *Session, httpClient *http.Client, apiLogger *zap.SugaredLogger) *Client {
	return &Client{
		QQ:        sess.QQ,
		Nickname:  sess.Nickname,
		GTK:       sess.GTK,
		Cookie:    sess.Cookie,
		Http:      httpClient,
		APILogger: apiLogger,
	}
}

// logAPI 负责统一记录底层 API 请求的调试信息
// 包含请求的 URL、状态码以及原始的响应内容，方便在出现风控或错误时进行排查
func (c *Client) logAPI(apiName, url string, headers map[string]string, body string, statusCode int, duration time.Duration, err error) {
	if c.APILogger == nil {
		return
	}

	status := "SUCCESS"
	if err != nil || (statusCode != 0 && statusCode >= 400) {
		status = "FAILED"
	}

	logMsg := fmt.Sprintf("\n%s\n", strings.Repeat("=", 60))
	logMsg += fmt.Sprintf(">>> API [%s] %s <<<\n", apiName, status)
	logMsg += fmt.Sprintf("Time:     %s\n", time.Now().Format("2006-01-02 15:04:05.000"))
	logMsg += fmt.Sprintf("Duration: %v\n", duration)
	logMsg += fmt.Sprintf("Status:   %d\n", statusCode)
	logMsg += fmt.Sprintf("URL:      %s\n", url)

	logMsg += "\n[Request Headers]\n"
	for k, v := range headers {
		logMsg += fmt.Sprintf("  %s: %s\n", k, v)
	}

	if err != nil {
		logMsg += fmt.Sprintf("\n[Error]\n  %v\n", err)
	}

	if body != "" {
		logMsg += "\n[Response Body]\n"
		// 尝试格式化 JSON 以便阅读
		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, []byte(body), "", "  "); err == nil {
			logMsg += prettyJSON.String()
		} else {
			// 如果不是标准 JSON（如 JSONP），则直接输出
			logMsg += body
		}
	}
	logMsg += "\n" + strings.Repeat("=", 60) + "\n"

	if err != nil || (statusCode != 0 && statusCode >= 400) {
		c.APILogger.Errorf(logMsg)
	} else {
		c.APILogger.Debug(logMsg)
	}
}

// GetAlbumList 拉取指定 QQ 号的相册列表
// 返回一个包含所有相册信息的 gjson.Result 数组
func (c *Client) GetAlbumList(ctx context.Context, targetUin string) ([]gjson.Result, error) {
	headers := map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", c.QQ),
		"origin":     "https://user.qzone.qq.com",
	}

	var (
		offset    int64 = 0
		limit     int64 = 30
		allAlbums []gjson.Result
	)

	for {
		apiURL := fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/fcg_list_album_v3?g_tk=%v&callback=shine_Callback&hostUin=%v&uin=%v&appid=4&inCharset=utf-8&outCharset=utf-8&source=qzone&plat=qzone&format=jsonp&notice=0&filter=1&handset=4&pageNumModeSort=40&pageNumModeClass=15&needUserInfo=1&idcNum=4&mode=2&pageStart=%d&pageNum=%d&callbackFun=shine", c.GTK, targetUin, c.QQ, offset, limit)

		start := time.Now()
		_, body, code, err := c.Http.Get(ctx, apiURL, headers)
		duration := time.Since(start)

		bodyStr := string(body)
		c.logAPI("GetAlbumList", apiURL, headers, bodyStr, code, duration, err)

		if err != nil {
			return nil, fmt.Errorf("failed to fetch album page %d: %w", offset/limit+1, err)
		}
		if code != 200 {
			return nil, fmt.Errorf("failed to fetch album page %d: status code %d", offset/limit+1, code)
		}

		data, err := parseJSONP(bodyStr, "shine_Callback")
		if err != nil {
			return nil, fmt.Errorf("failed to parse album response: %w (raw body: %s)", err, bodyStr)
		}

		res := gjson.Parse(data)
		if code := res.Get("code").Int(); code != 0 {
			msg := res.Get("message").String()
			if msg == "" {
				msg = res.Get("msg").String()
			}
			return nil, fmt.Errorf("api error (code: %d): %s", code, msg)
		}

		albumList := res.Get("data.albumList").Array()
		// 尝试兼容不同的 JSON 路径
		if len(albumList) == 0 && offset == 0 {
			if altList := res.Get("albumList").Array(); len(altList) > 0 {
				albumList = altList
			}
		}

		allAlbums = append(allAlbums, albumList...)

		nextPageStart := res.Get("data.nextPageStart").Int()
		totalAlbums := res.Get("data.albumsInUser").Int()

		if nextPageStart >= totalAlbums || len(albumList) == 0 {
			break
		}
		offset = nextPageStart
	}

	return allAlbums, nil
}

// GetPhotoList 分页拉取指定相册下的所有照片/视频列表
// 由于 QQ 空间接口有分页限制，此方法会自动循环请求直到拉取完所有数据
func (c *Client) GetPhotoList(ctx context.Context, targetUin string, albumID string) ([]gjson.Result, error) {
	headers := map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/4", targetUin),
		"origin":     "https://user.qzone.qq.com",
	}

	var (
		offset     int64 = 0
		limit      int64 = 500
		allPhotos  []gjson.Result
		photoCount int64 = 0
	)

	for {
		apiURL := fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/cgi_list_photo?g_tk=%v&callback=shine_Callback&mode=0&idcNum=4&hostUin=%v&topicId=%v&noTopic=0&uin=%v&pageStart=%v&pageNum=%v&skipCmtCount=0&singleurl=1&batchId=&notice=0&appid=4&inCharset=utf-8&outCharset=utf-8&source=qzone&plat=qzone&outstyle=json&format=jsonp&json_esc=1&callbackFun=shine", c.GTK, targetUin, albumID, c.QQ, offset, limit)

		start := time.Now()
		header, body, code, err := c.Http.Get(ctx, apiURL, headers)
		duration := time.Since(start)

		bodyStr := string(body)
		c.logAPI("GetPhotoList", apiURL, headers, bodyStr, code, duration, err)

		if err != nil {
			return nil, fmt.Errorf("failed to fetch photo page: %w", err)
		}
		if code != 200 {
			return nil, fmt.Errorf("failed to fetch photo page: status code %d", code)
		}

		for _, cookie := range header.Values("Set-Cookie") {
			if key := extractCookieValue(cookie, "qq_photo_key"); key != "" {
				if !strings.Contains(c.Cookie, "qq_photo_key") {
					c.Cookie += "; qq_photo_key=" + key
					headers["cookie"] = c.Cookie
				}
				break
			}
		}

		data, err := parseJSONP(bodyStr, "shine_Callback")
		if err != nil {
			return nil, fmt.Errorf("failed to parse photo response: %w (raw body: %s)", err, bodyStr)
		}

		res := gjson.Parse(data)
		if res.Get("code").Int() != 0 {
			return nil, fmt.Errorf("api error: %s", res.Get("message").String())
		}

		list := res.Get("data.photoList").Array()
		allPhotos = append(allPhotos, list...)
		photoCount += int64(len(list))

		totalInAlbum := res.Get("data.totalInAlbum").Int()
		if totalInAlbum == 0 {
			totalInAlbum = res.Get("data.total").Int()
		}

		if photoCount >= totalInAlbum || len(list) == 0 {
			break
		}
		offset += limit
	}

	return allPhotos, nil
}

// GetVideoDownloadURL 获取指定视频或实况图的真实下载直链
// 对于 MP4 视频或实况图，QQ 空间需要通过该专用接口获取带有 token 的真实下载地址
func (c *Client) GetVideoDownloadURL(ctx context.Context, targetUin string, albumID string, sloc string) (string, error) {
	headers := map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", targetUin),
	}

	apiURL := fmt.Sprintf("https://h5.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/cgi_floatview_photo_list_v2?g_tk=%v&callback=viewer_Callback&topicId=%v&picKey=%v&cmtOrder=1&fupdate=1&plat=qzone&source=qzone&cmtNum=0&inCharset=utf-8&outCharset=utf-8&callbackFun=viewer&uin=%v&hostUin=%v&appid=4&isFirst=1", c.GTK, albumID, sloc, c.QQ, targetUin)

	start := time.Now()
	_, body, code, err := c.Http.Get(ctx, apiURL, headers)
	duration := time.Since(start)

	bodyStr := string(body)
	c.logAPI("GetVideoDownloadURL", apiURL, headers, bodyStr, code, duration, err)

	if err != nil {
		return "", err
	}
	if code != 200 {
		return "", fmt.Errorf("failed to fetch video download url: status code %d", code)
	}

	data, err := parseJSONP(bodyStr, "viewer_Callback")
	if err != nil {
		return "", fmt.Errorf("failed to parse video response: %w (raw body: %s)", err, bodyStr)
	}

	res := gjson.Parse(data)
	photos := res.Get("data.photos").Array()
	if len(photos) == 0 {
		return "", fmt.Errorf("no video found in response")
	}

	video := photos[res.Get("data.picPosInPage").Int()]
	vInfo := video.Get("video_info")

	// 优先获取 download_url 或 video_url，只要有链接就尝试下载，忽略 status 限制
	downloadURL := vInfo.Get("download_url").String()
	if downloadURL == "" {
		downloadURL = vInfo.Get("video_url").String()
	}

	// 如果仍然为空，且状态不为 2，才报错
	if downloadURL == "" && vInfo.Get("status").Int() != 2 {
		return "", fmt.Errorf("video is not ready and no URL found (status: %d, video_info: %s)", vInfo.Get("status").Int(), vInfo.Raw)
	}

	// 尝试获取高清地址，但保留原始地址作为备份
	finalURL := downloadURL
	if strings.Contains(downloadURL, ".f20.mp4") {
		highResURL := strings.Replace(downloadURL, ".f20.mp4", ".f0.mp4", 1)
		// 检查高清地址是否可用（发送一个 HEAD 请求）
		// 注意：如果 HEAD 请求太慢或被封，可能需要直接在下载逻辑里做重试
		finalURL = highResURL
	}

	return finalURL, nil
}

// GetFriendList 获取当前用户的所有好友列表
// 并并发检测每个好友的空间访问权限 (是否对我开放)
func (c *Client) GetFriendList(ctx context.Context) ([]gjson.Result, error) {
	apiURL := fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/r.qzone.qq.com/cgi-bin/tfriend/friend_ship_manager.cgi?uin=%v&do=1&fupdate=1&clean=1&g_tk=%v", c.QQ, c.GTK)
	headers := map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", c.QQ),
		"origin":     "https://user.qzone.qq.com",
	}

	start := time.Now()
	_, body, code, err := c.Http.Get(ctx, apiURL, headers)
	duration := time.Since(start)

	bodyStr := string(body)
	c.logAPI("GetFriendList", apiURL, headers, bodyStr, code, duration, err)

	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("failed to fetch friend list: status code %d", code)
	}

	data, err := parseJSONP(bodyStr, "shine_Callback")
	if err != nil {
		return nil, fmt.Errorf("failed to parse friend response: %w (raw body: %s)", err, bodyStr)
	}

	res := gjson.Parse(data)
	if res.Get("code").Int() != 0 {
		return nil, fmt.Errorf("api error: %s", res.Get("message").String())
	}

	return res.Get("data.items_list").Array(), nil
}

// Helper functions - 以下为内部辅助函数
// parseJSONP 用于解析腾讯接口常返回的 JSONP 格式数据
// 它会剥离外部的 callback 回调函数包装，提取并返回纯净的内部 JSON 字符串
func parseJSONP(content string, callback string) (string, error) {
	start := strings.Index(content, "(")
	end := strings.LastIndex(content, ")")
	if start == -1 || end == -1 || end <= start {
		return "", fmt.Errorf("invalid JSONP response")
	}
	return content[start+1 : end], nil
}

// GetAlbumListURL 根据给定的参数生成并返回拉取相册列表的 API URL
func GetAlbumListURL(hostUin, uin, gtk string) string {
	return fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/fcg_list_album_v3?g_tk=%v&callback=shine_Callback&hostUin=%v&uin=%v&appid=4&inCharset=utf-8&outCharset=utf-8&source=qzone&plat=qzone&format=jsonp&notice=0&filter=1&handset=4&pageNumModeSort=40&pageNumModeClass=15&needUserInfo=1&idcNum=4&callbackFun=shine", gtk, hostUin, uin)
}
