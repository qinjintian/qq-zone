/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-07
 * @FileName: friend_test.go
 */

package qzone

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestParseFriendDisplayNamesPrefersRemark(t *testing.T) {
	raw := `{"code":0,"data":{"items":[
		{"uin":20001,"name":"阿明","remark":"老王"},
		{"uin":10002,"name":"小红","remark":""}
	]}}`
	got := parseFriendDisplayNames(gjson.Parse(raw).Get("data.items").Array())
	if got["20001"] != "老王" {
		t.Fatalf("remark = %q", got["20001"])
	}
	if got["10002"] != "小红" {
		t.Fatalf("nick fallback = %q", got["10002"])
	}
}
