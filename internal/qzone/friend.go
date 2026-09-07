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
 * @FileName: friend.go
 * @Description: [好友列表：取出备注名，说说评论展示和空间网页一致]
 */

package qzone

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// GetFriendDisplayNames 返回「登录账号」视角的 QQ → 展示名。
// 说说评论/点赞接口只带对方昵称，空间网页会再用通讯录里的备注盖上去；
// 查看页要和网页一致，必须另拉这份表。键是好友 QQ，值优先备注，没有备注才用昵称。
func (c *Client) GetFriendDisplayNames(ctx context.Context) (map[string]string, error) {
	if c == nil {
		return map[string]string{}, nil
	}

	// 完整好友表（含 remark）。失败或为空时再退回「我在意谁」那份较短列表。
	names, err := c.getFriendNamesFromQQFriends(ctx)
	if err == nil && len(names) > 0 {
		return names, nil
	}

	items, listErr := c.GetFriendList(ctx)
	if listErr != nil {
		if err != nil {
			return nil, err
		}
		return nil, listErr
	}
	out := parseFriendDisplayNames(items)
	if len(out) == 0 && err != nil {
		return nil, err
	}
	return out, nil
}

// getFriendNamesFromQQFriends 走 friend_show_qqfriends.cgi，条目在 data.items。
// 每条一般有 uin、name（昵称）、remark（备注）；部分环境名单在 data.items_list。
func (c *Client) getFriendNamesFromQQFriends(ctx context.Context) (map[string]string, error) {
	apiURL := fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/r.qzone.qq.com/cgi-bin/tfriend/friend_show_qqfriends.cgi?uin=%s&follow_flag=1&groupface_flag=0&fupdate=1&g_tk=%s", c.QQ, c.GTK)
	headers := map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", c.QQ),
		"origin":     "https://user.qzone.qq.com",
	}

	cgiCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	start := time.Now()
	_, body, code, err := c.Http.Get(cgiCtx, apiURL, headers)
	bodyStr := string(body)
	c.logAPI("GetQQFriends", apiURL, headers, bodyStr, code, time.Since(start), err)
	if err != nil {
		return nil, fmt.Errorf("拉取好友列表失败: %w", err)
	}
	if code != 200 {
		return nil, fmt.Errorf("拉取好友列表失败: HTTP %d", code)
	}

	data, err := parseCGIBody(bodyStr)
	if err != nil {
		return nil, fmt.Errorf("解析好友列表失败: %w", err)
	}
	res := gjson.Parse(data)
	if apiCode := res.Get("code").Int(); apiCode != 0 {
		return nil, fmt.Errorf("好友列表接口错误 (code: %d): %s", apiCode, strings.TrimSpace(res.Get("message").String()))
	}

	items := res.Get("data.items")
	if !items.Exists() || len(items.Array()) == 0 {
		items = res.Get("data.items_list")
	}
	return parseFriendDisplayNames(items.Array()), nil
}

// parseFriendDisplayNames 从好友条目抽出展示名：remark / memo 优先，否则 name / nick。
// friend_ship_manager 那份列表往往没有 remark，name 本身就是备注。
func parseFriendDisplayNames(items []gjson.Result) map[string]string {
	out := make(map[string]string, len(items))
	for _, item := range items {
		uin := strings.TrimSpace(item.Get("uin").String())
		if uin == "" || uin == "0" {
			continue
		}
		remark := strings.TrimSpace(firstNonEmpty(item.Get("remark").String(), item.Get("memo").String()))
		nick := strings.TrimSpace(firstNonEmpty(item.Get("name").String(), item.Get("nick").String()))
		if remark != "" {
			out[uin] = remark
			continue
		}
		if nick != "" {
			out[uin] = nick
		}
	}
	return out
}
