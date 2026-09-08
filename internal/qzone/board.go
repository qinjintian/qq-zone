/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-08
 * @FileName: board.go
 * @Description: [QQ 空间留言板接口：分页拉取留言、寄语和回复]
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

const (
	boardPageSize   = 20               // get_msgb 单页上限就是 20，再大接口会截断
	boardCGITimeout = 20 * time.Second // 无权查看时接口有时会挂住，单次请求加超时
)

// boardCGIContext 给留言板请求加上超时，避免一条不可见空间把整次备份卡住。
func boardCGIContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, boardCGITimeout)
}

// MsgBoardPage 是 get_msgb 一页的解析结果。
type MsgBoardPage struct {
	Total    int            // data.total，空间侧声明的留言总数
	Nickname string         // 空间主人昵称，来自 authorInfo
	Sign     string         // 主人寄语（留言板顶部那句），只在部分页返回
	SignUin  string         // 寄语对应的主人 QQ
	Items    []gjson.Result // 本页 commentList
	Raw      string         // 本页原始 JSON，可选落盘
}

// GetMsgBoard 拉取一页留言。start 按 20 递增，不要按返回条数加。
func (c *Client) GetMsgBoard(ctx context.Context, targetUin string, start int) (*MsgBoardPage, error) {
	loginUin := strings.TrimSpace(c.QQ)
	if loginUin == "" {
		loginUin = targetUin
	}

	params := url.Values{}
	params.Set("uin", loginUin)
	params.Set("hostUin", targetUin)
	params.Set("start", fmt.Sprintf("%d", start))
	params.Set("num", fmt.Sprintf("%d", boardPageSize))
	params.Set("format", "jsonp")
	params.Set("inCharset", "utf-8")
	params.Set("outCharset", "utf-8")
	params.Set("ref", "qzone")
	params.Set("g_tk", c.GTK)
	// hostword=0：不要只拉主人自己的话；不传 essence，避免只拿到精华留言。
	params.Set("hostword", "0")
	params.Set("iNotice", "0")

	apiURL := "https://user.qzone.qq.com/proxy/domain/m.qzone.qq.com/cgi-bin/new/get_msgb?" + params.Encode()
	headers := c.boardHeaders(targetUin)

	cgiCtx, cancel := boardCGIContext(ctx)
	defer cancel()

	startAt := time.Now()
	_, body, code, err := c.Http.Get(cgiCtx, apiURL, headers)
	bodyStr := string(body)
	c.logAPI("GetMsgBoard", apiURL, headers, bodyStr, code, time.Since(startAt), err)

	if err != nil {
		return nil, fmt.Errorf("拉取留言板失败: %w", err)
	}
	if code != 200 {
		return nil, fmt.Errorf("拉取留言板失败: HTTP %d", code)
	}

	data, err := parseCGIBody(bodyStr)
	if err != nil {
		return nil, fmt.Errorf("解析留言板失败: %w", err)
	}

	res := gjson.Parse(data)
	if apiCode := res.Get("code").Int(); apiCode != 0 {
		return nil, boardAPIError(apiCode, res)
	}

	page := parseMsgBoardPage(data, res)
	return page, nil
}

// parseMsgBoardPage 从已解析的 JSON 收成 MsgBoardPage，测试可直接调用。
func parseMsgBoardPage(raw string, res gjson.Result) *MsgBoardPage {
	data := res.Get("data")
	if !data.Exists() {
		data = res
	}

	page := &MsgBoardPage{
		Total:    int(firstBoardInt(data, "total", "totalNum", "msgNum")),
		Nickname: firstBoardString(data, "authorInfo.nickname", "authorInfo.nick", "nickname"),
		Sign:     firstBoardString(data, "authorInfo.sign", "authorInfo.message", "sign"),
		SignUin:  firstBoardString(data, "authorInfo.uin", "authorInfo.hostUin"),
		Raw:      raw,
	}

	list := data.Get("commentList")
	if !list.Exists() || list.Type == gjson.Null {
		list = data.Get("items")
	}
	if list.Exists() && list.Type != gjson.Null {
		page.Items = list.Array()
	}
	return page
}

// BoardPageSize 返回留言板每页请求条数，备份循环按这个步长增加 start。
func BoardPageSize() int {
	return boardPageSize
}

// boardHeaders 留言板接口使用的 Cookie / Referer，Referer 指到空间留言板页。
func (c *Client) boardHeaders(targetUin string) map[string]string {
	return map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s", targetUin),
		"origin":     "https://user.qzone.qq.com",
	}
}

// boardAPIError 把空间返回码转成中文错误；登录失效和无权限单独提示。
func boardAPIError(code int64, res gjson.Result) error {
	msg := strings.TrimSpace(firstNonEmpty(res.Get("message").String(), res.Get("msg").String()))
	switch code {
	case -3000, -4001, -87998:
		return fmt.Errorf("登录已失效，请重新扫码 (code: %d)", code)
	case -4009, -10000, -3001, -3002:
		return fmt.Errorf("没有权限查看该留言板 (code: %d %s)", code, msg)
	default:
		if msg == "" {
			msg = "unknown"
		}
		return fmt.Errorf("留言板接口错误 (code: %d): %s", code, msg)
	}
}

// IsBoardPermissionError 判断是否为「当前账号看不到这块留言板」，备份时应提示而不是当任务崩溃。
func IsBoardPermissionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "没有权限") ||
		strings.Contains(msg, "无权") ||
		strings.Contains(msg, "不可见")
}

// firstBoardString 按候选路径取第一个非空字符串，兼容 nickname/nick 这类字段名差异。
func firstBoardString(v gjson.Result, keys ...string) string {
	for _, key := range keys {
		s := strings.TrimSpace(v.Get(key).String())
		if s != "" && s != "null" {
			return s
		}
	}
	return ""
}

// firstBoardInt 按候选路径取第一个存在的整数，例如 total / totalNum。
func firstBoardInt(v gjson.Result, keys ...string) int64 {
	for _, key := range keys {
		n := v.Get(key)
		if n.Exists() && n.Type != gjson.Null {
			return n.Int()
		}
	}
	return 0
}
