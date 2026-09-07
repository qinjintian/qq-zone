/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-07
 * @FileName: mood.go
 * @Description: [QQ 空间说说接口：列表分页、详情/评论补全、配图补拉]
 */

package qzone

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const moodPageSize = 20 // 网页端说说列表一页条数；pos 必须按这个步长加，不能按返回条数加

// MoodListPage 是 emotion_cgi_msglist_v6 一页的解析结果。
type MoodListPage struct {
	Total    int            // usrinfo.msgnum，空间侧声明的说说总数（含当前账号不可见的）
	Nickname string         // 空间主人昵称
	Uin      string         // 空间主人 QQ
	Messages []gjson.Result // 本页可见说说；不可见的条目会被接口直接丢掉
	Archived bool           // msglist 为空且仍声明还有数据：更早的说说被封存/无权查看
	Raw      string         // 本页原始 JSON，可选落盘
}

// MoodDetail 是 emotion_cgi_msgdetail_v6 聚合后的一条说说（含翻页评论）。
type MoodDetail struct {
	Item     gjson.Result   // 详情主体，长文通常比列表更完整
	Comments []gjson.Result // 全部评论页拼起来
}

// GetMoodList 拉取一页说说。pos 按接口的逻辑偏移递增（每次加请求的 num），
// 不要按返回条数加：不可见说说仍占 pos，但不会出现在 msglist 里。
func (c *Client) GetMoodList(ctx context.Context, targetUin string, pos int) (*MoodListPage, error) {
	params := url.Values{}
	params.Set("uin", targetUin)
	params.Set("ftype", "0")
	params.Set("sort", "0")
	params.Set("pos", fmt.Sprintf("%d", pos))
	params.Set("num", fmt.Sprintf("%d", moodPageSize))
	params.Set("replynum", "100")
	params.Set("g_tk", c.GTK)
	params.Set("callback", "shine_Callback")
	params.Set("code_version", "1")
	params.Set("format", "jsonp")
	params.Set("need_private_comment", "1")
	params.Set("cgi_host", "https://taotao.qq.com/cgi-bin/emotion_cgi_msglist_v6")

	apiURL := "https://user.qzone.qq.com/proxy/domain/taotao.qq.com/cgi-bin/emotion_cgi_msglist_v6?" + params.Encode()
	headers := c.moodHeaders(targetUin)

	start := time.Now()
	_, body, code, err := c.Http.Get(ctx, apiURL, headers)
	bodyStr := string(body)
	c.logAPI("GetMoodList", apiURL, headers, bodyStr, code, time.Since(start), err)

	if err != nil {
		return nil, fmt.Errorf("拉取说说列表失败: %w", err)
	}
	if code != 200 {
		return nil, fmt.Errorf("拉取说说列表失败: HTTP %d", code)
	}

	data, err := parseCGIBody(bodyStr)
	if err != nil {
		return nil, fmt.Errorf("解析说说列表失败: %w", err)
	}

	res := gjson.Parse(data)
	if apiCode := res.Get("code").Int(); apiCode != 0 {
		return nil, moodAPIError(apiCode, res)
	}

	page := &MoodListPage{
		Total:    int(res.Get("usrinfo.msgnum").Int()),
		Nickname: res.Get("usrinfo.name").String(),
		Uin:      firstNonEmpty(res.Get("usrinfo.uin").String(), targetUin),
		Raw:      data,
	}

	msglist := res.Get("msglist")
	if !msglist.Exists() || msglist.Type == gjson.Null {
		// 列表接口在「后面的说说被封存 / 无权」时会返回 msglist: null。
		if pos > 0 || page.Total > 0 {
			page.Archived = true
		}
		return page, nil
	}

	page.Messages = msglist.Array()
	if len(page.Messages) == 0 && page.Total > pos {
		page.Archived = true
	}
	return page, nil
}

// MoodPageSize 返回说说列表每页请求条数，备份循环按这个步长增加 pos。
func MoodPageSize() int {
	return moodPageSize
}

// GetMoodDetail 拉取单条说说详情，并翻页拼完整评论。
// 列表里评论超过约 10 条、或正文被截断时需要走这个接口。
func (c *Client) GetMoodDetail(ctx context.Context, targetUin, tid string) (*MoodDetail, error) {
	tid = strings.TrimSpace(tid)
	if tid == "" {
		return nil, fmt.Errorf("empty mood tid")
	}

	const pageNum = 20
	var (
		detail   = &MoodDetail{}
		pos      = 0
		allCmts  []gjson.Result
		gotFirst bool
	)

	for {
		item, comments, err := c.getMoodDetailPage(ctx, targetUin, tid, pos, pageNum)
		if err != nil {
			if !gotFirst {
				return nil, err
			}
			break
		}
		if !gotFirst {
			detail.Item = item
			gotFirst = true
		}
		if len(comments) == 0 {
			break
		}
		allCmts = append(allCmts, comments...)
		if len(comments) < pageNum {
			break
		}
		pos += pageNum
		if pos > 5000 {
			break
		}
	}

	detail.Comments = allCmts
	return detail, nil
}

