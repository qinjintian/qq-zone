// 群空间扫码登录参数单元测试

package qzone

import (
	"strings"
	"testing"
)

func TestQunQRTargetUsesGroupConsole(t *testing.T) {
	got := qunQRTarget()
	if got.appID != "715030901" || got.daid != "73" {
		t.Fatalf("appid/daid = %s/%s", got.appID, got.daid)
	}
	if !strings.Contains(got.jumpURL, "qun.qq.com") {
		t.Fatalf("jump = %s", got.jumpURL)
	}
	if !strings.Contains(got.xloginURL, "715030901") || !strings.Contains(got.xloginURL, "daid=73") {
		t.Fatalf("xlogin = %s", got.xloginURL)
	}
	if !strings.Contains(got.qrHint, "不是重新登录") {
		t.Fatalf("hint = %s", got.qrHint)
	}
}

func TestQzoneQRTargetUnchanged(t *testing.T) {
	got := qzoneQRTarget()
	if got.appID != "549000912" || got.daid != "5" {
		t.Fatalf("appid/daid = %s/%s", got.appID, got.daid)
	}
	if !strings.Contains(got.jumpURL, "qzone") {
		t.Fatalf("jump = %s", got.jumpURL)
	}
}

func TestQQFromCookie(t *testing.T) {
	if got := qqFromCookie("uin=o0514092640; p_skey=x"); got != "514092640" {
		t.Fatalf("uin = %q", got)
	}
	if got := qqFromCookie("p_uin=o10001; skey=a"); got != "10001" {
		t.Fatalf("p_uin = %q", got)
	}
	if got := qqFromCookie("skey=a"); got != "" {
		t.Fatalf("empty = %q", got)
	}
}
