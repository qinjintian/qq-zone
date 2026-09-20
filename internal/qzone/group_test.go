/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-20
 * @FileName: group_test.go
 */

package qzone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestParseQunQzoneGroupList(t *testing.T) {
	raw := `{
		"code":0,
		"data":{"group":[
			{"groupid":123456,"groupname":"班级群"},
			{"groupid":"123456","groupname":"重复应丢掉"},
			{"groupid":987654321,"groupname":"技术交流"}
		]}
	}`
	got := parseQunQzoneGroupList(gjson.Parse(raw))
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].ID != "123456" || got[0].Name != "班级群" {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].ID != "987654321" {
		t.Fatalf("second = %+v", got[1])
	}
}

func TestParseQunMgrGroupListPrefersCreated(t *testing.T) {
	raw := `{
		"ec":0,
		"create":[{"gc":111,"gn":"我的群","owner":10001}],
		"manage":[{"gc":222,"gn":"管理的群","owner":10002}],
		"join":[
			{"gc":111,"gn":"我的群-重复"},
			{"gc":333,"gn":"加入的群","owner":10003}
		]
	}`
	got := parseQunMgrGroupList(gjson.Parse(raw))
	if len(got) != 3 {
		t.Fatalf("len = %d %+v", len(got), got)
	}
	if got[0].ID != "111" || got[0].Role != groupRoleCreated || got[0].RoleLabel() != "我创建的" {
		t.Fatalf("created = %+v", got[0])
	}
	if got[1].ID != "222" || got[1].RoleLabel() != "我管理的" {
		t.Fatalf("managed = %+v", got[1])
	}
	if got[2].ID != "333" || got[2].RoleLabel() != "我加入的" {
		t.Fatalf("joined = %+v", got[2])
	}
}

func TestNormalizeGroupAlbum(t *testing.T) {
	raw := `{"id":"album-1","title":"毕业照","photocnt":12,"desc":"2020"}`
	album := normalizeGroupAlbum(gjson.Parse(raw), "123456", "班级群")
	if album.Get("id").String() != "album-1" {
		t.Fatalf("id = %s", album.Get("id").String())
	}
	if album.Get("name").String() != "毕业照" {
		t.Fatalf("name = %s", album.Get("name").String())
	}
	if album.Get("total").Int() != 12 {
		t.Fatalf("total = %d", album.Get("total").Int())
	}
	if album.Get("allowAccess").Int() != 1 {
		t.Fatalf("allowAccess = %d", album.Get("allowAccess").Int())
	}
	if album.Get("_group_id").String() != "123456" {
		t.Fatalf("group = %s", album.Get("_group_id").String())
	}
}

func TestPickGroupPhotoURLPrefersOriginal(t *testing.T) {
	raw := `{
		"sloc":"ABC",
		"name":"操场",
		"uploadtime":"2018-06-01 12:00:00",
		"photourl":{
			"0":{"url":"http://gzc.photo.store.qq.com/orig.jpg","width":0,"height":0},
			"1":{"url":"https://gzc.photo.store.qq.com/small.jpg","width":200,"height":200},
			"5":{"url":"https://gzc.photo.store.qq.com/big.jpg","width":1600,"height":1200}
		}
	}`
	photo := gjson.Parse(raw)
	got := pickGroupPhotoURL(photo)
	if got != "https://gzc.photo.store.qq.com/orig.jpg" {
		t.Fatalf("origin = %s", got)
	}

	noOrig := gjson.Parse(`{"photourl":{
		"1":{"url":"https://a/small.jpg","width":200,"height":200},
		"5":{"url":"https://a/large.jpg","width":1600,"height":900,"enlarge_rate":1}
	}}`)
	if pickGroupPhotoURL(noOrig) != "https://a/large.jpg" {
		t.Fatalf("largest = %s", pickGroupPhotoURL(noOrig))
	}
}

