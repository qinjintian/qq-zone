// 个人中心「全部动态」：信息中心 feeds 翻页，以及日志正文补拉。

package qzone

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	feedPageSize   = 10
	feedCGITimeout = 20 * time.Second
)

// FeedPageSize 是信息中心一页条数，和网页翻页一致。
func FeedPageSize() int { return feedPageSize }

// FeedCursor 是信息中心动态的翻页位置。第一页 BeginTime 留空即可。
type FeedCursor struct {
	BeginTime   string // 上一页最旧一条的时间，接口用它继续往前翻
	ExternParam string // 上一页带回的 externparam，没有就留空
	Page        int    // 从 1 开始的页码
}

// FeedPage 是 feeds3_html_more 一页的解析结果。
// 空间把动态卡片放在 HTML 里，有的环境还会再给一份结构化条目。
type FeedPage struct {
	Code         int64
	Message      string
	Nickname     string
	HasMore      bool
	HasMoreSet   bool // 响应里确实带了 hasMoreFeeds；没带时不能把它当成「没有更多」
	LastFeedTime string
	ExternParam  string
	HTML         string
	Items        []gjson.Result
	Raw          string
}

// BlogDetail 是一条日志的标题和正文 HTML。拉不到时备份仍保留动态卡片上的摘要。
type BlogDetail struct {
	Title string
	HTML  string
	Raw   string
}

// feedCGIContext 给信息中心和日志接口加上超时，避免单次请求一直不返回。
func feedCGIContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, feedCGITimeout)
}

// GetFeedPage 拉取个人中心一页「全部动态」。
// 只包含当前账号在信息中心还能翻到的内容，不是每位好友的完整历史。
func (c *Client) GetFeedPage(ctx context.Context, cursor FeedCursor) (*FeedPage, error) {
	if c == nil {
		return nil, fmt.Errorf("尚未登录空间")
	}
	page := cursor.Page
	if page < 1 {
		page = 1
	}
	begin := strings.TrimSpace(cursor.BeginTime)
	if begin == "" {
		begin = "0"
	}

	params := url.Values{}
	params.Set("uin", c.QQ)
	params.Set("scope", "0")
	params.Set("view", "1")
	params.Set("daylist", "")
	params.Set("uinlist", "")
	params.Set("gid", "")
	params.Set("flag", "1")
	params.Set("filter", "all")
	params.Set("applist", "all")
	params.Set("refresh", "0")
	params.Set("aisortEndTime", "0")
	params.Set("aisortOffset", "0")
	params.Set("getAisort", "0")
	params.Set("aisortBeginTime", "0")
	params.Set("pagenum", strconv.Itoa(page))
	params.Set("externparam", cursor.ExternParam)
	params.Set("firstGetGroup", "0")
	params.Set("icServerTime", "0")
	params.Set("mixnocache", "0")
	params.Set("scene", "0")
	params.Set("begintime", begin)
	params.Set("count", strconv.Itoa(feedPageSize))
	params.Set("dayspac", "0")
	params.Set("sidomain", "qzonestyle.gtimg.cn")
	params.Set("useutf8", "1")
	params.Set("outputhtmlfeed", "1")
	params.Set("rd", strconv.FormatInt(time.Now().UnixNano(), 10))
	params.Set("usertime", strconv.FormatInt(time.Now().Unix(), 10))
	params.Set("windowId", "0")
	params.Set("g_tk", c.GTK)
	params.Set("format", "jsonp")
	params.Set("inCharset", "utf-8")
	params.Set("outCharset", "utf-8")

	apiURL := "https://user.qzone.qq.com/proxy/domain/ic2.qzone.qq.com/cgi-bin/feeds/feeds3_html_more?" + params.Encode()
	headers := c.feedHeaders()

	cgiCtx, cancel := feedCGIContext(ctx)
	defer cancel()

	start := time.Now()
	_, body, code, err := c.Http.Get(cgiCtx, apiURL, headers)
	bodyStr := decodeCGIBytes(body)
	c.logAPI("GetFeedPage", apiURL, headers, bodyStr, code, time.Since(start), err)
	if err != nil {
		return nil, fmt.Errorf("拉取个人中心动态失败: %w", err)
	}
	if code != 200 {
		return nil, fmt.Errorf("拉取个人中心动态失败: HTTP %d", code)
	}
	return parseFeedBody(bodyStr)
}

