package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestParseFeedHTMLCards(t *testing.T) {
	html := `
<div class="f-single f-s-s" id="feed_k1" data-key="k1" data-uin="10001" data-tid="t1" data-appid="311" data-abstime="1696723200" data-nickname="小明">
  <div class="f-aside"><a class="f-user-avatar"><img src="https://qlogo4.store.qq.com/qzone/10001/10001/50"></a></div>
  <div class="f-wrap">
    <div class="f-nick"><a class="f-name" href="https://user.qzone.qq.com/10001">小明</a><span class="f-nick-extra">发表说说</span></div>
    <div class="f-info"><span class="state">2023年10月8日 08:00</span><a class="source">手机QQ</a></div>
    <div class="f-ct">
      <pre class="content">今天真好<img src="https://qzonestyle.gtimg.cn/qzone/em/e100.gif"></pre>
      <div class="img-box"><img src="https://a.qpic.cn/preview.jpg" data-origin="https://a.qpic.cn/origin.jpg"></div>
    </div>
    <div class="comments-list">
      <div class="comments-item" data-uin="20001">
        <a class="nickname" href="https://user.qzone.qq.com/20001">评论者</a>
        <span class="comment-content">说得好</span>
      </div>
    </div>
    <div class="f-op-wrap"><a class="item">赞(2)</a><a class="item">评论(1)</a></div>
  </div>
</div>
<div class="f-single" data-key="k2" data-uin="10002" data-appid="2" data-abstime="1696636800">
  <div class="f-nick"><a class="f-name" href="https://user.qzone.qq.com/10002">小红</a><span class="f-action">写了日志</span></div>
  <div class="f-ct">
    <a class="f-title" href="https://user.qzone.qq.com/10002/blog/123456789">周末记事</a>
    <div class="f-ct-txt">只是一段摘要</div>
  </div>
</div>
<div class="f-single" data-key="k3" data-uin="10003" data-appid="4" data-abstime="1696550400">
  <div class="f-nick"><a class="f-name">小刚</a><span class="f-action">上传了照片</span></div>
  <div class="img-box">
    <img data-origin="https://b.qpic.cn/1.jpg">
    <img data-origin="https://b.qpic.cn/2.jpg">
  </div>
</div>
<div class="f-single" data-key="k4" data-uin="10001" data-appid="311" data-abstime="1600000000">
  <div class="f-nick"><a class="f-name">小明</a><span class="f-action">转发说说</span></div>
  <div class="f-ct"><pre class="content">转发一句</pre>
    <div class="f-repost f-single" data-uin="10009">
      <a class="f-name">原作者</a>
      <pre class="content">原来的话</pre>
      <img data-origin="https://c.qpic.cn/rt.jpg">
    </div>
  </div>
</div>`

	now := time.Date(2026, 10, 8, 15, 0, 0, 0, shanghaiLoc)
	posts := parseFeedHTML(html, now)
	if len(posts) != 4 {
		t.Fatalf("posts=%d", len(posts))
	}

	mood := posts[0]
	if mood.TID != "k1" || mood.OriginTID != "t1" || mood.Author.Name != "小明" || mood.Author.UIN != "10001" {
		t.Fatalf("mood identity: %+v", mood)
	}
	if mood.FeedType != "shuoshuo" || mood.FeedLabel != "说说" {
		t.Fatalf("type %s %s", mood.FeedType, mood.FeedLabel)
	}
	if mood.Time != 1696723200 {
		t.Fatalf("time %d", mood.Time)
	}
	if !strings.Contains(mood.Content, "今天真好") || !strings.Contains(mood.Content, "[em]e100[/em]") {
		t.Fatalf("content %q", mood.Content)
	}
	if len(mood.Media) != 1 || mood.Media[0].URL != "https://a.qpic.cn/origin.jpg" {
		t.Fatalf("media %+v", mood.Media)
	}
	if !mood.LikeKnown || mood.LikeCount != 2 || !mood.CommentKnown || mood.CommentCount != 1 {
		t.Fatalf("counts like=%d cmt=%d known %v %v", mood.LikeCount, mood.CommentCount, mood.LikeKnown, mood.CommentKnown)
	}
	if len(mood.Comments) != 1 || mood.Comments[0].Author.Name != "评论者" || mood.Comments[0].Content != "说得好" {
		t.Fatalf("comments %+v", mood.Comments)
	}
	if strings.Contains(mood.Content, "说得好") {
		t.Fatalf("comment leaked into content: %q", mood.Content)
	}

	blog := posts[1]
	if blog.FeedType != "blog" || blog.Title != "周末记事" || blog.BlogID != "123456789" || blog.Author.Name != "小红" {
		t.Fatalf("blog %+v", blog)
	}
	if !strings.Contains(blog.Content, "摘要") {
		t.Fatalf("blog content %q", blog.Content)
	}

	photo := posts[2]
	if photo.FeedType != "photo" || len(photo.Media) != 2 {
		t.Fatalf("photo %+v", photo)
	}

	repost := posts[3]
	if repost.FeedType != "repost" || repost.Content != "转发一句" || repost.Repost == nil {
		t.Fatalf("repost %+v", repost)
	}
	if repost.Repost.Author.Name != "原作者" || repost.Repost.Content != "原来的话" || len(repost.Repost.Media) != 1 {
		t.Fatalf("inner %+v", repost.Repost)
	}
	if len(repost.Media) != 0 {
		t.Fatalf("repost image leaked %+v", repost.Media)
	}
}