func TestNormalizeGroupPhotoVideo(t *testing.T) {
	raw := `{
		"sloc":"VID1",
		"name":"晚会",
		"uploadtime":"2019-01-02 08:00:00",
		"photourl":{"0":{"url":"https://cover.jpg","width":0,"height":0}},
		"videodata":{"videostatus":2,"videoid":"abcde123456","actionurl":"http://group.video.store.qq.com/a.mp4","videourl":"https://play.qq.com/a.mp4"}
	}`
	photo := normalizeGroupPhoto(gjson.Parse(raw), "123456", "album-1")
	if !photo.Get("is_video").Bool() {
		t.Fatal("want video")
	}
	if photo.Get("raw").String() != "https://cover.jpg" {
		t.Fatalf("cover = %s", photo.Get("raw").String())
	}
	if photo.Get("video_url").String() != "https://group.video.store.qq.com/a.mp4" {
		t.Fatalf("video = %s", photo.Get("video_url").String())
	}
	if photo.Get("rawshoottime").String() != "2019-01-02 08:00:00" {
		t.Fatalf("time = %s", photo.Get("rawshoottime").String())
	}

	src := GroupVideoSource(photo)
	if len(src.Candidates) < 1 || src.Candidates[0].URL != "https://group.video.store.qq.com/a.mp4" {
		t.Fatalf("candidates = %+v", src.Candidates)
	}
	if src.VideoID != "abcde123456" {
		t.Fatalf("vid = %s", src.VideoID)
	}
}

func TestUnixToDateTime(t *testing.T) {
	if unixToDateTime(0) != "" {
		t.Fatal("zero")
	}
	s := unixToDateTime(1530000000)
	if !strings.HasPrefix(s, "2018-") {
		t.Fatalf("sec = %s", s)
	}
	ms := unixToDateTime(1530000000000)
	if ms != s {
		t.Fatalf("ms = %s sec = %s", ms, s)
	}
}

func TestIsValidGroupID(t *testing.T) {
	if !IsValidGroupID("123456") || !IsValidGroupID(" 987654321 ") {
		t.Fatal("want valid")
	}
	if IsValidGroupID("abc") || IsValidGroupID("12") || IsValidGroupID("") {
		t.Fatal("want invalid")
	}
}

func TestGroupAPIErrorPermission(t *testing.T) {
	err := groupAPIError(-100, gjson.Parse(`{"message":"对不起，您没有权限"}`))
	if err == nil || !strings.Contains(err.Error(), "无权访问") {
		t.Fatalf("err = %v", err)
	}
}

func TestIsQunAuthError(t *testing.T) {
	err := groupAPIError(4, gjson.Parse(`{"ec":4,"em":"no login"}`))
	if !IsQunAuthError(err) {
		t.Fatalf("want auth error, got %v", err)
	}
	if IsQunAuthError(nil) {
		t.Fatal("nil")
	}
	perm := groupAPIError(-100, gjson.Parse(`{"message":"对不起，您没有权限"}`))
	if IsQunAuthError(perm) {
		t.Fatalf("permission is not qun auth: %v", perm)
	}
}

func TestHasQunAuthNeedsPskey(t *testing.T) {
	if (&Client{QunCookie: "skey=abc; uin=o1"}).HasQunAuth() {
		t.Fatal("skey only should not count")
	}
	if !(&Client{QunCookie: "p_skey=qun; uin=o1"}).HasQunAuth() {
		t.Fatal("want p_skey")
	}
}

func TestGroupBknFromCookiePrefersSkey(t *testing.T) {
	skeyGTK := calculateGTK("skeyval")
	if got := groupBknFromCookie("skey=skeyval; p_skey=ps", "fallback"); got != skeyGTK {
		t.Fatalf("got %s want %s", got, skeyGTK)
	}
	if got := groupBknFromCookie("", "fallback"); got != "fallback" {
		t.Fatalf("fallback = %s", got)
	}
}

