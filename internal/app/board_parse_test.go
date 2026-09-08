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
	post := parseBoardMessage(gjson.Parse(raw), 0)
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
	post := parseBoardMessage(gjson.Parse(raw), 11)
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
	if post.Floor != 11 {
		t.Fatalf("floor = %d", post.Floor)
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
	post := parseBoardMessage(gjson.Parse(raw), 0)
	if post.Floor != 11 {
		t.Fatalf("floor from id = %d", post.Floor)
	}
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
			Floor:    11,
			Time:     1520855040,
			TimeText: "2018-03-12 21:04",
			Content:  "你好",
			HTML:     `你好<img src="/qzone/em/e182.gif" />`,
			Secret:   true,
			Author:   MoodPerson{UIN: "20001", Name: "好友"},
			Comments: []MoodComment{{
				ID:       "r1",
				Content:  "收到",
				HTML:     "收到",
				TimeText: "2018-03-12 22:00",
				Author:   MoodPerson{UIN: "10001", Name: "小明"},
			}},
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
	if !strings.Contains(string(js), `"floor":11`) {
		t.Fatal("viewer js missing floor")
	}
	if !strings.Contains(string(js), "收到") {
		t.Fatal("viewer js missing reply")
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

func TestParseBoardMessageAPIReplyAndFloorFallback(t *testing.T) {
	raw := `{
		"id": "1000050011",
		"uin": 10002,
		"nickname": "张三",
		"htmlContent": "今天天气不错",
		"pubtime": "2026-09-08 13:00:23",
		"replyList": [{
			"content": "@{uin:10002,nick:张三} 收到了",
			"uin": 10001,
			"time": 1788871055,
			"nick": "李四"
		}]
	}`
	post := parseBoardMessage(gjson.Parse(raw), 11)
	if post.Floor != 11 {
		t.Fatalf("floor = %d", post.Floor)
	}
	if len(post.Comments) != 1 {
		t.Fatalf("comments = %+v", post.Comments)
	}
	c := post.Comments[0]
	if c.Author.UIN != "10001" || c.Author.Name != "李四" {
		t.Fatalf("reply author = %+v", c.Author)
	}
	if !strings.Contains(c.Content, "收到了") {
		t.Fatalf("reply content = %q", c.Content)
	}
	if strings.Contains(c.HTML, "@{uin:") {
		t.Fatalf("raw at-uin still in html: %s", c.HTML)
	}
	if c.Time != 1788871055 {
		t.Fatalf("reply time = %d", c.Time)
	}
	if want := formatMoodTime(1788871055, ""); c.TimeText != want {
		t.Fatalf("reply time_text = %q want %q", c.TimeText, want)
	}

	posts := []MoodPost{post}
	applyFriendNamesToPosts(posts, map[string]string{
		"10001": "王五",
		"10002": "老张",
	})
	if posts[0].Author.Name != "老张" {
		t.Fatalf("board owner remark = %q", posts[0].Author.Name)
	}
	if posts[0].Comments[0].Author.Name != "王五" {
		t.Fatalf("reply remark = %q", posts[0].Comments[0].Author.Name)
	}
	if !strings.Contains(posts[0].Comments[0].HTML, "老张") {
		t.Fatalf("at-mention should use remark: %s", posts[0].Comments[0].HTML)
	}
}

func TestParseBoardFloorPrefersExplicitThenFallback(t *testing.T) {
	item := gjson.Parse(`{"id":"1000050003","floor":7}`)
	if n := parseBoardFloor(item, "1000050003", 11); n != 7 {
		t.Fatalf("explicit floor = %d", n)
	}
	item = gjson.Parse(`{"id":"abc"}`)
	if n := parseBoardFloor(item, "abc", 4); n != 4 {
		t.Fatalf("fallback floor = %d", n)
	}
}

func TestMergeMoodPostsKeepsNewerReplies(t *testing.T) {
	older := []MoodPost{{
		TID:     "11",
		Time:    100,
		Content: "旧正文",
		Media:   []MoodMedia{{ID: "pic1", Type: "image", Path: "media/a.jpg", URL: "https://example/a.jpg"}},
	}}
	newer := []MoodPost{{
		TID:     "11",
		Time:    100,
		Floor:   11,
		Content: "旧正文",
		Media:   []MoodMedia{{ID: "pic1", Type: "image", URL: "https://example/a.jpg"}},
		Comments: []MoodComment{{
			ID:      "10001-1788871055",
			Content: "新回复",
			Author:  MoodPerson{UIN: "10001", Name: "王五"},
		}},
	}}
	merged := mergeMoodPosts(newer, older)
	if len(merged) != 1 {
		t.Fatalf("len = %d", len(merged))
	}
	got := keepBoardMedia(merged[0], older[0])
	if got.Floor != 11 {
		t.Fatalf("floor = %d", got.Floor)
	}
	if len(got.Comments) != 1 || got.Comments[0].Content != "新回复" {
		t.Fatalf("comments = %+v", got.Comments)
	}
	if got.Media[0].Path != "media/a.jpg" {
		t.Fatalf("local media path lost: %+v", got.Media)
	}
}

func TestBoardViewerAssetsShowFloorAndReplies(t *testing.T) {
	js, err := os.ReadFile(filepath.Join("viewer", "assets", "viewer.js"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(js)
	if !strings.Contains(body, "第") || !strings.Contains(body, "p.floor") {
		t.Fatal("viewer.js should render 第N楼")
	}
	if !strings.Contains(body, "isBoard ? comments") {
		t.Fatal("viewer.js should expand all board replies")
	}
	css, err := os.ReadFile(filepath.Join("viewer", "assets", "viewer.css"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".floor") {
		t.Fatal("viewer.css missing .floor")
	}
}
