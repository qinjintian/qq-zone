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
 * @LastEditTime: 2026-09-04 17:10:00
 * @FileName: video.go
 * @Description: [QQ 空间视频多源解析：下载链优先，播放链/HLS/腾讯视频 getinfo 作为失效 URL 的兜底]
 */

package qzone

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/qinjintian/qq-zone/internal/net/http"
	"github.com/tidwall/gjson"
)

const (
	// VideoSourceDownload 相册原文件下载地址，对应网页端「下载视频」。
	VideoSourceDownload = "download"
	// VideoSourcePlay 网页播放器使用的媒体地址，对应 Chrono 一类嗅探器抓到的链接。
	VideoSourcePlay = "play"
	// VideoSourceHLS 播放器走 HLS 时的 m3u8 地址。
	VideoSourceHLS = "hls"
	// VideoSourceGetInfo 通过 vid 调用腾讯视频 getinfo 解析出的 CDN 地址。
	VideoSourceGetInfo = "getinfo"
)

// tencentVIDPattern 匹配腾讯视频 11 位 vid。相册里其它格式的 video_id 不能拿去打 getinfo。
var tencentVIDPattern = regexp.MustCompile(`(?i)^[a-z0-9]{11}$`)

// VideoCandidate 表示一条可尝试的视频拉取地址。
type VideoCandidate struct {
	URL  string // 实际请求的媒体地址
	Kind string // 来源类型：download / play / hls / getinfo
}

// VideoSource 一次解析得到的全部候选源，以及后续 getinfo 兜底用的 vid。
type VideoSource struct {
	Candidates []VideoCandidate // 按优先顺序排列：下载链 → 播放链 → HLS
	VideoID    string           // 腾讯视频 vid；下载链全失败后才用来调 getinfo
}

// GetVideoSource 解析指定相册视频的全部可用地址。
// 顺序为：download_url（含 f0/f20 清晰度变体）→ video_url 播放链 → HLS。
// 腾讯视频 getinfo 不会在这里预取，避免每个视频都多打一次接口。
func (c *Client) GetVideoSource(ctx context.Context, targetUin, albumID, sloc string, photo gjson.Result) (*VideoSource, error) {
	src := &VideoSource{}
	var lastErr error

	floatview, err := c.fetchFloatviewVideoInfo(ctx, targetUin, albumID, sloc)
	if err != nil {
		lastErr = err
	} else {
		src.mergeVideoInfo(floatview)
	}

	// 相册列表里偶发也带 video_info，与 floatview 合并去重，避免接口抖动丢字段。
	if photo.Exists() {
		src.mergeVideoInfo(photo.Get("video_info"))
		if src.VideoID == "" {
			src.VideoID = firstNonEmpty(photo.Get("video_id").String(), photo.Get("vid").String())
		}
	}

	if len(src.Candidates) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no video found in response")
	}
	return src, nil
}

// ResolveTencentVideo 用 11 位 vid 调腾讯 getinfo，把解析出的 CDN 地址作为 getinfo 候选返回。
// 调用方通常在 download / play / hls 都失败后再用；非法 vid 直接返回空列表。
func (c *Client) ResolveTencentVideo(ctx context.Context, vid string) []VideoCandidate {
	vid = strings.TrimSpace(vid)
	if vid == "" || !tencentVIDPattern.MatchString(vid) {
		return nil
	}

	// 两条 getinfo 入口轮询：H5 优先，失败再试 PC 端。
	apis := []string{
		fmt.Sprintf("https://h5vv.video.qq.com/getinfo?vid=%s&platform=11001&sdtfrom=v1010&otype=json&defn=mp4", vid),
		fmt.Sprintf("https://vv.video.qq.com/getinfo?vids=%s&platform=101001&charge=0&otype=json&defn=shd", vid),
	}

	var out []VideoCandidate
	seen := map[string]bool{}
	headers := map[string]string{
		"user-agent": UserAgent,
		"referer":    "https://v.qq.com/",
	}

	for _, apiURL := range apis {
		start := time.Now()
		_, body, code, err := c.Http.Get(ctx, apiURL, headers)
		c.logAPI("ResolveTencentVideo", apiURL, headers, string(body), code, time.Since(start), err)
		if err != nil || code != 200 {
			continue
		}
		for _, u := range parseTencentGetInfoURLs(string(body)) {
			addVideoCandidate(&out, seen, u, VideoSourceGetInfo)
		}
		if len(out) > 0 {
			break
		}
	}
	return out
}