// getMoodDetailPage 拉取详情接口的一页评论；not_trunc_con=1 尽量拿完整正文。
func (c *Client) getMoodDetailPage(ctx context.Context, targetUin, tid string, pos, num int) (gjson.Result, []gjson.Result, error) {
	params := url.Values{}
	params.Set("uin", targetUin)
	params.Set("tid", tid)
	params.Set("ftype", "0")
	params.Set("sort", "0")
	params.Set("pos", fmt.Sprintf("%d", pos))
	params.Set("num", fmt.Sprintf("%d", num))
	params.Set("g_tk", c.GTK)
	params.Set("callback", "shine_Callback")
	params.Set("code_version", "1")
	params.Set("format", "jsonp")
	params.Set("need_private_comment", "1")
	params.Set("not_trunc_con", "1")

	apiURL := "https://user.qzone.qq.com/proxy/domain/taotao.qq.com/cgi-bin/emotion_cgi_msgdetail_v6?" + params.Encode()
	headers := c.moodHeaders(targetUin)

	start := time.Now()
	_, body, code, err := c.Http.Get(ctx, apiURL, headers)
	bodyStr := string(body)
	c.logAPI("GetMoodDetail", apiURL, headers, bodyStr, code, time.Since(start), err)

	if err != nil {
		return gjson.Result{}, nil, fmt.Errorf("拉取说说详情失败: %w", err)
	}
	if code != 200 {
		return gjson.Result{}, nil, fmt.Errorf("拉取说说详情失败: HTTP %d", code)
	}

	data, err := parseCGIBody(bodyStr)
	if err != nil {
		return gjson.Result{}, nil, fmt.Errorf("解析说说详情失败: %w", err)
	}

	res := gjson.Parse(data)
	if apiCode := res.Get("code").Int(); apiCode != 0 {
		return gjson.Result{}, nil, moodAPIError(apiCode, res)
	}

	comments := res.Get("commentlist").Array()
	return res, comments, nil
}

// GetMoodPics 补拉一条说说在列表里被截断的配图地址。
func (c *Client) GetMoodPics(ctx context.Context, targetUin, tid string) ([]string, error) {
	params := url.Values{}
	params.Set("uin", targetUin)
	params.Set("tid", tid)
	params.Set("g_tk", c.GTK)
	params.Set("callback", "shine_Callback")
	params.Set("format", "jsonp")

	apiURL := "https://user.qzone.qq.com/proxy/domain/taotao.qq.com/cgi-bin/emotion_cgi_get_pics_v6?" + params.Encode()
	headers := c.moodHeaders(targetUin)

	start := time.Now()
	_, body, code, err := c.Http.Get(ctx, apiURL, headers)
	bodyStr := string(body)
	c.logAPI("GetMoodPics", apiURL, headers, bodyStr, code, time.Since(start), err)

	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("拉取说说配图失败: HTTP %d", code)
	}

	data, err := parseCGIBody(bodyStr)
	if err != nil {
		return nil, err
	}

	res := gjson.Parse(data)
	if apiCode := res.Get("code").Int(); apiCode != 0 && !res.Get("imageUrls").Exists() {
		return nil, moodAPIError(apiCode, res)
	}

	var urls []string
	res.Get("imageUrls").ForEach(func(_, v gjson.Result) bool {
		s := strings.TrimSpace(v.String())
		if s != "" {
			urls = append(urls, s)
		}
		return true
	})
	return urls, nil
}

// moodHeaders 说说接口使用的 Cookie / Referer，Referer 指到空间说说页。
func (c *Client) moodHeaders(targetUin string) map[string]string {
	return map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/mood", targetUin),
		"origin":     "https://user.qzone.qq.com",
	}
}

// moodAPIError 把空间返回码转成中文错误；登录失效和无权限单独提示。
func moodAPIError(code int64, res gjson.Result) error {
	msg := strings.TrimSpace(firstNonEmpty(res.Get("message").String(), res.Get("msg").String(), res.Get("subcode").String()))
	switch code {
	case -3000, -4001:
		return fmt.Errorf("登录已失效，请重新扫码 (code: %d)", code)
	case -4009, -10000:
		return fmt.Errorf("没有权限查看该空间的说说 (code: %d %s)", code, msg)
	default:
		if msg == "" {
			msg = "unknown"
		}
		return fmt.Errorf("说说接口错误 (code: %d): %s", code, msg)
	}
}

// parseCGIBody 兼容 JSONP 和裸 JSON。空间说说接口两种都会出现。
func parseCGIBody(content string) (string, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "\ufeff")
	if content == "" {
		return "", fmt.Errorf("empty response")
	}
	if strings.HasPrefix(content, "{") {
		return content, nil
	}
	return parseJSONP(content, "")
}