// GetBlogDetail 补拉一条日志的标题和正文。动态卡片里通常只有摘要。
func (c *Client) GetBlogDetail(ctx context.Context, hostUin, blogID string) (*BlogDetail, error) {
	hostUin = strings.TrimSpace(hostUin)
	blogID = strings.TrimSpace(blogID)
	if c == nil {
		return nil, fmt.Errorf("尚未登录空间")
	}
	if hostUin == "" || blogID == "" {
		return nil, fmt.Errorf("缺少日志作者或日志 id")
	}

	hosts := []string{
		"https://user.qzone.qq.com/proxy/domain/b11.qzone.qq.com/cgi-bin/blognew/blog_output_data",
		"https://user.qzone.qq.com/proxy/domain/b.qzone.qq.com/cgi-bin/blognew/blog_output_data",
	}
	var lastErr error
	for _, host := range hosts {
		detail, err := c.getBlogDetailFrom(ctx, host, hostUin, blogID)
		if err == nil && (strings.TrimSpace(detail.HTML) != "" || strings.TrimSpace(detail.Title) != "") {
			return detail, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("日志正文为空")
	}
	return nil, lastErr
}

// getBlogDetailFrom 向指定日志接口拉一篇正文。个人中心和日志域名各试一次时会调用它。
func (c *Client) getBlogDetailFrom(ctx context.Context, apiURL, hostUin, blogID string) (*BlogDetail, error) {
	params := url.Values{}
	params.Set("uin", hostUin)
	params.Set("blogid", blogID)
	params.Set("styledm", "qzonestyle.gtimg.cn")
	params.Set("imgdm", "qzonestyle.gtimg.cn")
	params.Set("bdm", "b.qzone.qq.com")
	params.Set("mode", "2")
	params.Set("numperpage", "15")
	params.Set("inCharset", "utf-8")
	params.Set("outCharset", "utf-8")
	params.Set("ref", "qzone")
	params.Set("g_tk", c.GTK)
	params.Set("format", "jsonp")

	full := apiURL + "?" + params.Encode()
	headers := map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/blog/%s", hostUin, blogID),
		"origin":     "https://user.qzone.qq.com",
	}

	cgiCtx, cancel := feedCGIContext(ctx)
	defer cancel()

	start := time.Now()
	_, body, code, err := c.Http.Get(cgiCtx, full, headers)
	bodyStr := decodeCGIBytes(body)
	c.logAPI("GetBlogDetail", full, headers, bodyStr, code, time.Since(start), err)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("拉取日志失败: HTTP %d", code)
	}
	return parseBlogDetail(bodyStr)
}

// GetBlogComments 拉取一条日志的评论。没有评论时返回空列表，不算失败。
func (c *Client) GetBlogComments(ctx context.Context, hostUin, blogID string) ([]gjson.Result, error) {
	hostUin = strings.TrimSpace(hostUin)
	blogID = strings.TrimSpace(blogID)
	if c == nil {
		return nil, fmt.Errorf("尚未登录空间")
	}
	if hostUin == "" || blogID == "" {
		return nil, fmt.Errorf("缺少日志作者或日志 id")
	}

	params := url.Values{}
	params.Set("uin", hostUin)
	params.Set("blogid", blogID)
	params.Set("id", blogID)
	params.Set("start", "0")
	params.Set("num", "50")
	params.Set("inCharset", "utf-8")
	params.Set("outCharset", "utf-8")
	params.Set("format", "jsonp")
	params.Set("ref", "qzone")
	params.Set("g_tk", c.GTK)

	apiURL := "https://user.qzone.qq.com/proxy/domain/b11.qzone.qq.com/cgi-bin/blognew/get_comment_list?" + params.Encode()
	headers := map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/blog/%s", hostUin, blogID),
		"origin":     "https://user.qzone.qq.com",
	}

	cgiCtx, cancel := feedCGIContext(ctx)
	defer cancel()

	start := time.Now()
	_, body, code, err := c.Http.Get(cgiCtx, apiURL, headers)
	bodyStr := decodeCGIBytes(body)
	c.logAPI("GetBlogComments", apiURL, headers, bodyStr, code, time.Since(start), err)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("拉取日志评论失败: HTTP %d", code)
	}
	return parseBlogComments(bodyStr)
}

