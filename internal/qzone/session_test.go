/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-20
 * @FileName: session_test.go
 */

package qzone

import (
	"path/filepath"
	"testing"
)

func TestQunCookieForQQPreservedAcrossSave(t *testing.T) {
	old := SessionPath
	SessionPath = filepath.Join(t.TempDir(), "sessions.json")
	t.Cleanup(func() { SessionPath = old })

	if got := qunCookieForQQ("514092640"); got != "" {
		t.Fatalf("empty store = %q", got)
	}
	if err := SaveSession(&Session{
		QQ:        "514092640",
		Nickname:  "nobody",
		Cookie:    "uin=o514092640; p_skey=zone",
		QunCookie: "uin=o514092640; p_skey=qun",
	}); err != nil {
		t.Fatal(err)
	}
	if got := qunCookieForQQ("514092640"); got != "uin=o514092640; p_skey=qun" {
		t.Fatalf("got %q", got)
	}
	if got := qunCookieForQQ("10000"); got != "" {
		t.Fatalf("other qq = %q", got)
	}
}

func TestNewClientFromSessionKeepsQunCookie(t *testing.T) {
	c := NewClientFromSession(&Session{
		QQ:        "514092640",
		Cookie:    "zone",
		QunCookie: "p_skey=qun",
	}, nil, nil)
	if !c.HasQunAuth() || c.QunCookie != "p_skey=qun" {
		t.Fatalf("client = %+v", c)
	}
}
