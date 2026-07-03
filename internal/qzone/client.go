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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qinjintian/qq-zone/internal/net/http"
	"github.com/qinjintian/qq-zone/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Client handles all communication with QQ Zone API
type Client struct {
	QQ        string
	Nickname  string
	GTK       string
	Cookie    string
	Http      *http.Client
	APILogger *zap.SugaredLogger
}

// NewClient creates a new Qzone client after login
func NewClient(httpClient *http.Client, logFact *logger.Factory) (*Client, error) {
	loginRes, err := NewLoginHandler(httpClient).Login()
	if err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}

	cookie := loginRes["cookie"]
	qq := extractCookieValue(cookie, "uin")
	qq = strings.TrimPrefix(qq, "o")
	qq = strings.TrimLeft(qq, "0")

	apiLogger, _ := logFact.CreateAPILogger(qq)

	return &Client{
		QQ:        qq,
		Nickname:  loginRes["nickname"],
		GTK:       loginRes["g_tk"],
		Cookie:    cookie,
		Http:      httpClient,
		APILogger: apiLogger,
	}, nil
}

// logAPI logs the details of an API request and response
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

// GetAlbumList fetches all albums for a target QQ
func (c *Client) GetAlbumList(targetUin string) ([]gjson.Result, error) {
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
		_, body, code, err := c.Http.Get(apiURL, headers)
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

// GetPhotoList fetches all photos in an album
func (c *Client) GetPhotoList(targetUin string, albumID string) ([]gjson.Result, error) {
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
		header, body, code, err := c.Http.Get(apiURL, headers)
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

// GetVideoDownloadURL gets the actual download URL for a video
func (c *Client) GetVideoDownloadURL(targetUin string, albumID string, picKey string) (string, error) {
	headers := map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", targetUin),
	}

	apiURL := fmt.Sprintf("https://h5.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/cgi_floatview_photo_list_v2?g_tk=%v&callback=viewer_Callback&topicId=%v&picKey=%v&cmtOrder=1&fupdate=1&plat=qzone&source=qzone&cmtNum=0&inCharset=utf-8&outCharset=utf-8&callbackFun=viewer&uin=%v&hostUin=%v&appid=4&isFirst=1", c.GTK, albumID, picKey, c.QQ, targetUin)

	start := time.Now()
	_, body, code, err := c.Http.Get(apiURL, headers)
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
	if vInfo.Get("status").Int() != 2 {
		return "", fmt.Errorf("video is not ready (status: %d)", vInfo.Get("status").Int())
	}

	downloadURL := vInfo.Get("download_url").String()
	if downloadURL == "" {
		downloadURL = vInfo.Get("video_url").String()
	}

	return downloadURL, nil
}

// GetFriendList fetches the list of friends
func (c *Client) GetFriendList() ([]gjson.Result, error) {
	apiURL := fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/r.qzone.qq.com/cgi-bin/tfriend/friend_ship_manager.cgi?uin=%v&do=1&fupdate=1&clean=1&g_tk=%v", c.QQ, c.GTK)
	headers := map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", c.QQ),
		"origin":     "https://user.qzone.qq.com",
	}

	start := time.Now()
	_, body, code, err := c.Http.Get(apiURL, headers)
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

// Helper functions

func parseJSONP(content string, callback string) (string, error) {
	start := strings.Index(content, "(")
	end := strings.LastIndex(content, ")")
	if start == -1 || end == -1 || end <= start {
		return "", fmt.Errorf("invalid JSONP response")
	}
	return content[start+1 : end], nil
}

// GetAlbumListURL returns the URL for album list
func GetAlbumListURL(hostUin, uin, gtk string) string {
	return fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/fcg_list_album_v3?g_tk=%v&callback=shine_Callback&hostUin=%v&uin=%v&appid=4&inCharset=utf-8&outCharset=utf-8&source=qzone&plat=qzone&format=jsonp&notice=0&filter=1&handset=4&pageNumModeSort=40&pageNumModeClass=15&needUserInfo=1&idcNum=4&callbackFun=shine", gtk, hostUin, uin)
}