func TestParseFeedCardFromInfoCenter(t *testing.T) {
	html := `
<li class="f-single f-s-s" id="fct_10001_311_0_1700001111_0_1">
  <div class="f-single-head f-aside">
    <div class="user-pto"><a class="user-avatar q_namecard" href="http://user.qzone.qq.com/10001"><img src="https://qlogo1.store.qq.com/qzone/10001/10001/50"></a></div>
    <div class="user-info"><div class="f-nick"><a class="f-name q_namecard" href="http://user.qzone.qq.com/10001">小明</a></div>
      <div class="info-detail"><span class="state">14:21</span></div>
    </div>
  </div>
  <div class="f-single-content f-wrap">
    <div class="f-item f-s-i" id="feed_10001_311_0_1700001111_0_1" data-key="tid1">
      <div class="f-info">今天真好</div>
      <div class="qz_summary">
        <i class="none" name="feed_data" data-tid="tid1" data-uin="10001" data-fkey="tid1" data-abstime="1700001111"></i>
        <div class="f-ct"><div class="txt-box">配一句正文</div>
          <div class="img-box"><img src="https://a.qpic.cn/preview.jpg" data-origin="https://a.qpic.cn/origin.jpg"></div>
          <a class="f-video-wrap" data-v_vidiourl="https://vwecam.tc.qq.com/a.mp4" data-v_picinfo_url="https://a.qpic.cn/poster.jpg" data-v_itemid="vid9"></a>
        </div>
      </div>
    </div>
  </div>
  <div class="f-single-foot">
    <a class="item qz_like_btn_v3" data-likecnt="3" data-unikey="http://user.qzone.qq.com/10001/mood/tid1"></a>
  </div>
</li>`
	now := time.Date(2026, 10, 8, 15, 0, 0, 0, shanghaiLoc)
	posts := parseFeedHTML(html, now)
	if len(posts) != 1 {
		t.Fatalf("posts=%d", len(posts))
	}
	post := posts[0]
	if post.TID != "tid1" || post.OriginTID != "tid1" || post.Author.UIN != "10001" || post.Author.Name != "小明" {
		t.Fatalf("identity %+v", post)
	}
	if post.FeedType != "shuoshuo" || post.Time != 1700001111 {
		t.Fatalf("type %s time %d", post.FeedType, post.Time)
	}
	if !strings.Contains(post.Content, "配一句正文") {
		t.Fatalf("content %q", post.Content)
	}
	var image, video *MoodMedia
	for i := range post.Media {
		switch post.Media[i].Type {
		case "image":
			image = &post.Media[i]
		case "video":
			video = &post.Media[i]
		}
	}
	if image == nil || image.URL != "https://a.qpic.cn/origin.jpg" || video == nil || video.URL != "https://vwecam.tc.qq.com/a.mp4" || video.Poster != "https://a.qpic.cn/poster.jpg" {
		t.Fatalf("media %+v", post.Media)
	}
	if !post.LikeKnown || post.LikeCount != 3 || post.Unikey == "" {
		t.Fatalf("like %d known %v unikey %q", post.LikeCount, post.LikeKnown, post.Unikey)
	}
}

func TestLargerQzonePhotoURL(t *testing.T) {
	raw := "https://a1.qpic.cn/psc?/abc/def/c&ek=1&kp=1&pt=0&bo=QQ&sce=60-2-2&rf=0-0-0"
	got := largerQzonePhotoURLs(raw)
	if len(got) != 3 {
		t.Fatalf("urls %v", got)
	}
	if !strings.Contains(got[0], "/o&ek=") || strings.Contains(got[0], "sce=") {
		t.Fatalf("origin %s", got[0])
	}
	if !strings.Contains(got[1], "/b&ek=") || strings.Contains(got[1], "sce=") {
		t.Fatalf("big %s", got[1])
	}
	if !strings.Contains(got[2], "/c&ek=") || !strings.Contains(got[2], "sce=60-2-2") {
		t.Fatalf("thumb %s", got[2])
	}
	kept := largerQzonePhotoURLs("https://m.qpic.cn/psc?/abc/def/o&bo=QQ")
	if len(kept) != 1 || !strings.Contains(kept[0], "/o&bo=") {
		t.Fatalf("kept %v", kept)
	}
}

