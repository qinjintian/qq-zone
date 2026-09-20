/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-20
 * @FileName: group.go
 * @Description: [QQ 群相册：拉取已加入的群、群相册列表和照片/视频，并规整成个人相册同款字段]
 */

package qzone

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const (
	// groupCGITimeout 群列表、相册、照片接口单次请求超时。
	groupCGITimeout = 25 * time.Second
	// groupAlbumPageSize 一次拉取群相册列表的条数，群相册一般不会超过这个量。
	groupAlbumPageSize = 1000
	// groupPhotoPageSize 网页端群相册每页照片数，翻页请求按这个步长走。
	groupPhotoPageSize = 36
	// groupPhotoMaxPages 单个相册最多翻多少页，防止接口异常时死循环。
	groupPhotoMaxPages = 500
	// groupRoleCreated 当前账号是这个群的群主。
	groupRoleCreated = "created"
	// groupRoleManaged 当前账号是管理员。
	groupRoleManaged = "managed"
	// groupRoleJoined 当前账号只是普通成员。
	groupRoleJoined = "joined"
	// groupRoleRecent 本机用过或手输过，接口列表里还没有。
	groupRoleRecent = "recent"
	// groupQunMgrUA 拉 qun.qq.com 群列表时用的手机 QQ 标识，桌面浏览器 UA 有时会被拒。
	groupQunMgrUA = "Mozilla/5.0 (Linux; Android 12; Pixel) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/90.0.4430.210 Mobile Safari/537.36 V1_AND_SQ_8.9.68_3898_YYB_D QQ/8.9.68.10240 NetType/WIFI"
)

// userStorageRoot 是每个 QQ 的本地目录，群号缓存写在这里。测试里会改成临时目录。
var userStorageRoot = filepath.Join("storage", "qzone")

// Group 是当前登录号加入的一个 QQ 群。
type Group struct {
	ID    string // 群号
	Name  string // 群名
	Role  string // created / managed / joined
	Owner string // 群主 QQ，接口没给时为空
}

// RoleLabel 把角色翻成菜单里能看懂的中文。
func (g Group) RoleLabel() string {
	switch g.Role {
	case groupRoleCreated:
		return "我创建的"
	case groupRoleManaged:
		return "我管理的"
	case groupRoleJoined:
		return "我加入的"
	case groupRoleRecent:
		return "用过的"
	default:
		return ""
	}
}

// DisplayName 群名优先，没有名字时用群号。
func (g Group) DisplayName() string {
	name := strings.TrimSpace(g.Name)
	if name == "" {
		return g.ID
	}
	return name
}

// GetGroupList 拉取当前账号加入的群。
// 空间扫码的 p_skey 管不了 qun.qq.com；有群管理页授权时走 get_group_list，否则旧接口多半拉空。
func (c *Client) GetGroupList(ctx context.Context) ([]Group, error) {
	if c.HasQunAuth() {
		return c.getGroupListFromQunMgrCookie(ctx, c.QunCookie)
	}

	groups, err := c.getGroupListFromQunQzone(ctx)
	if err == nil && len(groups) > 0 {
		return groups, nil
	}

	fallback, err2 := c.getGroupListFromQunMgr(ctx)
	if err2 == nil && len(fallback) > 0 {
		return fallback, nil
	}
	if err == nil {
		return groups, nil
	}
	if err2 == nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("%v；备选接口: %v", err, err2)
}

