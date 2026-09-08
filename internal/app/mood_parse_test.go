/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-07
 * @FileName: mood_parse_test.go
 */

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// TestParseMoodPost 校验正文、@、表情、原图地址、转发和楼中楼是否按预期收进 MoodPost。
func TestParseMoodPost(t *testing.T) {
	raw := `{
		"tid": "abc123",
		"uin": 10001,
		"name": "小明",
		"content": "今天去了海边[em]e100[/em]",
		"created_time": 1520855040,
		"createTime": "2018年3月12日",
		"modifytime": 1520941440,
		"source_name": "手机QQ",
		"cmtnum": 2,
		"pictotal": 1,
		"has_more_con": 0,
		"lbs": {"name": "厦门·鼓浪屿"},
		"like": {"count": 3, "list": [{"uin": 10002, "nick": "小红"}]},
		"conlist": [
			{"type": 0, "con": "今天去了海边[em]e100[/em] "},
			{"type": 2, "uin": 10002, "nick": "小红"}
		],
		"pic": [{
			"pic_id": "p1",
			"url1": "http://example.com/s.jpg",
			"url3": "http://example.com/b&bo=xx",
			"origin_url": "http://example.com/origin.jpg"
		}],
		"commentlist": [{
			"uin": 10002,
			"name": "小红",
			"content": "好看",
			"create_time": 1520856000,
			"list_3": [{
				"uin": 10001,
				"name": "小明",
				"content": "谢谢",
				"create_time": 1520856100
			}]
		}],
		"rt_tid": "old456",
		"rt_uin": 10003,
		"rt_uinname": "阿强",
		"rt_con": {"content": "原说说正文"}
	}`

	post := parseMoodPost(gjson.Parse(raw), nil)
	if post.TID != "abc123" {
		t.Fatalf("tid = %s", post.TID)
	}
	if post.Author.UIN != "10001" || post.Author.Name != "小明" {
		t.Fatalf("author = %+v", post.Author)
	}
	if post.Location != "厦门·鼓浪屿" {
		t.Fatalf("location = %s", post.Location)
	}
	if post.LikeCount != 3 || len(post.Likes) != 1 {
		t.Fatalf("likes = %d %+v", post.LikeCount, post.Likes)
	}
	if !strings.Contains(post.Content, "@小红") {
		t.Fatalf("content = %s", post.Content)
	}
	if !strings.Contains(post.HTML, "qzonestyle.gtimg.cn/qzone/em/e100.gif") {
		t.Fatalf("html missing emote: %s", post.HTML)
	}
	if !strings.Contains(post.HTML, "mention") {
		t.Fatalf("html missing mention: %s", post.HTML)
	}
	if len(post.Media) != 1 || post.Media[0].Type != "image" {
		t.Fatalf("media = %+v", post.Media)
	}
	if post.Media[0].URL != "https://example.com/origin.jpg" {
		t.Fatalf("pic url = %s", post.Media[0].URL)
	}
	if post.Repost == nil || post.Repost.Author.Name != "阿强" {
		t.Fatalf("repost = %+v", post.Repost)
	}
	if len(post.Comments) != 1 || len(post.Comments[0].Replies) != 1 {
		t.Fatalf("comments = %+v", post.Comments)
	}
	if post.EditTime != 1520941440 || post.EditTimeText == "" {
		t.Fatalf("edit time = %d %s", post.EditTime, post.EditTimeText)
	}
}

func TestParseLikeCntField(t *testing.T) {
	item := gjson.Parse(`{"like":{"cnt":2,"list":[{"fuin":10002,"nick":"张三"},{"fuin":10003,"nick":"王五"}]}}`)
	if n := parseLikeCount(item); n != 2 {
		t.Fatalf("like cnt = %d", n)
	}
	likes := parseLikers(item)
	if len(likes) != 2 || likes[0].Name != "张三" || likes[1].Name != "王五" {
		t.Fatalf("likers = %+v", likes)
	}
}

