/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-08
 * @FileName: board_test.go
 */

package qzone

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestParseMsgBoardPage(t *testing.T) {
	raw := `{
		"code": 0,
		"data": {
			"total": 3,
			"authorInfo": {"uin": 10000, "nickname": "小明", "sign": "欢迎来留言"},
			"commentList": [
				{"id": "11", "uin": 20001, "nickname": "好友A", "htmlContent": "你好", "pubtime": "2012-05-01 12:00"},
				{"id": "12", "uin": 20002, "nickname": "好友B", "ubbContent": "好久不见", "secret": 1}
			]
		}
	}`
	page := parseMsgBoardPage(raw, gjson.Parse(raw))
	if page.Total != 3 {
		t.Fatalf("total = %d", page.Total)
	}
	if page.Nickname != "小明" || page.Sign != "欢迎来留言" || page.SignUin != "10000" {
		t.Fatalf("author = %+v", page)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %d", len(page.Items))
	}
	if page.Items[0].Get("id").String() != "11" {
		t.Fatalf("first id = %s", page.Items[0].Get("id").String())
	}
}

func TestBoardAPIError(t *testing.T) {
	err := boardAPIError(-4009, gjson.Parse(`{"message":"无权访问"}`))
	if !IsBoardPermissionError(err) {
		t.Fatalf("want permission error, got %v", err)
	}
	err = boardAPIError(-3000, gjson.Parse(`{}`))
	if err == nil || !strings.Contains(err.Error(), "登录已失效") {
		t.Fatalf("login error = %v", err)
	}
}