// GetGroupAlbumList 拉取指定群的相册列表，字段对齐个人相册：id / name / total / allowAccess。
func (c *Client) GetGroupAlbumList(ctx context.Context, groupID string) ([]gjson.Result, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil, fmt.Errorf("群号不能为空")
	}

	albums, err := c.getGroupAlbumListV2(ctx, groupID, false)
	if err != nil || len(albums) == 0 {
		alt, err2 := c.getGroupAlbumListV2(ctx, groupID, true)
		if err2 == nil && (len(alt) > 0 || err != nil) {
			albums, err = alt, nil
		}
	}
	if err != nil {
		return nil, err
	}

	out := make([]gjson.Result, 0, len(albums))
	for _, album := range albums {
		item := normalizeGroupAlbum(album, groupID, "")
		if item.Get("id").String() == "" {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

// GetGroupPhotoList 分页拉取群相册里的照片和视频，规整成 Spider 认识的 raw / origin_url / is_video 等字段。
func (c *Client) GetGroupPhotoList(ctx context.Context, groupID, albumID string) ([]gjson.Result, error) {
	groupID = strings.TrimSpace(groupID)
	albumID = strings.TrimSpace(albumID)
	if groupID == "" || albumID == "" {
		return nil, fmt.Errorf("群号和相册 ID 不能为空")
	}

	photos, err := c.getGroupPhotoListInQQ(ctx, groupID, albumID)
	if err != nil || len(photos) == 0 {
		alt, err2 := c.getGroupPhotoListV2(ctx, groupID, albumID)
		if err2 == nil && (len(alt) > 0 || err != nil) {
			photos, err = alt, nil
		}
	}
	if err != nil {
		return nil, err
	}

	out := make([]gjson.Result, 0, len(photos))
	for _, photo := range photos {
		item := normalizeGroupPhoto(photo, groupID, albumID)
		if item.Get("sloc").String() == "" && item.Get("url").String() == "" && item.Get("raw").String() == "" {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

// GroupVideoSource 从群相册照片 JSON 里取出视频下载链，不走个人相册的浮层接口。
func GroupVideoSource(photo gjson.Result) *VideoSource {
	src := &VideoSource{}
	if !photo.Exists() {
		return src
	}

	action := strings.TrimSpace(firstNonEmpty(
		photo.Get("videodata.actionurl").String(),
		photo.Get("video_url").String(),
	))
	play := strings.TrimSpace(firstNonEmpty(
		photo.Get("videodata.videourl").String(),
		photo.Get("videodata.video_url").String(),
	))
	src.VideoID = firstNonEmpty(
		photo.Get("vid").String(),
		photo.Get("video_id").String(),
		photo.Get("videodata.videoid").String(),
		photo.Get("videodata.video_id").String(),
	)

	seen := map[string]bool{}
	addVideoCandidate(&src.Candidates, seen, rewriteHTTPURL(action), VideoSourceDownload)
	if play != action {
		addVideoCandidate(&src.Candidates, seen, rewriteHTTPURL(play), VideoSourcePlay)
	}
	return src
}

func (c *Client) getGroupListFromQunQzone(ctx context.Context) ([]Group, error) {
	urls := []string{
		fmt.Sprintf("https://qun.qzone.qq.com/cgi-bin/get_group_list?uin=%s&g_tk=%s&fupdate=1", c.QQ, c.GTK),
		fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/qun.qzone.qq.com/cgi-bin/get_group_list?uin=%s&g_tk=%s&fupdate=1", c.QQ, c.GTK),
	}
	headers := c.groupQzoneHeaders()

	var lastErr error
	for _, apiURL := range urls {
		body, code, err := c.getCGI(ctx, "GetGroupListQzone", apiURL, headers)
		if err != nil {
			lastErr = err
			continue
		}
		if code != 200 {
			lastErr = fmt.Errorf("拉取群列表失败: HTTP %d", code)
			continue
		}
		data, err := decodeCGIJSON(body)
		if err != nil {
			lastErr = fmt.Errorf("解析群列表失败: %w", err)
			continue
		}
		if apiCode := cgiStatusCode(data); apiCode != 0 {
			lastErr = groupAPIError(apiCode, data)
			continue
		}
		groups := parseQunQzoneGroupList(data)
		return groups, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("拉取群列表失败")
	}
	return nil, lastErr
}

func (c *Client) getGroupListFromQunMgr(ctx context.Context) ([]Group, error) {
	attempts := []map[string]string{
		{
			"cookie":       c.Cookie,
			"user-agent":   UserAgent,
			"referer":      "https://qun.qq.com/member.html",
			"origin":       "https://qun.qq.com",
			"content-type": "application/x-www-form-urlencoded; charset=UTF-8",
		},
		{
			"cookie":       qunMgrCookie(c.Cookie),
			"user-agent":   groupQunMgrUA,
			"referer":      "https://qun.qq.com/member.html",
			"origin":       "https://qun.qq.com",
			"content-type": "application/x-www-form-urlencoded; charset=UTF-8",
		},
	}

	body := "bkn=" + url.QueryEscape(groupBknFromCookie(c.Cookie, c.GTK))
	var lastErr error
	for _, headers := range attempts {
		groups, err := c.postQunMgrGroupList(ctx, body, headers)
		if err == nil {
			return groups, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("拉取群列表失败")
	}
	return nil, lastErr
}

func (c *Client) getGroupListFromQunMgrCookie(ctx context.Context, cookie string) ([]Group, error) {
	body := "bkn=" + url.QueryEscape(groupBknFromCookie(cookie, c.GTK))
	headers := map[string]string{
		"cookie":       cookie,
		"user-agent":   UserAgent,
		"referer":      "https://qun.qq.com/member.html",
		"origin":       "https://qun.qq.com",
		"content-type": "application/x-www-form-urlencoded; charset=UTF-8",
	}
	return c.postQunMgrGroupList(ctx, body, headers)
}

func (c *Client) postQunMgrGroupList(ctx context.Context, form string, headers map[string]string) ([]Group, error) {
	apiURL := "https://qun.qq.com/cgi-bin/qun_mgr/get_group_list"
	cgiCtx, cancel := context.WithTimeout(ctx, groupCGITimeout)
	defer cancel()

	start := time.Now()
	_, raw, code, err := c.Http.Post(cgiCtx, apiURL, form, headers)
	bodyStr := string(raw)
	c.logAPI("GetGroupListQunMgr", apiURL, headers, bodyStr, code, time.Since(start), err)
	if err != nil {
		return nil, fmt.Errorf("拉取群列表失败: %w", err)
	}
	if code != 200 {
		return nil, fmt.Errorf("拉取群列表失败: HTTP %d", code)
	}

	res, err := decodeCGIJSON(bodyStr)
	if err != nil {
		return nil, fmt.Errorf("解析群列表失败: %w", err)
	}
	if apiCode := cgiStatusCode(res); apiCode != 0 {
		return nil, groupAPIError(apiCode, res)
	}
	return parseQunMgrGroupList(res), nil
}

func (c *Client) getGroupAlbumListV2(ctx context.Context, groupID string, asJSON bool) ([]gjson.Result, error) {
	params := url.Values{}
	params.Set("g_tk", c.GTK)
	params.Set("qunId", groupID)
	params.Set("qunid", groupID)
	params.Set("uin", c.QQ)
	params.Set("start", "0")
	params.Set("num", strconv.Itoa(groupAlbumPageSize))
	params.Set("getMemberRole", "1")
	params.Set("inCharset", "utf-8")
	params.Set("outCharset", "utf-8")
	params.Set("source", "qzone")
	params.Set("attach_info", "")
	if asJSON {
		params.Set("format", "json")
		params.Set("cmd", "qunGetAlbumList")
	} else {
		params.Set("callback", "shine2_Callback")
		params.Set("callbackFun", "shine2")
	}

	apiURL := "https://h5.qzone.qq.com/proxy/domain/u.photo.qzone.qq.com/cgi-bin/upp/qun_list_album_v2?" + params.Encode()
	headers := c.groupPhotoHeaders(groupID)
	delete(headers, "content-type")
	delete(headers, "x-requested-with")

	body, code, err := c.getCGI(ctx, "GetGroupAlbumList", apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("拉取群相册列表失败: %w", err)
	}
	if code != 200 {
		return nil, fmt.Errorf("拉取群相册列表失败: HTTP %d", code)
	}
	if strings.Contains(body, "对不起") {
		return nil, fmt.Errorf("无权访问该群相册，请确认你仍在群内且相册对你可见")
	}

	data, err := parseCGIBody(body)
	if err != nil {
		return nil, fmt.Errorf("解析群相册列表失败: %w", err)
	}
	res := gjson.Parse(data)
	if apiCode := cgiStatusCode(res); apiCode != 0 {
		return nil, groupAPIError(apiCode, res)
	}

	list := res.Get("data.album")
	if !list.Exists() || len(list.Array()) == 0 {
		list = res.Get("album")
	}
	if !list.Exists() {
		return nil, nil
	}
	return list.Array(), nil
}

func (c *Client) getGroupPhotoListInQQ(ctx context.Context, groupID, albumID string) ([]gjson.Result, error) {
	var (
		all   []gjson.Result
		start = 0
	)
	for page := 0; page < groupPhotoMaxPages; page++ {
		res, err := c.fetchGroupPhotoPageInQQ(ctx, groupID, albumID, start, true)
		if err != nil && page == 0 {
			res, err = c.fetchGroupPhotoPageInQQ(ctx, groupID, albumID, start, false)
		}
		if err != nil {
			return nil, err
		}

		list := pickGroupPhotoArray(res)
		all = append(all, list...)
		if len(list) == 0 {
			break
		}

		total := res.Get("data.total").Int()
		if total == 0 {
			total = res.Get("data.totalInAlbum").Int()
		}
		start += len(list)
		if total > 0 && int64(start) >= total {
			break
		}
		if len(list) < groupPhotoPageSize {
			break
		}
	}
	return all, nil
}

func (c *Client) fetchGroupPhotoPageInQQ(ctx context.Context, groupID, albumID string, start int, quoted bool) (gjson.Result, error) {
	apiURL := "https://h5.qzone.qq.com/groupphoto/inqq?g_tk=" + url.QueryEscape(c.GTK)
	form := fmt.Sprintf(
		"qunId=%s&albumId=%s&uin=%s&start=%d&num=%d&getCommentCnt=0&getMemberRole=0&hostUin=%s&getalbum=0&platform=qzone&inCharset=utf-8&outCharset=utf-8&source=qzone&cmd=qunGetPhotoList&qunid=%s&albumid=%s&attach_info=start_count%%3D%d",
		url.QueryEscape(groupID),
		url.QueryEscape(albumID),
		url.QueryEscape(c.QQ),
		start,
		groupPhotoPageSize,
		url.QueryEscape(c.QQ),
		url.QueryEscape(groupID),
		url.QueryEscape(albumID),
		start,
	)
	body := form
	if quoted {
		body = `"` + form + `"`
	}

	headers := c.groupPhotoHeaders(groupID)
	cgiCtx, cancel := context.WithTimeout(ctx, groupCGITimeout)
	defer cancel()

	startAt := time.Now()
	_, raw, code, err := c.Http.Post(cgiCtx, apiURL, body, headers)
	bodyStr := string(raw)
	c.logAPI("GetGroupPhotoList", apiURL, headers, bodyStr, code, time.Since(startAt), err)
	if err != nil {
		return gjson.Result{}, fmt.Errorf("拉取群相册照片失败: %w", err)
	}
	if code != 200 {
		return gjson.Result{}, fmt.Errorf("拉取群相册照片失败: HTTP %d", code)
	}
	if strings.Contains(bodyStr, "对不起") {
		return gjson.Result{}, fmt.Errorf("无权访问该群相册，请确认你仍在群内且相册对你可见")
	}

	data, err := parseCGIBody(bodyStr)
	if err != nil {
		return gjson.Result{}, fmt.Errorf("解析群相册照片失败: %w", err)
	}
	res := gjson.Parse(data)
	if apiCode := cgiStatusCode(res); apiCode != 0 {
		return gjson.Result{}, groupAPIError(apiCode, res)
	}
	return res, nil
}

func (c *Client) getGroupPhotoListV2(ctx context.Context, groupID, albumID string) ([]gjson.Result, error) {
	var (
		all    []gjson.Result
		offset = 0
	)
	for page := 0; page < groupPhotoMaxPages; page++ {
		params := url.Values{}
		params.Set("g_tk", c.GTK)
		params.Set("uin", c.QQ)
		params.Set("qunId", groupID)
		params.Set("albumId", albumID)
		params.Set("inCharset", "utf-8")
		params.Set("outCharset", "utf-8")
		params.Set("num", "500")
		params.Set("start", strconv.Itoa(offset))

		apiURL := "https://h5.qzone.qq.com/proxy/domain/u.photo.qzone.qq.com/cgi-bin/upp/qun_list_photo_v2?" + params.Encode()
		headers := c.groupPhotoHeaders(groupID)
		delete(headers, "content-type")
		delete(headers, "x-requested-with")

		body, code, err := c.getCGI(ctx, "GetGroupPhotoListV2", apiURL, headers)
		if err != nil {
			return nil, fmt.Errorf("拉取群相册照片失败: %w", err)
		}
		if code != 200 {
			return nil, fmt.Errorf("拉取群相册照片失败: HTTP %d", code)
		}

		data, err := parseCGIBody(body)
		if err != nil {
			return nil, fmt.Errorf("解析群相册照片失败: %w", err)
		}
		res := gjson.Parse(data)
		if apiCode := cgiStatusCode(res); apiCode != 0 {
			return nil, groupAPIError(apiCode, res)
		}

		list := pickGroupPhotoArray(res)
		all = append(all, list...)
		if len(list) == 0 {
			break
		}
		offset += len(list)
		if len(list) < 500 {
			break
		}
	}
	return all, nil
}

func (c *Client) getCGI(ctx context.Context, name, apiURL string, headers map[string]string) (string, int, error) {
	cgiCtx, cancel := context.WithTimeout(ctx, groupCGITimeout)
	defer cancel()

	start := time.Now()
	_, body, code, err := c.Http.Get(cgiCtx, apiURL, headers)
	bodyStr := string(body)
	c.logAPI(name, apiURL, headers, bodyStr, code, time.Since(start), err)
	return bodyStr, code, err
}

func (c *Client) groupQzoneHeaders() map[string]string {
	return map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", c.QQ),
		"origin":     "https://user.qzone.qq.com",
	}
}

func (c *Client) groupPhotoHeaders(groupID string) map[string]string {
	return map[string]string{
		"cookie":           c.Cookie,
		"user-agent":       UserAgent,
		"referer":          fmt.Sprintf("https://h5.qzone.qq.com/groupphoto/index?inqq=1&groupId=%s", groupID),
		"origin":           "https://h5.qzone.qq.com",
		"accept":           "application/json, text/javascript, */*; q=0.01",
		"content-type":     "application/x-www-form-urlencoded; charset=UTF-8",
		"x-requested-with": "XMLHttpRequest",
	}
}

func (c *Client) groupBkn() string {
	return groupBknFromCookie(c.Cookie, c.GTK)
}

func groupBknFromCookie(cookie, fallback string) string {
	if skey := extractCookieValue(cookie, "skey"); skey != "" {
		return calculateGTK(skey)
	}
	if pskey := extractCookieValue(cookie, "p_skey"); pskey != "" {
		return calculateGTK(pskey)
	}
	return fallback
}

func qunMgrCookie(cookie string) string {
	keep := map[string]bool{"uin": true, "skey": true, "p_uin": true, "ptcz": true, "RK": true}
	var parts []string
	for _, p := range strings.Split(cookie, ";") {
		p = strings.TrimSpace(p)
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 || kv[1] == "" {
			continue
		}
		if keep[kv[0]] {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	return strings.Join(parts, "; ")
}

func decodeCGIJSON(body string) (gjson.Result, error) {
	if cgiLooksLikeHTML(body) {
		return gjson.Result{}, fmt.Errorf("接口返回了网页而不是数据")
	}
	data, err := parseCGIBody(body)
	if err != nil {
		return gjson.Result{}, err
	}
	trim := strings.TrimSpace(data)
	if !strings.HasPrefix(trim, "{") && !strings.HasPrefix(trim, "[") {
		return gjson.Result{}, fmt.Errorf("接口返回无法解析")
	}
	return gjson.Parse(data), nil
}

func cgiLooksLikeHTML(body string) bool {
	s := strings.TrimSpace(body)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		return false
	}
	head := strings.ToLower(s)
	if len(head) > 240 {
		head = head[:240]
	}
	return strings.HasPrefix(head, "<!doctype") ||
		strings.HasPrefix(head, "<html") ||
		strings.Contains(head, "<html") ||
		strings.Contains(head, "<!doctype html")
}

func knownGroupsFile(qq string) string {
	return filepath.Join(userStorageRoot, qq, "groups.json")
}

// LoadKnownGroups 读本机记过的群：手输成功过的，以及 qun/<群号>/ 目录。
func LoadKnownGroups(qq string) []Group {
	qq = strings.TrimSpace(qq)
	if qq == "" {
		return nil
	}

	var extra []Group
	if data, err := os.ReadFile(knownGroupsFile(qq)); err == nil {
		var stored []Group
		if json.Unmarshal(data, &stored) == nil {
			extra = append(extra, stored...)
		}
	}

	qunDir := filepath.Join(userStorageRoot, qq, "qun")
	if entries, err := os.ReadDir(qunDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || !IsValidGroupID(e.Name()) {
				continue
			}
			extra = append(extra, Group{ID: e.Name(), Role: groupRoleRecent})
		}
	}

	for i := range extra {
		if extra[i].Role == "" {
			extra[i].Role = groupRoleRecent
		}
	}
	return MergeGroups(nil, extra)
}

// RememberGroup 把用过的群号记到本机，下次拉不到接口时还能出现在列表里。
func RememberGroup(qq string, g Group) error {
	qq = strings.TrimSpace(qq)
	g.ID = strings.TrimSpace(g.ID)
	if qq == "" || !IsValidGroupID(g.ID) {
		return nil
	}
	if g.Role == "" {
		g.Role = groupRoleRecent
	}

	merged := MergeGroups(LoadKnownGroups(qq), []Group{g})
	dir := filepath.Join(userStorageRoot, qq)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(knownGroupsFile(qq), data, 0o644)
}

// VisibleGroups 决定菜单里出现哪些群。
// 已经拉到完整加入名单时，只展示仍在的群，本机用过但已退的不再列出。
// 还没拉到完整名单时，把本机用过的补上，方便先下手头的群。
func VisibleGroups(remote, local []Group, listedAll bool) []Group {
	if listedAll {
		return MergeGroups(remote, nil)
	}
	return MergeGroups(remote, local)
}

// MergeGroups 远程列表在前；本机记过的群补在后面，群号相同则保留已有名字和角色。
func MergeGroups(primary, extra []Group) []Group {
	out := make([]Group, 0, len(primary)+len(extra))
	seen := map[string]int{}
	add := func(g Group) {
		g.ID = strings.TrimSpace(g.ID)
		if g.ID == "" || g.ID == "0" || !IsValidGroupID(g.ID) {
			return
		}
		if idx, ok := seen[g.ID]; ok {
			if strings.TrimSpace(out[idx].Name) == "" || isPlaceholderGroupName(out[idx].ID, out[idx].Name) {
				if name := strings.TrimSpace(g.Name); name != "" && !isPlaceholderGroupName(g.ID, name) {
					out[idx].Name = name
				}
			}
			if out[idx].Role == "" || out[idx].Role == groupRoleRecent {
				if g.Role != "" && g.Role != groupRoleRecent {
					out[idx].Role = g.Role
				}
			}
			if out[idx].Owner == "" {
				out[idx].Owner = g.Owner
			}
			return
		}
		seen[g.ID] = len(out)
		out = append(out, g)
	}
	for _, g := range primary {
		add(g)
	}
	for _, g := range extra {
		add(g)
	}
	return out
}

func isPlaceholderGroupName(id, name string) bool {
	name = strings.TrimSpace(name)
	return name == "" || name == id || name == "群"+id
}

func parseQunQzoneGroupList(res gjson.Result) []Group {
	items := res.Get("data.group")
	if !items.Exists() || len(items.Array()) == 0 {
		items = res.Get("data.groupList")
	}
	if !items.Exists() || len(items.Array()) == 0 {
		items = res.Get("group")
	}
	out := make([]Group, 0, len(items.Array()))
	seen := map[string]bool{}
	for _, item := range items.Array() {
		g := Group{
			ID:    firstNonEmpty(jsonStringID(item.Get("groupid")), jsonStringID(item.Get("gc")), jsonStringID(item.Get("groupId"))),
			Name:  strings.TrimSpace(firstNonEmpty(item.Get("groupname").String(), item.Get("gn").String(), item.Get("groupName").String())),
			Role:  groupRoleJoined,
			Owner: jsonStringID(item.Get("owner")),
		}
		if g.ID == "" || g.ID == "0" || seen[g.ID] {
			continue
		}
		seen[g.ID] = true
		out = append(out, g)
	}
	return out
}

func parseQunMgrGroupList(res gjson.Result) []Group {
	type bucket struct {
		key  string
		role string
	}
	buckets := []bucket{
		{key: "create", role: groupRoleCreated},
		{key: "manage", role: groupRoleManaged},
		{key: "join", role: groupRoleJoined},
	}

	out := make([]Group, 0)
	seen := map[string]bool{}
	for _, b := range buckets {
		for _, item := range res.Get(b.key).Array() {
			g := Group{
				ID:    firstNonEmpty(jsonStringID(item.Get("gc")), jsonStringID(item.Get("groupid"))),
				Name:  strings.TrimSpace(firstNonEmpty(item.Get("gn").String(), item.Get("groupname").String())),
				Role:  b.role,
				Owner: jsonStringID(item.Get("owner")),
			}
			if g.ID == "" || g.ID == "0" || seen[g.ID] {
				continue
			}
			seen[g.ID] = true
			out = append(out, g)
		}
	}
	return out
}

func normalizeGroupAlbum(item gjson.Result, groupID, groupName string) gjson.Result {
	fields := overlayJSON(item.Raw, map[string]interface{}{
		"id":          firstNonEmpty(item.Get("id").String(), jsonStringID(item.Get("albumid")), jsonStringID(item.Get("albumId"))),
		"name":        strings.TrimSpace(firstNonEmpty(item.Get("title").String(), item.Get("name").String())),
		"total":       groupAlbumTotal(item),
		"allowAccess": 1,
		"_group_id":   groupID,
		"_group_name": groupName,
	})
	return gjson.ParseBytes(fields)
}

func groupAlbumTotal(item gjson.Result) int64 {
	if n := item.Get("photocnt").Int(); n > 0 {
		return n
	}
	if n := item.Get("photo_cnt").Int(); n > 0 {
		return n
	}
	if n := item.Get("total").Int(); n > 0 {
		return n
	}
	return item.Get("num").Int()
}

func normalizeGroupPhoto(photo gjson.Result, groupID, albumID string) gjson.Result {
	sloc := firstNonEmpty(photo.Get("sloc").String(), photo.Get("lloc").String(), photo.Get("id").String(), jsonStringID(photo.Get("photoid")))
	name := strings.TrimSpace(firstNonEmpty(photo.Get("name").String(), photo.Get("desc").String(), sloc))
	origin := pickGroupPhotoURL(photo)
	upload := groupPhotoTime(photo)
	videoURL := strings.TrimSpace(firstNonEmpty(
		photo.Get("videodata.actionurl").String(),
		photo.Get("videodata.videourl").String(),
		photo.Get("video_url").String(),
	))
	isVideo := videoURL != ""

	fields := overlayJSON(photo.Raw, map[string]interface{}{
		"id":           sloc,
		"sloc":         sloc,
		"lloc":         firstNonEmpty(photo.Get("lloc").String(), sloc),
		"name":         name,
		"raw":          origin,
		"origin_url":   origin,
		"url":          firstNonEmpty(origin, photo.Get("url").String(), photo.Get("burl").String()),
		"is_video":     isVideo,
		"uploadtime":   upload,
		"rawshoottime": firstNonEmpty(groupShootTime(photo), upload),
		"video_url":    rewriteHTTPURL(videoURL),
		"vid":          firstNonEmpty(photo.Get("videodata.videoid").String(), photo.Get("vid").String()),
		"_group_id":    groupID,
		"_album_id":    albumID,
	})
	return gjson.ParseBytes(fields)
}

func pickGroupPhotoURL(photo gjson.Result) string {
	var (
		origin      string
		bestURL     string
		bestArea    int64 = -1
		bestEnlarge int64 = -1
	)

	photo.Get("photourl").ForEach(func(_, v gjson.Result) bool {
		u := strings.TrimSpace(v.Get("url").String())
		if u == "" {
			return true
		}
		w := v.Get("width").Int()
		h := v.Get("height").Int()
		if w == 0 && h == 0 && origin == "" {
			origin = u
		}
		area := w * h
		en := v.Get("enlarge_rate").Int()
		if area > bestArea || (area == bestArea && en > bestEnlarge) {
			bestArea = area
			bestEnlarge = en
			bestURL = u
		}
		return true
	})

	picked := firstNonEmpty(
		origin,
		bestURL,
		photo.Get("burl").String(),
		photo.Get("origin_url").String(),
		photo.Get("raw").String(),
		photo.Get("url").String(),
	)
	return rewriteHTTPURL(picked)
}

func pickGroupPhotoArray(res gjson.Result) []gjson.Result {
	for _, path := range []string{
		"data.photolist",
		"data.photoList",
		"data.photos",
		"data.photo_list",
		"photolist",
		"photos",
	} {
		list := res.Get(path)
		if list.Exists() && list.IsArray() {
			return list.Array()
		}
	}
	return nil
}

func groupPhotoTime(photo gjson.Result) string {
	if s := strings.TrimSpace(photo.Get("uploadtime").String()); looksLikeDateTime(s) {
		return s
	}
	if s := strings.TrimSpace(photo.Get("uploadTime").String()); looksLikeDateTime(s) {
		return s
	}
	return unixToDateTime(firstPositiveInt(
		photo.Get("uploadtime").Int(),
		photo.Get("phototime").Int(),
		photo.Get("timestamp").Int(),
	))
}

func groupShootTime(photo gjson.Result) string {
	if s := strings.TrimSpace(photo.Get("rawshoottime").String()); looksLikeDateTime(s) {
		return s
	}
	if s := strings.TrimSpace(photo.Get("shoottime").String()); looksLikeDateTime(s) {
		return s
	}
	return unixToDateTime(firstPositiveInt(
		photo.Get("rawshoottime").Int(),
		photo.Get("shoottime").Int(),
		photo.Get("phototime").Int(),
	))
}

func looksLikeDateTime(s string) bool {
	if len(s) < 10 || s == "0" {
		return false
	}
	_, err := time.Parse("2006-01-02 15:04:05", s)
	if err == nil {
		return true
	}
	_, err = time.Parse("2006-01-02", s[:10])
	return err == nil
}

func unixToDateTime(v int64) string {
	if v <= 0 {
		return ""
	}
	if v > 1e12 {
		v = v / 1000
	}
	return time.Unix(v, 0).In(time.Local).Format("2006-01-02 15:04:05")
}

func firstPositiveInt(values ...int64) int64 {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func cgiStatusCode(res gjson.Result) int64 {
	if res.Get("code").Exists() {
		return res.Get("code").Int()
	}
	if res.Get("ec").Exists() {
		return res.Get("ec").Int()
	}
	if res.Get("ret").Exists() {
		return res.Get("ret").Int()
	}
	return 0
}

func groupAPIError(code int64, res gjson.Result) error {
	msg := strings.TrimSpace(firstNonEmpty(res.Get("message").String(), res.Get("msg").String(), res.Get("em").String()))
	switch {
	case code == -3000 || code == -10000:
		if msg == "" {
			msg = "登录已失效，请重新扫码"
		}
		return fmt.Errorf("群相册接口错误 (code: %d): %s", code, msg)
	case strings.Contains(msg, "权限") || strings.Contains(msg, "无权") || strings.Contains(msg, "对不起"):
		if msg == "" {
			msg = "无权访问"
		}
		return fmt.Errorf("无权访问该群相册: %s", msg)
	default:
		if msg == "" {
			msg = "未知错误"
		}
		return fmt.Errorf("群相册接口错误 (code: %d): %s", code, msg)
	}
}

// IsQunAuthError 群列表接口表示没登录或票据过期，需要再扫一次群管理页。
func IsQunAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if strings.Contains(s, "no login") || strings.Contains(s, "登录已失效") {
		return true
	}
	return strings.Contains(s, "(code: 4)") || strings.Contains(s, "code: 4:")
}

func overlayJSON(raw string, extra map[string]interface{}) []byte {
	var m map[string]interface{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	for k, v := range extra {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		b, _ = json.Marshal(extra)
	}
	return b
}

func rewriteHTTPURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "http://") {
		return "https://" + strings.TrimPrefix(u, "http://")
	}
	return u
}

func IsValidGroupID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 4 || len(s) > 15 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