// TestNormalizeMediaURL 校验 https 补全和预览参数改原图。
func TestNormalizeMediaURL(t *testing.T) {
	got := normalizeMediaURL("//qpic.cn/a.jpg")
	if got != "https://qpic.cn/a.jpg" {
		t.Fatalf("got %s", got)
	}
	got = normalizeMediaURL("http://x.com/pic?b&bo=1")
	if got != "https://x.com/pic?o&bo=1" {
		t.Fatalf("got %s", got)
	}
}

// TestWriteMoodViewer 确认会生成 index.html，且查看页 JS 里不含原始下载地址。
func TestWriteMoodViewer(t *testing.T) {
	root := t.TempDir()
	file := &MoodBackupFile{
		UIN:      "10001",
		Nickname: "小明",
		Posts: []MoodPost{{
			TID:       "abc",
			Time:      1520855040,
			TimeText:  "2018-03-12 21:04",
			Content:   "hello",
			HTML:      "hello",
			LikeCount: 3,
			Likes: []MoodPerson{
				{UIN: "10002", Name: "张三"},
				{UIN: "10003", Name: "李四"},
			},
			Author: MoodPerson{UIN: "10001", Name: "小明"},
			Media: []MoodMedia{{
				Type: "image",
				Path: "media/2018/03/a.jpg",
				URL:  "https://secret.example/a.jpg",
			}},
		}},
	}
	if err := writeMoodViewer(root, file); err != nil {
		t.Fatal(err)
	}

	index := filepath.Join(root, "index.html")
	if _, err := os.Stat(index); err != nil {
		t.Fatal(err)
	}
	js, err := os.ReadFile(filepath.Join(root, "data", "posts-2018.js"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), "secret.example") {
		t.Fatal("viewer js should not contain original download urls")
	}
	if !strings.Contains(string(js), "media/2018/03/a.jpg") {
		t.Fatal("viewer js missing local media path")
	}
	if !strings.Contains(string(js), `"like_count":3`) || !strings.Contains(string(js), "张三") {
		t.Fatal("viewer js missing like data")
	}
	html, _ := os.ReadFile(index)
	if !strings.Contains(string(html), "posts-2018.js") {
		t.Fatal("index.html missing year script")
	}
	css, err := os.ReadFile(filepath.Join(root, "assets", "viewer.css"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(css)
	if !strings.Contains(body, "video:fullscreen") || !strings.Contains(body, "video:-webkit-full-screen") {
		t.Fatal("viewer css missing fullscreen video rules")
	}
	if !strings.Contains(body, "object-fit: contain") {
		t.Fatal("fullscreen video should keep aspect ratio")
	}
}

func TestMoodHiddenFromViewer(t *testing.T) {
	own := gjson.Parse(`{"tid":"a","secret":1,"content":"私密"}`)
	if moodHiddenFromViewer(own, "10001", "10001") {
		t.Fatal("backing up own space should keep private moods")
	}

	secret := gjson.Parse(`{"tid":"b","secret":1,"content":""}`)
	if !moodHiddenFromViewer(secret, "10001", "707220871") {
		t.Fatal("friend secret mood should be skipped")
	}

	onlySelf := gjson.Parse(`{"tid":"c","ugc_right":64,"content":"x"}`)
	if !moodHiddenFromViewer(onlySelf, "10001", "707220871") {
		t.Fatal("ugc_right=64 should be skipped")
	}

	noTID := gjson.Parse(`{"tid":"","content":""}`)
	if !moodHiddenFromViewer(noTID, "10001", "707220871") {
		t.Fatal("empty tid should be skipped")
	}

	pub := gjson.Parse(`{"tid":"d","secret":0,"content":"公开"}`)
	if moodHiddenFromViewer(pub, "10001", "707220871") {
		t.Fatal("public mood should not be skipped")
	}
}

func TestParseMoodEditTimeIgnoredWhenUnchanged(t *testing.T) {
	item := gjson.Parse(`{"created_time":1520855040,"modifytime":1520855040}`)
	edited, text := parseMoodEditTime(item, 1520855040)
	if edited != 0 || text != "" {
		t.Fatalf("unchanged mood should have no edit time, got %d %q", edited, text)
	}
}

func TestParseType2PlainTextEmote(t *testing.T) {
	item := gjson.Parse(`{"tid":"t1","conlist":[{"type":2,"con":"#头顶一块布全球我最富[em]e120[/em]"}]}`)
	post := parseMoodPost(item, nil)
	if strings.HasPrefix(post.Content, "@") {
		t.Fatalf("plain type:2 text should not be @mention: %s", post.Content)
	}
	if strings.Contains(post.HTML, "[em]") {
		t.Fatalf("html still has emote code: %s", post.HTML)
	}
	if !strings.Contains(post.HTML, "qzonestyle.gtimg.cn/qzone/em/e120.gif") {
		t.Fatalf("html missing emote image: %s", post.HTML)
	}
	if strings.Contains(post.HTML, "mention") {
		t.Fatalf("plain text should not be mention: %s", post.HTML)
	}
}

func TestRepairFalseMentionEmote(t *testing.T) {
	p := repairMoodPost(MoodPost{
		Content: "@#头顶一块布全球我最富[em]e120[/em]",
		HTML:    `<span class="mention">@#头顶一块布全球我最富[em]e120[/em]</span>`,
	})
	if strings.HasPrefix(p.Content, "@") {
		t.Fatalf("content still prefixed: %s", p.Content)
	}
	if strings.Contains(p.HTML, "[em]") || strings.Contains(p.HTML, "mention") {
		t.Fatalf("repaired html = %s", p.HTML)
	}
	if !strings.Contains(p.HTML, "e120.gif") {
		t.Fatalf("repaired html missing gif: %s", p.HTML)
	}
}

func TestWriteMoodViewerRepairsEmote(t *testing.T) {
	root := t.TempDir()
	file := &MoodBackupFile{
		UIN:      "10001",
		Nickname: "nobody",
		Posts: []MoodPost{{
			TID:      "t1",
			Time:     1767294120,
			TimeText: "2026-01-02 03:02",
			Content:  "@#头顶一块布全球我最富[em]e120[/em]",
			HTML:     `<span class="mention">@#头顶一块布全球我最富[em]e120[/em]</span>`,
			Author:   MoodPerson{UIN: "10001", Name: "nobody"},
		}},
	}
	if err := writeMoodViewer(root, file); err != nil {
		t.Fatal(err)
	}
	js, err := os.ReadFile(filepath.Join(root, "data", "posts-2026.js"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(js)
	if !strings.Contains(body, "e120.gif") {
		t.Fatal("viewer js missing emote image")
	}
	if strings.Contains(body, "mention") {
		t.Fatal("viewer js still wraps the whole post as mention")
	}
}

func TestRewriteReplyAtUinUsesRemark(t *testing.T) {
	raw := `{"tid":"t1","commentlist":[{
		"uin":20001,
		"name":"阿明",
		"content":"所以去哪跨年？",
		"create_time":1762836900,
		"list_3":[{
			"uin":514092640,
			"name":"nobody",
			"content":"@{uin:20001,nick:阿明,who:1,auto:1}阿联酋[em]e10344[/em]",
			"create_time":1762843227
		}]
	}]}`
	post := parseMoodPost(gjson.Parse(raw), nil)
	applyFriendNamesToPosts([]MoodPost{post}, map[string]string{"20001": "老王"})
	if post.Comments[0].Author.Name != "老王" {
		t.Fatalf("comment name = %s", post.Comments[0].Author.Name)
	}
	reply := post.Comments[0].Replies[0]
	if strings.Contains(reply.HTML, "@{uin:") {
		t.Fatalf("raw at-uin still in html: %s", reply.HTML)
	}
	if !strings.Contains(reply.HTML, "老王") || !strings.Contains(reply.HTML, "回复") {
		t.Fatalf("reply html = %s", reply.HTML)
	}
	if strings.Contains(reply.HTML, "阿明") {
		t.Fatalf("nickname should be replaced: %s", reply.HTML)
	}
	if !strings.Contains(reply.HTML, "e10344.gif") {
		t.Fatalf("emote missing: %s", reply.HTML)
	}
}