// feedHeaders 返回访问个人中心接口时要带的 Cookie 和来源页。
func (c *Client) feedHeaders() map[string]string {
	uin := ""
	if c != nil {
		uin = c.QQ
	}
	return map[string]string{
		"cookie":     c.Cookie,
		"user-agent": UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", uin),
		"origin":     "https://user.qzone.qq.com",
	}
}

// parseFeedBody 解析信息中心一页响应。JSONP、裸 JSON，以及夹在 data.main 里的 HTML 都认。
func parseFeedBody(body string) (*FeedPage, error) {
	data, err := parseCGIBody(body)
	if err != nil {
		return nil, fmt.Errorf("解析个人中心动态失败: %w", err)
	}
	res := gjson.Parse(data)
	page := &FeedPage{Raw: data}

	codeVal := res.Get("code")
	if !codeVal.Exists() {
		codeVal = res.Get("ret")
	}
	if !codeVal.Exists() {
		codeVal = res.Get("data.main.code")
	}
	page.Code = codeVal.Int()
	page.Message = strings.TrimSpace(firstNonEmpty(
		res.Get("message").String(),
		res.Get("msg").String(),
		res.Get("data.main.msg").String(),
		res.Get("data.main.message").String(),
	))
	if page.Code != 0 {
		return nil, feedAPIError(page.Code, page.Message)
	}
	mainCode := res.Get("data.main.code")
	if mainCode.Exists() && mainCode.Int() != 0 {
		msg := firstNonEmpty(res.Get("data.main.message").String(), res.Get("data.main.msg").String(), page.Message)
		return nil, feedAPIError(mainCode.Int(), msg)
	}

	page.Nickname = firstNonEmpty(res.Get("data.nickname").String(), res.Get("data.main.nickname").String(), res.Get("nickname").String())
	page.HasMore, page.HasMoreSet = jsonBool(res,
		"data.hasMoreFeeds",
		"data.main.hasMoreFeeds",
		"hasMoreFeeds",
		"data.hasmore",
		"data.main.hasMore",
	)
	page.LastFeedTime = firstJSONString(res,
		"data.lastFeedTime",
		"data.main.lastFeedTime",
		"lastFeedTime",
		"data.main.attach",
	)
	page.ExternParam = firstJSONString(res,
		"data.externparam",
		"data.main.externparam",
		"externparam",
		"data.externParam",
	)
	page.HTML = normalizeFeedHTML(firstJSONString(res,
		"html",
		"data.html",
		"data.main.html",
		"data.data.html",
		"data.feedhtml",
		"feedhtml",
	))
	if page.HTML == "" && res.Get("data").Type == gjson.String {
		page.HTML = normalizeFeedHTML(res.Get("data").String())
	}
	page.Items = feedItems(res)
	// 个人中心实际返回的 data 往往不是 JSON，而是一段 JS 对象，HTML 用 \x3C 转义。
	if page.HTML == "" && len(page.Items) == 0 {
		applyLooseFeed(body, page)
	}
	return page, nil
}

// parseBlogDetail 从日志接口响应里取出标题和正文 HTML。
func parseBlogDetail(body string) (*BlogDetail, error) {
	data, err := parseCGIBody(body)
	if err != nil {
		return nil, fmt.Errorf("解析日志失败: %w", err)
	}
	res := gjson.Parse(data)
	codeVal := res.Get("code")
	if !codeVal.Exists() {
		codeVal = res.Get("ret")
	}
	if codeVal.Exists() && codeVal.Int() != 0 {
		msg := firstNonEmpty(res.Get("message").String(), res.Get("msg").String())
		return nil, feedAPIError(codeVal.Int(), msg)
	}
	title := firstJSONString(res,
		"data.title",
		"data.blogtitle",
		"data.blog.title",
		"data.blog.blogtitle",
		"title",
	)
	rawHTML := firstJSONString(res,
		"data.html",
		"data.htmlContent",
		"data.blog.html",
		"data.blog.htmlContent",
		"data.blog.content",
		"data.content",
		"data.blog_content",
		"html",
	)
	if rawHTML == "" && strings.Contains(data, "blog") && !strings.HasPrefix(strings.TrimSpace(data), "{") {
		rawHTML = data
	}
	return &BlogDetail{Title: strings.TrimSpace(title), HTML: rawHTML, Raw: data}, nil
}