// fetchFloatviewVideoInfo 调用空间浮层接口，取出指定照片条目上的 video_info。
func (c *Client) fetchFloatviewVideoInfo(ctx context.Context, targetUin, albumID, sloc string) (gjson.Result, error) {
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
		return gjson.Result{}, err
	}
	if code != 200 {
		return gjson.Result{}, fmt.Errorf("failed to fetch video download url: status code %d", code)
	}

	data, err := parseJSONP(bodyStr, "viewer_Callback")
	if err != nil {
		return gjson.Result{}, fmt.Errorf("failed to parse video response: %w (raw body: %s)", err, bodyStr)
	}

	res := gjson.Parse(data)
	photos := res.Get("data.photos").Array()
	if len(photos) == 0 {
		return gjson.Result{}, fmt.Errorf("no video found in response")
	}

	video := pickFloatviewPhoto(photos, sloc, res.Get("data.picPosInPage").Int())
	vInfo := video.Get("video_info")
	if !vInfo.Exists() || vInfo.Raw == "" || vInfo.Raw == "{}" {
		return gjson.Result{}, fmt.Errorf("no video found in response")
	}
	return vInfo, nil
}

// pickFloatviewPhoto 从浮层返回的照片列表里定位当前视频。
// 优先按 sloc/lloc/picKey 精确匹配，其次用页内序号，最后退到第一条带 video_info 的项。
func pickFloatviewPhoto(photos []gjson.Result, sloc string, picPos int64) gjson.Result {
	for _, p := range photos {
		if p.Get("sloc").String() == sloc || p.Get("lloc").String() == sloc || p.Get("picKey").String() == sloc {
			return p
		}
	}
	if picPos >= 0 && int(picPos) < len(photos) {
		return photos[picPos]
	}
	for _, p := range photos {
		if p.Get("is_video").Bool() || p.Get("video_info").Exists() {
			return p
		}
	}
	return photos[0]
}

// mergeVideoInfo 把一份 video_info JSON 里的下载链、播放链并入候选列表。
// 已知字段名先收，再扫一遍所有 http 字符串，兼容接口改名；封面图会被跳过。
func (src *VideoSource) mergeVideoInfo(vInfo gjson.Result) {
	if src == nil || !vInfo.Exists() {
		return
	}

	if src.VideoID == "" {
		src.VideoID = firstNonEmpty(vInfo.Get("video_id").String(), vInfo.Get("vid").String())
	}

	seen := make(map[string]bool, len(src.Candidates)+8)
	for _, c := range src.Candidates {
		seen[c.URL] = true
	}

	var downloadURLs, playURLs []string
	for _, key := range []string{"download_url", "downloadUrl", "origin_url"} {
		if u := vInfo.Get(key).String(); u != "" {
			downloadURLs = append(downloadURLs, u)
		}
	}
	for _, key := range []string{"video_url", "videoUrl", "play_url", "playurl", "url"} {
		if u := vInfo.Get(key).String(); u != "" {
			playURLs = append(playURLs, u)
		}
	}

	vInfo.ForEach(func(key, value gjson.Result) bool {
		if value.Type != gjson.String {
			return true
		}
		u := value.String()
		if !strings.HasPrefix(u, "http") {
			return true
		}
		k := strings.ToLower(key.String())
		if strings.Contains(k, "cover") || strings.Contains(k, "thumb") || strings.Contains(k, "pic") || strings.Contains(k, "image") {
			return true
		}
		if strings.Contains(k, "download") {
			downloadURLs = append(downloadURLs, u)
			return true
		}
		if strings.Contains(k, "video") || strings.Contains(k, "play") || k == "url" {
			playURLs = append(playURLs, u)
		}
		return true
	})

	appendVariants := func(raw string, kind string) {
		for _, u := range qualityVariants(raw) {
			if http.IsHLSURL(u) {
				addVideoCandidate(&src.Candidates, seen, u, VideoSourceHLS)
				continue
			}
			addVideoCandidate(&src.Candidates, seen, u, kind)
		}
	}

	// 下载链必须排在播放链前面，调用方会按 Candidates 顺序依次尝试。

	for _, u := range downloadURLs {
		appendVariants(u, VideoSourceDownload)
	}
	for _, u := range playURLs {
		appendVariants(u, VideoSourcePlay)
	}
}

