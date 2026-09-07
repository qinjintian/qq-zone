/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-07
 * @FileName: mood_like_test.go
 */

package qzone

import "testing"

func TestTidFromMoodUnikey(t *testing.T) {
	got := tidFromMoodUnikey("http://user.qzone.qq.com/514092640/mood/606ea41e7f3e8c69cf150400")
	if got != "606ea41e7f3e8c69cf150400" {
		t.Fatalf("tid = %s", got)
	}
	got = tidFromMoodUnikey("http://user.qzone.qq.com/514092640/mood/abc.1")
	if got != "abc" {
		t.Fatalf("tid with .1 = %s", got)
	}
}

func TestParseMoodLikeCounts(t *testing.T) {
	raw := `{
		"code":0,
		"data":[
			{"unikey":"http://user.qzone.qq.com/514092640/mood/abc","current":{"likedata":{"cnt":3}}},
			{"unikey":"http://user.qzone.qq.com/514092640/mood/abc.1","current":{"likedata":{"cnt":3}}},
			{"unikey":"http://user.qzone.qq.com/514092640/mood/def","current":{"likedata":{"cnt":0}}}
		]
	}`
	got, err := parseMoodLikeCounts(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["abc"].Like != 3 || got["def"].Like != 0 {
		t.Fatalf("counts = %#v", got)
	}
}

func TestParseMoodLikeCountsJSONPAndIndexFallback(t *testing.T) {
	raw := `_Callback({"code":0,"data":[{"current":{"likedata":{"cnt":2}}}]});`
	keys := []string{"http://user.qzone.qq.com/1/mood/tid1"}
	got, err := parseMoodLikeCounts(raw, keys)
	if err != nil {
		t.Fatal(err)
	}
	if got["tid1"].Like != 2 {
		t.Fatalf("counts = %#v", got)
	}
}

func TestParseMoodVisitCount(t *testing.T) {
	// newdata.PRD 是说说浏览次数；LIKE 是赞数的另一种字段。
	raw := `{"code":0,"data":[{"unikey":"http://user.qzone.qq.com/1/mood/tid1.1","current":{"likedata":{"cnt":10},"newdata":{"PRD":413,"LIKE":10}}}]}`
	got, err := parseMoodLikeCounts(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["tid1"].Like != 10 || got["tid1"].Visit != 413 {
		t.Fatalf("opcnt = %+v", got["tid1"])
	}
}

func TestParseMoodLikeCountsSingleObjectUsesCurrentKey(t *testing.T) {
	// 用 | 拼 unikey 时，接口常把 data 收成单个对象，且 unikey 在 current.key。
	// 不能按 requested 下标把这一条数套到后面的 tid 上。
	raw := `{"code":0,"data":{"current":{"key":"http://user.qzone.qq.com/1/mood/onlyme.1","likedata":{"cnt":5},"newdata":{"PRD":9}}}}`
	keys := []string{
		"http://user.qzone.qq.com/1/mood/onlyme.1",
		"http://user.qzone.qq.com/1/mood/other.1",
	}
	got, err := parseMoodLikeCounts(raw, keys)
	if err != nil {
		t.Fatal(err)
	}
	if got["onlyme"].Like != 5 || got["onlyme"].Visit != 9 {
		t.Fatalf("onlyme = %+v", got["onlyme"])
	}
	if _, ok := got["other"]; ok {
		t.Fatalf("other should be missing, got %#v", got)
	}
}

func TestParseMoodLikeCountsCurrentKeyAndList(t *testing.T) {
	raw := `{
		"code":0,
		"data":[
			{"current":{"key":"http://user.qzone.qq.com/1/mood/aaa","likedata":{"cnt":0},"newdata":{"PRD":138}}},
			{"current":{"key":"http://user.qzone.qq.com/1/mood/bbb","likedata":{"cnt":4,"list":[[10002,"张三",0],[10003,"李四",0]]},"newdata":{"PRD":197,"LIKE":4}}}
		]
	}`
	got, err := parseMoodLikeCounts(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["aaa"].Visit != 138 || got["aaa"].Like != 0 {
		t.Fatalf("aaa = %+v", got["aaa"])
	}
	if got["bbb"].Like != 4 || got["bbb"].Visit != 197 || len(got["bbb"].Likers) != 2 || got["bbb"].Likers[0].UIN != "10002" {
		t.Fatalf("bbb = %+v", got["bbb"])
	}
}

func TestParseMoodLikeList(t *testing.T) {
	raw := `_Callback({"code":0,"data":{"total_number":3,"like_uin_info":[
		{"fuin":10002,"nick":"张三"},
		{"fuin":10003,"nick":"李四"},
		{"fuin":10004,"nick":"王五"}
	]}});`
	likers, total, err := parseMoodLikeList(raw)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(likers) != 3 || likers[0].Name != "张三" || likers[1].UIN != "10003" {
		t.Fatalf("likers=%+v total=%d", likers, total)
	}
}