func TestPickGroupPhotoArray(t *testing.T) {
	res := gjson.Parse(`{"data":{"photolist":[{"sloc":"a"},{"sloc":"b"}]}}`)
	list := pickGroupPhotoArray(res)
	if len(list) != 2 {
		t.Fatalf("len = %d", len(list))
	}
}

func TestDecodeCGIJSONRejectsHTMLHomepage(t *testing.T) {
	html := `<!DOCTYPE html><html lang="zh-cn"><head><title>QQ空间-分享生活，留住感动</title></head><body><script>window.g_qzonetoken = (function(){ try{return "";} catch(e){}});</script></body></html>`
	if _, err := decodeCGIJSON(html); err == nil {
		t.Fatal("want html rejected")
	}
	if !cgiLooksLikeHTML(html) {
		t.Fatal("want html detected")
	}

	ok := `{"ec":0,"join":[{"gc":748162248,"gn":"班级群"}]}`
	res, err := decodeCGIJSON(ok)
	if err != nil {
		t.Fatal(err)
	}
	if res.Get("join.0.gc").Int() != 748162248 {
		t.Fatalf("gc = %s", res.Get("join.0.gc").String())
	}
}

func TestMergeGroupsDedupesSameIDFromLocalSources(t *testing.T) {
	got := MergeGroups(nil, []Group{
		{ID: "40146248", Name: "群40146248", Role: groupRoleRecent},
		{ID: "40146248", Role: groupRoleRecent},
	})
	if len(got) != 1 || got[0].ID != "40146248" {
		t.Fatalf("got %+v", got)
	}
}

func TestVisibleGroupsHidesLeftGroupsWhenListedAll(t *testing.T) {
	remote := []Group{{ID: "111222", Name: "还在", Role: groupRoleJoined}}
	local := []Group{
		{ID: "111222", Name: "旧名", Role: groupRoleRecent},
		{ID: "999888", Name: "已退的群", Role: groupRoleRecent},
	}

	got := VisibleGroups(remote, local, true)
	if len(got) != 1 || got[0].ID != "111222" || got[0].Name != "还在" {
		t.Fatalf("listedAll = %+v", got)
	}

	got = VisibleGroups(remote, local, false)
	if len(got) != 2 {
		t.Fatalf("fallback len = %d %+v", len(got), got)
	}
	if got[1].ID != "999888" {
		t.Fatalf("fallback = %+v", got)
	}
}

func TestMergeGroupsKeepsRemoteRole(t *testing.T) {
	remote := []Group{{ID: "111222", Name: "远程名", Role: groupRoleCreated}}
	local := []Group{{ID: "111222", Name: "群111222", Role: groupRoleRecent}, {ID: "222333", Name: "本机群", Role: groupRoleRecent}}
	got := MergeGroups(remote, local)
	if len(got) != 2 {
		t.Fatalf("len = %d %+v", len(got), got)
	}
	if got[0].ID != "111222" || got[0].Role != groupRoleCreated || got[0].Name != "远程名" {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].ID != "222333" || got[1].RoleLabel() != "用过的" {
		t.Fatalf("second = %+v", got[1])
	}
}

func TestRememberAndLoadKnownGroups(t *testing.T) {
	old := userStorageRoot
	userStorageRoot = t.TempDir()
	t.Cleanup(func() { userStorageRoot = old })

	if err := RememberGroup("10001", Group{ID: "748162248", Name: "17年的回忆"}); err != nil {
		t.Fatal(err)
	}
	qunDir := filepath.Join(userStorageRoot, "10001", "qun", "999888777")
	if err := os.MkdirAll(qunDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got := LoadKnownGroups("10001")
	if len(got) != 2 {
		t.Fatalf("len = %d %+v", len(got), got)
	}
	byID := map[string]Group{}
	for _, g := range got {
		byID[g.ID] = g
	}
	if byID["748162248"].Name != "17年的回忆" {
		t.Fatalf("remembered = %+v", byID["748162248"])
	}
	if _, ok := byID["999888777"]; !ok {
		t.Fatalf("missing dir group %+v", got)
	}
}