func TestFeedCardUsesLargerPhoto(t *testing.T) {
	html := `
<div class="f-single" data-key="kimg" data-uin="10001" data-appid="311" data-abstime="1700002222">
  <a class="f-name" href="http://user.qzone.qq.com/10001">小明</a>
  <div class="txt-box">有图</div>
  <div class="img-box">
    <a href="https://h5.qzone.qq.com/page/photo?pre=https%3A%2F%2Fa1.qpic.cn%2Fpsc%3F%2Fabc%2Fdef%2Fm%26ek%3D1%26sce%3D60-3-3">
      <img src="https://a1.qpic.cn/psc?/abc/def/c&amp;ek=1&amp;sce=60-2-2">
    </a>
  </div>
</div>`
	posts := parseFeedHTML(html, time.Date(2026, 10, 8, 15, 0, 0, 0, shanghaiLoc))
	if len(posts) != 1 || len(posts[0].Media) != 1 {
		t.Fatalf("posts %+v", posts)
	}
	urls := posts[0].Media[0].URLs
	if len(urls) < 3 {
		t.Fatalf("candidates %v", urls)
	}
	if !strings.Contains(urls[0], "/o&ek=") || strings.Contains(urls[0], "sce=") {
		t.Fatalf("origin %v", urls)
	}
	if !strings.Contains(urls[1], "/b&ek=") || strings.Contains(urls[1], "sce=") {
		t.Fatalf("hd %v", urls)
	}
	last := urls[len(urls)-1]
	if !strings.Contains(last, "/c&ek=") || !strings.Contains(last, "sce=60-2-2") {
		t.Fatalf("thumb should stay last %v", urls)
	}
}

func TestSkipFeedAdvertisement(t *testing.T) {
	html := `
<li class="f-single f-s-s f-single-biz" id="fct_0_6600_4_1700001111_0_1" data-complete_adinfo="%7B%7D" data-key="advertisement_outlink_0">
  <a class="f-name">WorkBuddy</a>
  <div class="f-info">查看详情</div>
  <img data-origin="https://pgdt.gtimg.cn/a.jpg">
</li>
<li class="f-single f-s-s" id="fct_10001_311_0_1700002222_0_1" data-key="tid1" data-uin="10001" data-appid="311" data-abstime="1700002222">
  <a class="f-name" href="http://user.qzone.qq.com/10001">小明</a>
  <div class="txt-box">今天真好</div>
</li>`
	posts := parseFeedHTML(html, time.Date(2026, 10, 8, 15, 0, 0, 0, shanghaiLoc))
	if len(posts) != 1 || posts[0].TID != "tid1" || posts[0].Author.Name != "小明" {
		t.Fatalf("posts %+v", posts)
	}
	if _, ok := parseFeedJSONItem(gjson.Parse(`{"appid":"6600","key":"advertisement_outlink_1","uin":"0","nickname":"欧派整装官方","summary":"立即咨询"}`), time.Now()); ok {
		t.Fatal("json ad was kept")
	}
}

func TestParseFeedMonthDay(t *testing.T) {
	now := time.Date(2026, 10, 8, 15, 0, 0, 0, shanghaiLoc)
	got := parseFeedClock("", "10月8日 08:35", now)
	want := time.Date(2026, 10, 8, 8, 35, 0, 0, shanghaiLoc).Unix()
	if got != want {
		t.Fatalf("got %d want %d", got, want)
	}
}

func TestAdaptFeedComments(t *testing.T) {
	raw := gjson.Parse(`[{"uin":"9","nick":"甲","content":"你好","create_time":1700000000,"replyList":[{"uin":"8","nick":"乙","content":"回复"}]}]`).Array()
	comments := adaptFeedComments(raw)
	if len(comments) != 1 || comments[0].Content != "你好" || comments[0].Author.Name != "甲" {
		t.Fatalf("%+v", comments)
	}
	if len(comments[0].Replies) != 1 || comments[0].Replies[0].Content != "回复" {
		t.Fatalf("replies %+v", comments[0].Replies)
	}
}

func TestWriteFeedViewer(t *testing.T) {
	dir := t.TempDir()
	file := &FeedBackupFile{
		UIN:      "10001",
		Nickname: "小明",
		Notice:   "测试提示",
		Posts: []MoodPost{{
			TID:       "k1",
			FeedType:  "blog",
			FeedLabel: "日志",
			Title:     "周末",
			Author:    MoodPerson{UIN: "10002", Name: "小红"},
			Time:      1696636800,
			Content:   "正文",
			HTML:      "正文",
		}},
	}
	if err := writeFeedViewer(dir, file); err != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile(filepath.Join(dir, "data", "meta.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(meta), `"kind":"feed"`) || !strings.Contains(string(meta), "测试提示") {
		t.Fatalf("meta %s", meta)
	}
}