// parseBlogComments 从日志评论接口里取出评论列表。没有评论时返回空列表。
func parseBlogComments(body string) ([]gjson.Result, error) {
	data, err := parseCGIBody(body)
	if err != nil {
		return nil, fmt.Errorf("解析日志评论失败: %w", err)
	}
	res := gjson.Parse(data)
	codeVal := res.Get("code")
	if !codeVal.Exists() {
		codeVal = res.Get("ret")
	}
	if codeVal.Exists() && codeVal.Int() != 0 {
		msg := firstNonEmpty(res.Get("message").String(), res.Get("msg").String())
		return nil, feedAPIError(codeVal.Int(), msg)
	}
	for _, path := range []string{
		"data.comments",
		"data.commentList",
		"data.commentlist",
		"commentlist",
		"data.list",
		"data.replyList",
	} {
		arr := res.Get(path)
		if arr.IsArray() {
			return arr.Array(), nil
		}
	}
	return nil, nil
}

// feedAPIError 把信息中心和日志接口的错误码转成可读错误。登录失效会明确提示重新扫码。
func feedAPIError(code int64, msg string) error {
	msg = strings.TrimSpace(msg)
	switch code {
	case -3000, -4001, -87998:
		return fmt.Errorf("登录已失效，请重新扫码 (code: %d)", code)
	default:
		if msg == "" {
			msg = "unknown"
		}
		return fmt.Errorf("个人中心动态接口错误 (code: %d): %s", code, msg)
	}
}

// feedItems 从几种可能的字段里取出结构化动态列表。实际页面经常只有 HTML，这时返回空。
func feedItems(res gjson.Result) []gjson.Result {
	if res.Get("data").IsArray() && len(res.Get("data").Array()) > 0 {
		return res.Get("data").Array()
	}
	for _, path := range []string{
		"data.feeds",
		"data.feedslist",
		"data.main.feeds",
		"data.main.feedslist",
		"feeds",
		"newfeeds",
		"data.newfeeds",
	} {
		arr := res.Get(path)
		if arr.IsArray() && len(arr.Array()) > 0 {
			return arr.Array()
		}
	}
	return nil
}

// firstJSONString 按顺序返回第一个非空字符串字段。
func firstJSONString(res gjson.Result, paths ...string) string {
	for _, path := range paths {
		v := res.Get(path)
		if !v.Exists() || v.Type == gjson.Null {
			continue
		}
		s := strings.TrimSpace(v.String())
		if s != "" && s != "null" {
			return s
		}
	}
	return ""
}

// jsonBool 读取布尔字段。第二个返回值表示这个字段是否真的出现过。
func jsonBool(res gjson.Result, paths ...string) (bool, bool) {
	for _, path := range paths {
		v := res.Get(path)
		if !v.Exists() || v.Type == gjson.Null {
			continue
		}
		switch v.Type {
		case gjson.True:
			return true, true
		case gjson.False:
			return false, true
		case gjson.Number:
			return v.Int() != 0, true
		case gjson.String:
			s := strings.ToLower(strings.TrimSpace(v.String()))
			switch s {
			case "true", "1":
				return true, true
			case "false", "0":
				return false, true
			}
		}
	}
	return false, false
}

// normalizeFeedHTML 整理动态卡片 HTML。若整段仍是转义文本，先还原成真正的标签。
func normalizeFeedHTML(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// 有的响应把卡片 HTML 再转义了一层，页面上还看不到真正的标签。
	if strings.Contains(s, "&lt;") && !strings.Contains(s, "<") {
		s = html.UnescapeString(s)
	}
	return s
}

// decodeCGIBytes 把接口响应按 UTF-8 读取，不是 UTF-8 时再按 GBK 解码。
func decodeCGIBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) {
		return string(b)
	}
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(b)
	if err != nil || len(decoded) == 0 {
		return string(b)
	}
	return string(decoded)
}
