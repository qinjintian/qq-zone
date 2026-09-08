/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-08
 * @FileName: board_parse_test.go
 */

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestParseBoardMessageSecret(t *testing.T) {
	raw := `{
		"id": "99",
		"uin": 20001,
		"nickname": "访客",
		"htmlContent": "不该出现的正文",
		"ubbContent": "不该出现的 UBB",
		"secret": 1,
		"pubtime": "2012-05-01 12:00"
	}`
	post := parseBoardMessage(gjson.Parse(raw))
	if !post.Secret {
		t.Fatal("want secret")
	}
	if post.TID != "99" {
		t.Fatalf("tid = %s", post.TID)
	}
	if post.Content != boardSecretPlaceholder {
		t.Fatalf("content leaked: %s", post.Content)
	}
	if strings.Contains(post.HTML, "不该出现") {
		t.Fatalf("html leaked: %s", post.HTML)
	}
	if len(post.Media) != 0 {
		t.Fatalf("secret should have no media: %+v", post.Media)
	}
	if post.Time <= 1e9 {
		t.Fatalf("time = %d", post.Time)
	}
}

func TestParseBoardMessageImageAndReply(t *testing.T) {
	raw := `{
		"id": "11",
		"uin": 10001,
		"nickname": "小明",
		"htmlContent": "好久不见<img src=\"https://qzonestyle.gtimg.cn/qzone/em/e100.gif\"><img src=\"https://b.example/pic.jpg\">",
		"pubtime": "2018-03-12 21:04:00",
		"replyList": [
			{
				"id": "r1",
				"uin": 10002,
				"nickname": "好友",
				"ubbContent": "收到",
				"pubtime": "2018-03-12 22:00"
			}
		]
	}`
	post := parseBoardMessage(gjson.Parse(raw))
	if post.TID != "11" || post.Author.Name != "小明" {
		t.Fatalf("post = %+v", post)
	}
	if !strings.Contains(post.HTML, "好久不见") {
		t.Fatalf("html = %s", post.HTML)
	}
	if strings.Contains(post.HTML, "b.example/pic.jpg") {
		t.Fatalf("remote pic should be stripped: %s", post.HTML)
	}
	if !strings.Contains(post.HTML, "qzonestyle.gtimg.cn/qzone/em/e100.gif") {
		t.Fatalf("emote should stay: %s", post.HTML)
	}
	if len(post.Media) != 1 || !strings.Contains(post.Media[0].URL, "b.example/pic.jpg") {
		t.Fatalf("media = %+v", post.Media)
	}
	if len(post.Comments) != 1 || post.Comments[0].Content != "收到" {
		t.Fatalf("comments = %+v", post.Comments)
	}
}

func TestParseBoardMessageEmoteNotMedia(t *testing.T) {
	raw := `{
		"id": "1000050011",
		"uin": 531517643,
		"nickname": "访客",
		"bmp": "18d195a001008101",
		"htmlContent": "就像今天。<img src=\"/qzone/em/e182.gif\"  /><wbr  />",
		"ubbContent": "就像今天。[em]e182[/em]",
		"pubtime": "2026-09-08 13:00:23"
	}`
	post := parseBoardMessage(gjson.Parse(raw))
	if len(post.Media) != 0 {
		t.Fatalf("bmp/emote should not be media: %+v", post.Media)
	}
	if !strings.Contains(post.HTML, "https://qzonestyle.gtimg.cn/qzone/em/e182.gif") {
		t.Fatalf("emote should use official CDN: %s", post.HTML)
	}
	if strings.Contains(post.HTML, `src="/qzone/em/`) {
		t.Fatalf("relative emote path should be rewritten: %s", post.HTML)
	}
	if !strings.Contains(post.HTML, `class="emote"`) {
		t.Fatalf("emote img missing class: %s", post.HTML)
	}
}

func TestRepairBoardPostDropsBmpID(t *testing.T) {
	post := repairBoardPost(MoodPost{
		HTML:  `今天<img src="/qzone/em/e144.gif"  />`,
		Media: []MoodMedia{{Type: "image", URL: "18d195a001008101"}},
	})
	if len(post.Media) != 0 {
		t.Fatalf("hex bmp id should be dropped: %+v", post.Media)
	}
	if !strings.Contains(post.HTML, "qzonestyle.gtimg.cn/qzone/em/e144.gif") {
		t.Fatalf("html = %s", post.HTML)
	}
}

func TestParseBoardTimeText(t *testing.T) {
	n := parseBoardTimeText("2012年5月1日 12:00")
	if n <= 1e9 {
		t.Fatalf("chinese date = %d", n)
	}
	n2 := parseBoardTimeText("1335859200")
	if n2 != 1335859200 {
		t.Fatalf("unix = %d", n2)
	}
}

func TestWriteBoardViewer(t *testing.T) {
	root := t.TempDir()
	file := &BoardBackupFile{
		UIN:      "10001",
		Nickname: "小明",
		Notice:   "没有权限查看该留言板，已跳过。",
		Intro: &BoardIntro{
			Author:  MoodPerson{UIN: "10001", Name: "小明"},
			Content: "欢迎来留言",
			HTML:    "欢迎来留言",
		},
		Posts: []MoodPost{{
			TID:      "11",
			Time:     1520855040,
			TimeText: "2018-03-12 21:04",
			Content:  "你好",
			HTML:     `你好<img src="/qzone/em/e182.gif" />`,
			Secret:   true,
			Author:   MoodPerson{UIN: "20001", Name: "好友"},
			Media: []MoodMedia{{
				Type: "image",
				Path: "media/2018/03/a.jpg",
				URL:  "https://secret.example/a.jpg",
			}},
		}},
	}
	if err := writeBoardViewer(root, file); err != nil {
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
	if !strings.Contains(string(js), `"secret":true`) {
		t.Fatal("viewer js missing secret flag")
	}
	if !strings.Contains(string(js), "qzonestyle.gtimg.cn/qzone/em/e182.gif") {
		t.Fatal("viewer js should rewrite relative emote to official CDN")
	}
	meta, err := os.ReadFile(filepath.Join(root, "data", "meta.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(meta), `"kind":"board"`) {
		t.Fatalf("meta missing kind: %s", meta)
	}
	if !strings.Contains(string(meta), "欢迎来留言") {
		t.Fatalf("meta missing intro: %s", meta)
	}
	html, _ := os.ReadFile(index)
	if !strings.Contains(string(html), "的留言备份") {
		t.Fatalf("index title = %s", html[:120])
	}
}

func TestIsBoardTask(t *testing.T) {
	if IsBoardTask(nil) {
		t.Fatal("nil should be false")
	}
	if !IsBoardTask(&TaskRecord{Mode: TaskModeBoard}) {
		t.Fatal("mode board")
	}
	retry := &TaskRecord{
		Mode: TaskModeRetryFailed,
		OpenFailedItems: []FailedItem{{
			Kind: FailedKindBoard,
		}},
	}
	if !IsBoardTask(retry) {
		t.Fatal("retry kind board")
	}
	if IsBoardTask(&TaskRecord{Mode: TaskModeShuoShuo}) {
		t.Fatal("shuoshuo should not match")
	}
}
