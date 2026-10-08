package qzone

import (
	"strings"
	"testing"
)

func TestParseFeedBodyHTMLInMain(t *testing.T) {
	body := `_Callback({"code":0,"data":{"nickname":"小明","main":{"hasMoreFeeds":true,"lastFeedTime":1690000000,"externparam":"cursor-1","html":"&lt;div class=\"f-single\" data-key=\"k9\"&gt;hi&lt;/div&gt;"}}});`
	page, err := parseFeedBody(body)
	if err != nil {
		t.Fatal(err)
	}
	if page.Nickname != "小明" {
		t.Fatalf("nickname: %q", page.Nickname)
	}
	if !page.HasMore || !page.HasMoreSet {
		t.Fatalf("hasMore: %+v", page)
	}
	if page.LastFeedTime != "1690000000" {
		t.Fatalf("lastFeedTime: %q", page.LastFeedTime)
	}
	if page.ExternParam != "cursor-1" {
		t.Fatalf("extern: %q", page.ExternParam)
	}
	if !strings.Contains(page.HTML, `class="f-single"`) || strings.Contains(page.HTML, "&lt;") {
		t.Fatalf("html not unescaped: %q", page.HTML)
	}
}

func TestParseFeedBodyLoginError(t *testing.T) {
	_, err := parseFeedBody(`{"code":-3000,"message":"login"}`)
	if err == nil || !strings.Contains(err.Error(), "重新扫码") {
		t.Fatalf("got %v", err)
	}
}

func TestParseBlogDetail(t *testing.T) {
	body := `shine_Callback({"code":0,"data":{"title":"周末记事","html":"<p>正文<img src=\"https://a.qpic.cn/a.jpg\"></p>"}});`
	detail, err := parseBlogDetail(body)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Title != "周末记事" {
		t.Fatalf("title %q", detail.Title)
	}
	if !strings.Contains(detail.HTML, "a.qpic.cn") {
		t.Fatalf("html %q", detail.HTML)
	}
}

func TestParseLooseFeedBody(t *testing.T) {
	body := `{
		"code":0,
		"subcode":0,
		"message":"",
		"default":0,
		"data":
{main:{hasMoreFeeds:true,begintime:'1700000000',externparam:'basetime=1700000000&pagenum=1'},data:[{appid:'311',abstime:'1700001111',html:'\x3Cli class=\x22f-single f-s-s\x22 id=\x22fct_10001_311_0_1700001111_0_1\x22>\x3Cdiv class=\x22f-info\x22>\xE4\xBB\x8A\xE5\xA4\xA9\xE7\x9C\x9F\xE5\xA5\xBD\x3C\/div>\x3Ci class=\x22none\x22 name=\x22feed_data\x22 data-tid=\x22tid1\x22 data-uin=\x2210001\x22 data-fkey=\x22tid1\x22>\x3C\/i>\x3C\/li>'}]}
}`
	page, err := parseFeedBody(body)
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || !page.HasMoreSet {
		t.Fatalf("hasMore %+v", page)
	}
	if page.ExternParam != "basetime=1700000000&pagenum=1" {
		t.Fatalf("extern %q", page.ExternParam)
	}
	if page.LastFeedTime != "1700000000" {
		t.Fatalf("begin %q", page.LastFeedTime)
	}
	if !strings.Contains(page.HTML, `class="f-single f-s-s"`) || strings.Contains(page.HTML, `\x3C`) {
		t.Fatalf("html %q", page.HTML)
	}
	if !strings.Contains(page.HTML, "今天真好") {
		t.Fatalf("text %q", page.HTML)
	}
}

func TestParseFeedBodyItems(t *testing.T) {
	body := `{"code":0,"data":{"feeds":[{"uin":"10001","nickname":"小明","appid":311,"key":"abc","abstime":1700000000,"summary":"一条说说"}]}}`
	page, err := parseFeedBody(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items %d", len(page.Items))
	}
	if !page.HasMoreSet && page.HasMore {
		t.Fatal("missing hasMore should not default to true")
	}
}