// addVideoCandidate 写入候选列表；http:// 会再补一条 https://，已经出现过的地址会被丢掉。
func addVideoCandidate(dst *[]VideoCandidate, seen map[string]bool, raw, kind string) {
	for _, u := range expandURLSchemes(raw) {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		*dst = append(*dst, VideoCandidate{URL: u, Kind: kind})
	}
}

// qualityVariants 为 QQ 空间常见的 f0/f20 清晰度后缀互出一条备用地址。
// f0 通常是原片，f20 是转码档；下载链失效时另一档有时仍可用。
func qualityVariants(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 3)
	push := func(u string) {
		if u == "" {
			return
		}
		for _, exist := range out {
			if exist == u {
				return
			}
		}
		out = append(out, u)
	}
	switch {
	case strings.Contains(raw, ".f20.mp4"):
		push(strings.Replace(raw, ".f20.mp4", ".f0.mp4", 1))
		push(raw)
	case strings.Contains(raw, ".f0.mp4"):
		push(raw)
		push(strings.Replace(raw, ".f0.mp4", ".f20.mp4", 1))
	default:
		push(raw)
	}
	return out
}

// expandURLSchemes 规范化 JSON 转义斜杠，并给 http 地址补一条 https 备用。
func expandURLSchemes(raw string) []string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, `\/`, "/"))
	if raw == "" || !strings.HasPrefix(raw, "http") {
		return nil
	}
	out := []string{raw}
	if strings.HasPrefix(raw, "http://") {
		out = append(out, "https://"+strings.TrimPrefix(raw, "http://"))
	}
	return out
}

// parseTencentGetInfoURLs 从 getinfo 的 JSON/JSONP 里拼出完整 mp4 地址：CDN 前缀 + 文件名 + vkey。
func parseTencentGetInfoURLs(body string) []string {
	jsonStr := strings.TrimSpace(body)
	// getinfo 常包一层 QZOutputJson=... 回调，先裁出中间的 JSON 对象。
	if i := strings.Index(jsonStr, "{"); i >= 0 {
		if j := strings.LastIndex(jsonStr, "}"); j > i {
			jsonStr = jsonStr[i : j+1]
		}
	}
	res := gjson.Parse(jsonStr)
	vi := res.Get("vl.vi.0")
	if !vi.Exists() {
		return nil
	}
	fn := vi.Get("fn").String()
	fvkey := vi.Get("fvkey").String()
	if fn == "" || fvkey == "" {
		return nil
	}
	// 分片视频结构更复杂，这里只兜底完整 mp4。
	if vi.Get("cl.fc").Int() > 0 {
		return nil
	}

	var urls []string
	vi.Get("ul.ui").ForEach(func(_, ui gjson.Result) bool {
		base := strings.TrimSpace(ui.Get("url").String())
		if base == "" {
			return true
		}
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		urls = append(urls, base+fn+"?vkey="+fvkey)
		return true
	})
	return urls
}

// firstNonEmpty 返回第一个非空字符串，用于从多个字段名里取 vid。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
