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

func TestParseMoodPost(t *testing.T) {
	raw := `{
		"tid": "abc123",
		"uin": 10001,
		"name": "小明",
		"content": "今天去了海边[em]e100[/em]",
		"created_time": 1520855040,
		"createTime": "2018年3月12日",
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
}

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

func TestWriteMoodViewer(t *testing.T) {
	root := t.TempDir()
	file := &MoodBackupFile{
		UIN:      "10001",
		Nickname: "小明",
		Posts: []MoodPost{{
			TID:      "abc",
			Time:     1520855040,
			TimeText: "2018-03-12 21:04",
			Content:  "hello",
			HTML:     "hello",
			Author:   MoodPerson{UIN: "10001", Name: "小明"},
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
	html, _ := os.ReadFile(index)
	if !strings.Contains(string(html), "posts-2018.js") {
		t.Fatal("index.html missing year script")
	}
}
