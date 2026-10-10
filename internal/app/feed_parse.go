// 把个人中心动态的 HTML 卡片收成查看页用的说说结构。

package app

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/tidwall/gjson"
	xhtml "golang.org/x/net/html"
)

var (
	feedUINPattern      = regexp.MustCompile(`(?i)user\.qzone\.qq\.com/(\d+)`)
	feedBlogIDPattern   = regexp.MustCompile(`(?i)(?:/blog/|blogid=)(\d{4,})`)
	feedEmotePattern    = regexp.MustCompile(`(?i)(?:/em/|/qzone/em/)e(\d+)\.gif`)
	feedLikePattern     = regexp.MustCompile(`赞\s*[（(]\s*(\d+)\s*[)）]`)
	feedCommentPattern  = regexp.MustCompile(`评论\s*[（(]\s*(\d+)\s*[)）]`)
	feedMonthDayPattern = regexp.MustCompile(`(\d{1,2})月(\d{1,2})日(?:\s*(\d{1,2}):(\d{2}))?`)
	feedClockPattern    = regexp.MustCompile(`(\d{1,2}):(\d{2})`)
)

// postsFromFeedPage 把一页动态收成帖子。HTML 卡片和结构化条目会按 tid 去重。
func postsFromFeedPage(pageHTML string, items []gjson.Result, now time.Time) []MoodPost {
	var posts []MoodPost
	seen := map[string]bool{}
	add := func(list []MoodPost) {
		for _, post := range list {
			if strings.TrimSpace(post.Content) == "" && strings.TrimSpace(post.Title) == "" && len(post.Media) == 0 && post.Repost == nil {
				continue
			}
			if post.TID != "" {
				if seen[post.TID] {
					continue
				}
				seen[post.TID] = true
			}
			posts = append(posts, post)
		}
	}
	if strings.TrimSpace(pageHTML) != "" {
		add(parseFeedHTML(pageHTML, now))
	}
	for _, item := range items {
		if h := strings.TrimSpace(item.Get("html").String()); h != "" {
			add(parseFeedHTML(h, now))
			continue
		}
		if post, ok := parseFeedJSONItem(item, now); ok {
			add([]MoodPost{post})
		}
	}
	return posts
}

// parseFeedHTML 从一页 HTML 里取出好友动态卡片。广告卡片在这里就丢掉。
func parseFeedHTML(fragment string, now time.Time) []MoodPost {
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	root, err := xhtml.Parse(strings.NewReader(fragment))
	if err != nil {
		return nil
	}
	var cards []*xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n != nil && n.Type == xhtml.ElementNode && hasClass(n, "f-single") {
			if !isFeedAdCard(n) {
				cards = append(cards, n)
			}
			return
		}
		if n == nil {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)

	posts := make([]MoodPost, 0, len(cards))
	for _, card := range cards {
		if post, ok := parseFeedCard(card, now); ok {
			posts = append(posts, post)
		}
	}
	return posts
}

// parseFeedCard 把一张动态卡片收成帖子，包含作者、正文、时间、赞评和配图。
func parseFeedCard(card *xhtml.Node, now time.Time) (MoodPost, bool) {
	appid := strings.TrimSpace(attr(card, "data-appid"))
	uin := strings.TrimSpace(attr(card, "data-uin"))
	tid := strings.TrimSpace(attr(card, "data-tid"))
	key := strings.TrimSpace(firstAttr(card, "data-key", "data-feedkey"))
	name := strings.TrimSpace(attr(card, "data-nickname"))
	abstime := strings.TrimSpace(firstAttr(card, "data-abstime", "data-time"))
	fillFeedCardMeta(card, &appid, &uin, &tid, &key, &abstime)
	if isFeedAdvertisement(appid, tid, key, attr(card, "id")) || isFeedAdCard(card) {
		return MoodPost{}, false
	}

	nameLink := findFirst(card, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "a" && hasClass(n, "f-name") && !inside(n, card, func(p *xhtml.Node) bool {
			return p != card && (hasClass(p, "f-repost") || hasClass(p, "f-single"))
		})
	})
	if nameLink != nil {
		if name == "" {
			name = cleanText(nodeText(nameLink, nil))
		}
		if uin == "" {
			uin = firstUIN(attr(nameLink, "href"))
		}
	}
	if uin == "" {
		uin = firstUIN(nodeHrefBlob(card))
	}

	action := cleanText(textOfClass(card, "f-nick-extra", "f-action"))
	action = strings.Trim(action, " :：")
	source := cleanText(textOfClass(card, "source"))
	title := cleanText(textOfClass(card, "f-title", "blog-title", "txt-box-title"))
	blogID := blogIDFromNode(card)

	contentRoot := findContentRoot(card)
	content := cleanText(nodeText(contentRoot, func(n *xhtml.Node) bool {
		return skipFeedChrome(n) || isFeedCommentBox(n) || hasClass(n, "f-title") || hasClass(n, "blog-title") || hasClass(n, "img-box") || hasClass(n, "f-ct-imgbox") || hasClass(n, "f-share") || hasClass(n, "share-box")
	}))
	if title != "" && content == title {
		content = ""
	}
	if content == "" {
		if info := findFirst(card, func(n *xhtml.Node) bool {
			return hasClass(n, "f-info") && !inside(n, card, func(p *xhtml.Node) bool {
				return p != card && (hasClass(p, "f-repost") || hasClass(p, "f-single"))
			})
		}); info != nil {
			content = cleanText(nodeText(info, func(n *xhtml.Node) bool {
				return hasClass(n, "state") || hasClass(n, "source") || hasClass(n, "f-sign-show")
			}))
		}
	}

	shareTitle, shareURL := shareFromCard(card)
	if title == "" && shareTitle != "" && (appid == "202" || strings.Contains(action, "分享")) {
		title = shareTitle
	}

	repostNode := findFirst(card, func(n *xhtml.Node) bool {
		if n == card || n.Type != xhtml.ElementNode {
			return false
		}
		return hasClass(n, "f-repost") || hasClass(n, "f-forward") || hasClass(n, "f-single")
	})
	var repost *MoodRepost
	if repostNode != nil {
		repost = parseFeedRepost(repostNode, now)
	}

	// 赞/评论数只从操作栏取，避免正文里的「赞」被当成计数。
	opOnly := textOfClass(card, "f-op", "f-op-wrap")

	post := MoodPost{
		TID:          feedStableID(appid, uin, tid, key, name, content, title, abstime),
		OriginTID:    tid,
		Unikey:       firstAttr(card, "data-unikey", "data-likeunikey"),
		Author:       MoodPerson{UIN: uin, Name: name},
		Time:         parseFeedClock(abstime, "", now),
		Content:      content,
		Source:       source,
		ShareTitle:   shareTitle,
		ShareURL:     shareURL,
		Title:        title,
		BlogID:       blogID,
		Action:       action,
		Media:        feedImages(card, repostNode),
		Repost:       repost,
		Comments:     parseFeedHTMLComments(card, repostNode),
		LikeCount:    firstInt(feedLikePattern, opOnly),
		CommentCount: firstInt(feedCommentPattern, opOnly),
	}
	if feedLikePattern.MatchString(opOnly) {
		post.LikeKnown = true
	}
	if feedCommentPattern.MatchString(opOnly) {
		post.CommentKnown = true
	}
	if n, ok := attrNonNeg(card, "data-likecnt", "data-likenum", "data-like"); ok {
		post.LikeCount = n
		post.LikeKnown = true
	}
	if !post.LikeKnown {
		if v := findFeedAttr(card, "data-likecnt", "data-likenum"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				post.LikeCount = n
				post.LikeKnown = true
			}
		}
	}
	if post.Unikey == "" {
		post.Unikey = findFeedAttr(card, "data-unikey", "data-likeunikey")
	}
	if n, ok := attrNonNeg(card, "data-cmtnum", "data-cmtcount", "data-commentnum"); ok {
		post.CommentCount = n
		post.CommentKnown = true
	}
	if len(post.Comments) > post.CommentCount {
		post.CommentCount = len(post.Comments)
	}
	if post.Time <= 0 {
		post.Time = parseFeedClock("", textOfClass(card, "state", "f-time", "f-info"), now)
	}
	post.TimeText = formatFeedTime(post.Time)
	post.FeedType, post.FeedLabel = classifyFeed(appid, action, blogID != "")
	post.HTML = moodTextToHTML(post.Content)
	if post.Author.Name == "" && post.Author.UIN != "" {
		post.Author.Name = post.Author.UIN
	}
	if post.Repost != nil && post.Repost.Author.Name == "" && post.Repost.Author.UIN != "" {
		post.Repost.Author.Name = post.Repost.Author.UIN
	}
	if post.Author.Name == "" && post.Content == "" && post.Title == "" && len(post.Media) == 0 && post.Repost == nil {
		return MoodPost{}, false
	}
	return post, true
}

// fillFeedCardMeta 从卡片内部补齐编号、作者、应用类型和发表时间。这些值经常不在最外层标签上。
func fillFeedCardMeta(card *xhtml.Node, appid, uin, tid, key, abstime *string) {
	if card == nil {
		return
	}
	if *tid == "" {
		*tid = findFeedAttr(card, "data-tid")
	}
	if *key == "" {
		*key = findFeedAttr(card, "data-key", "data-fkey", "data-feedkey")
	}
	if *uin == "" {
		*uin = findFeedAttr(card, "data-uin")
	}
	if *appid == "" {
		*appid = findFeedAttr(card, "data-appid")
	}
	if *abstime == "" {
		*abstime = findFeedAttr(card, "data-abstime", "data-time")
	}
	idUIN, idApp, idTime := splitFeedDOMID(attr(card, "id"))
	if idTime == "" && idUIN == "" {
		idUIN, idApp, idTime = splitFeedDOMID(findFeedAttr(card, "id"))
	}
	if *uin == "" {
		*uin = idUIN
	}
	if *appid == "" {
		*appid = idApp
	}
	if *abstime == "" {
		*abstime = idTime
	}
}

// findFeedAttr 在当前卡片里查找属性，不进入转发原文和评论区，避免拿到别人的编号。
func findFeedAttr(card *xhtml.Node, keys ...string) string {
	var found string
	var walk func(*xhtml.Node, bool)
	walk = func(n *xhtml.Node, nested bool) {
		if found != "" || n == nil {
			return
		}
		if n != card && n.Type == xhtml.ElementNode && (hasClass(n, "f-repost") || hasClass(n, "f-single") || isFeedCommentBox(n)) {
			nested = true
		}
		if !nested && n.Type == xhtml.ElementNode {
			for _, key := range keys {
				if v := strings.TrimSpace(attr(n, key)); v != "" {
					found = v
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, nested)
		}
	}
	walk(card, false)
	return found
}

// splitFeedDOMID 从 fct_QQ_应用_类型_时间 这类节点 id 里取出作者、应用类型和发表时间。
func splitFeedDOMID(id string) (uin, appid, abstime string) {
	parts := strings.Split(strings.TrimSpace(id), "_")
	if len(parts) < 5 {
		return "", "", ""
	}
	switch parts[0] {
	case "fct", "feed", "hex":
	default:
		return "", "", ""
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return "", "", ""
	}
	if _, err := strconv.ParseInt(parts[2], 10, 64); err != nil {
		return "", "", ""
	}
	if n, err := strconv.ParseInt(parts[4], 10, 64); err != nil || n < 1_000_000_000 {
		return parts[1], parts[2], ""
	}
	return parts[1], parts[2], parts[4]
}

// isFeedCommentBox 判断节点是不是评论区，避免把评论者当成动态作者。
func isFeedCommentBox(n *xhtml.Node) bool {
	return hasClass(n, "comments-list") || hasClass(n, "mod-comments") || hasClass(n, "f-comments") ||
		hasClass(n, "comment-list") || hasClass(n, "comments-item") || hasClass(n, "comment-item") ||
		hasClass(n, "f-comment")
}

// parseFeedHTMLComments 读取卡片里已经渲染出来的评论。个人中心多数时候只有输入框，评论要另外请求。
func parseFeedHTMLComments(card, skip *xhtml.Node) []MoodComment {
	if card == nil {
		return nil
	}
	var items []*xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n == nil || (skip != nil && n == skip) {
			return
		}
		if n != card && n.Type == xhtml.ElementNode && (hasClass(n, "comments-item") || hasClass(n, "comment-item") || hasClass(n, "f-comment")) {
			items = append(items, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(card)

	out := make([]MoodComment, 0, len(items))
	for _, item := range items {
		c := parseFeedHTMLComment(item)
		if c.Content == "" && c.Author.Name == "" && len(c.Media) == 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// parseFeedHTMLComment 把一条评论节点收成作者、正文、时间和配图。
func parseFeedHTMLComment(n *xhtml.Node) MoodComment {
	name := cleanText(textOfClass(n, "nickname", "c-name", "f-name"))
	uin := strings.TrimSpace(attr(n, "data-uin"))
	if uin == "" {
		link := findFirst(n, func(el *xhtml.Node) bool {
			return el.Type == xhtml.ElementNode && el.Data == "a"
		})
		if link != nil {
			uin = firstUIN(attr(link, "href"))
		}
	}
	contentNode := findFirst(n, func(el *xhtml.Node) bool {
		return hasClass(el, "comment-content") || hasClass(el, "c-body") || hasClass(el, "comment-txt")
	})
	var content string
	if contentNode != nil {
		content = cleanText(nodeText(contentNode, nil))
	} else {
		content = cleanText(nodeText(n, func(el *xhtml.Node) bool {
			return hasClass(el, "nickname") || hasClass(el, "c-name") || hasClass(el, "f-name") || hasClass(el, "comment-time") || hasClass(el, "c-time")
		}))
		if name != "" {
			content = strings.TrimSpace(strings.TrimPrefix(content, name))
		}
	}
	when := cleanText(textOfClass(n, "comment-time", "c-time"))
	c := MoodComment{
		Author:   MoodPerson{UIN: uin, Name: name},
		Content:  content,
		HTML:     moodTextToHTML(content),
		Time:     parseFeedClock("", when, time.Now()),
		TimeText: when,
		Media:    feedImages(n, nil),
	}
	if c.Time > 0 {
		c.TimeText = formatFeedTime(c.Time)
	}
	if c.Author.Name == "" && c.Author.UIN != "" {
		c.Author.Name = c.Author.UIN
	}
	return c
}

// adaptFeedComments 把日志或动态接口里的评论收成查看页结构。字段名和说说不完全一样。
func adaptFeedComments(list []gjson.Result) []MoodComment {
	if len(list) == 0 {
		return nil
	}
	out := make([]MoodComment, 0, len(list))
	for _, raw := range list {
		c := parseMoodComment(raw)
		if c.Content == "" {
			rawText := firstMoodString(raw, "htmlContent", "htmlcontent", "comment", "text", "cont")
			if strings.Contains(rawText, "<") {
				rawText, _ = parseRichFragment(rawText)
			}
			c.Content = cleanText(rawText)
			c.HTML = moodTextToHTML(c.Content)
		}
		if c.Author.Name == "" {
			c.Author.Name = strings.TrimSpace(firstMoodString(raw, "nick", "nickname", "uinname"))
		}
		if c.Author.UIN == "" {
			c.Author.UIN = strings.TrimSpace(firstMoodString(raw, "uin", "fuin", "commentuin"))
		}
		if c.Time == 0 {
			c.Time = parseFeedClock(firstMoodString(raw, "create_time", "pubtime", "createTime", "time", "postTime"), "", time.Now())
		}
		if c.TimeText == "" {
			c.TimeText = formatFeedTime(c.Time)
		}
		if len(c.Replies) == 0 {
			replies := raw.Get("replyList").Array()
			if len(replies) == 0 {
				replies = raw.Get("replies").Array()
			}
			if len(replies) > 0 {
				c.Replies = adaptFeedComments(replies)
			}
		}
		if c.Content == "" && c.Author.Name == "" && len(c.Media) == 0 && len(c.Replies) == 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// attrNonNeg 读取第一个不小于 0 的数字属性，用来取赞数和评论数。
func attrNonNeg(n *xhtml.Node, keys ...string) (int, bool) {
	for _, key := range keys {
		v := strings.TrimSpace(attr(n, key))
		if v == "" {
			continue
		}
		i, err := strconv.Atoi(v)
		if err == nil && i >= 0 {
			return i, true
		}
	}
	return 0, false
}

// parseFeedRepost 解析卡片里转发的原文。没有作者也没有正文时返回空。
func parseFeedRepost(n *xhtml.Node, now time.Time) *MoodRepost {
	name := cleanText(textOfClass(n, "f-name"))
	uin := strings.TrimSpace(attr(n, "data-uin"))
	if uin == "" {
		link := findFirst(n, func(el *xhtml.Node) bool {
			return el.Type == xhtml.ElementNode && el.Data == "a" && hasClass(el, "f-name")
		})
		if link != nil {
			uin = firstUIN(attr(link, "href"))
			if name == "" {
				name = cleanText(nodeText(link, nil))
			}
		}
	}
	content := cleanText(nodeText(findContentRoot(n), func(el *xhtml.Node) bool {
		return hasClass(el, "f-name") || hasClass(el, "img-box") || hasClass(el, "f-op")
	}))
	if content == "" {
		content = cleanText(nodeText(n, func(el *xhtml.Node) bool {
			return hasClass(el, "f-name") || hasClass(el, "f-nick") || hasClass(el, "img-box") || hasClass(el, "f-op") || hasClass(el, "f-info")
		}))
	}
	if content == "" && name == "" {
		return nil
	}
	return &MoodRepost{
		TID:     strings.TrimSpace(attr(n, "data-tid")),
		Author:  MoodPerson{UIN: uin, Name: name},
		Content: content,
		HTML:    moodTextToHTML(content),
		Media:   feedImages(n, nil),
	}
}

// parseFeedJSONItem 把结构化动态条目收成帖子。广告条目直接跳过。
func parseFeedJSONItem(item gjson.Result, now time.Time) (MoodPost, bool) {
	if !item.Exists() {
		return MoodPost{}, false
	}
	appid := strings.TrimSpace(firstMoodString(item, "appid", "appId", "typeid"))
	uin := strings.TrimSpace(firstMoodString(item, "uin", "fuin", "user.uin"))
	name := strings.TrimSpace(firstMoodString(item, "nickname", "nick", "name"))
	key := strings.TrimSpace(firstMoodString(item, "key", "feedkey", "tid"))
	if isFeedAdvertisement(appid, strings.TrimSpace(firstMoodString(item, "tid", "storyid")), key, "") {
		return MoodPost{}, false
	}
	abstime := strings.TrimSpace(firstMoodString(item, "abstime", "time", "createtime"))
	content := strings.TrimSpace(firstMoodString(item, "summary", "content", "desc", "text"))
	title := strings.TrimSpace(firstMoodString(item, "title", "blogtitle"))
	action := strings.TrimSpace(firstMoodString(item, "action", "optext"))
	blogID := strings.TrimSpace(firstMoodString(item, "blogid", "blogId"))
	if blogID == "" {
		if m := feedBlogIDPattern.FindStringSubmatch(firstMoodString(item, "url", "href")); len(m) == 2 {
			blogID = m[1]
		}
	}
	origin := strings.TrimSpace(firstMoodString(item, "tid", "storyid"))
	post := MoodPost{
		TID:        feedStableID(appid, uin, origin, key, name, content, title, abstime),
		OriginTID:  origin,
		Unikey:     strings.TrimSpace(firstMoodString(item, "unikey")),
		Author:     MoodPerson{UIN: uin, Name: name},
		Time:       parseFeedClock(abstime, "", now),
		Content:    content,
		Title:      title,
		Action:     action,
		BlogID:     blogID,
		ShareURL:   normalizeMediaURL(firstMoodString(item, "url", "href")),
		ShareTitle: title,
	}
	post.TimeText = formatFeedTime(post.Time)
	post.FeedType, post.FeedLabel = classifyFeed(appid, action, blogID != "")
	post.HTML = moodTextToHTML(post.Content)
	if post.FeedType != "share" {
		post.ShareTitle = ""
		post.ShareURL = ""
	}
	if post.Author.Name == "" && post.Author.UIN != "" {
		post.Author.Name = post.Author.UIN
	}
	if post.Content == "" && post.Title == "" {
		return MoodPost{}, false
	}
	return post, true
}

// applyBlogDetail 用日志全文替换动态卡片上的摘要，并把正文图片补进媒体列表。
func applyBlogDetail(post *MoodPost, title, rawHTML string) {
	if post == nil {
		return
	}
	title = cleanText(title)
	text, media := parseRichFragment(rawHTML)
	text = cleanText(text)
	if title != "" {
		post.Title = title
	}
	if text != "" && (post.Content == "" || runeLen(text) > runeLen(post.Content)) {
		if text == post.Title {
			post.Content = ""
			post.HTML = ""
		} else {
			post.Content = text
			post.HTML = moodTextToHTML(text)
		}
	}
	post.Media = mergeFeedMedia(post.Media, media)
	if post.FeedType == "" || post.FeedType == "other" || post.FeedType == "shuoshuo" {
		post.FeedType, post.FeedLabel = "blog", "日志"
	}
}

// parseRichFragment 从日志正文 HTML 里取出纯文本和图片。
func parseRichFragment(fragment string) (string, []MoodMedia) {
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return "", nil
	}
	root, err := xhtml.Parse(strings.NewReader(fragment))
	if err != nil {
		return cleanText(fragment), nil
	}
	article := findFirst(root, func(n *xhtml.Node) bool {
		if n.Type != xhtml.ElementNode {
			return false
		}
		id := strings.ToLower(attr(n, "id"))
		if id == "blogdetaildiv" || id == "blogdetail" || id == "blogcontent" {
			return true
		}
		return hasClass(n, "blog_content") || hasClass(n, "blog-content") || hasClass(n, "blog_article")
	})
	if article == nil {
		article = root
	}
	text := cleanText(nodeText(article, func(n *xhtml.Node) bool {
		return hasClass(n, "comment") || hasClass(n, "comments") || hasClass(n, "mod-comments")
	}))
	return text, feedImages(article, nil)
}

// mergeFeedMedia 把日志正文里的图片接到卡片已有图片后面，相同地址只留一份。
func mergeFeedMedia(base, extra []MoodMedia) []MoodMedia {
	seen := map[string]bool{}
	for _, m := range base {
		if m.URL != "" {
			seen[m.URL] = true
		}
	}
	out := append([]MoodMedia{}, base...)
	for _, m := range extra {
		if m.URL != "" && seen[m.URL] {
			continue
		}
		if m.URL != "" {
			seen[m.URL] = true
		}
		out = append(out, m)
	}
	return out
}

// isFeedAdCard 判断这张卡片是不是个人中心广告，例如带推广样式或广告应用号。
func isFeedAdCard(card *xhtml.Node) bool {
	if card == nil {
		return false
	}
	if hasClass(card, "f-single-biz") || strings.TrimSpace(attr(card, "data-complete_adinfo")) != "" {
		return true
	}
	_, appid, _ := splitFeedDOMID(attr(card, "id"))
	return isFeedAdvertisement(appid, attr(card, "data-tid"), firstAttr(card, "data-key", "data-feedkey"), attr(card, "id"))
}

// isFeedAdvertisement 根据应用号 6600，或编号里带有 advertisement，判断是不是广告。
func isFeedAdvertisement(appid, tid, key, id string) bool {
	if strings.TrimSpace(appid) == "6600" {
		return true
	}
	blob := strings.ToLower(tid + " " + key + " " + id)
	return strings.Contains(blob, "advertisement")
}

// isSavedFeedAd 判断已经写入备份的一条是不是广告，方便下次备份时清掉。
func isSavedFeedAd(post MoodPost) bool {
	return isFeedAdvertisement("", post.OriginTID, post.TID, "")
}

// withoutFeedAds 从已保存动态里去掉广告，下次写查看页时就不再出现。
func withoutFeedAds(posts []MoodPost) []MoodPost {
	if len(posts) == 0 {
		return posts
	}
	out := make([]MoodPost, 0, len(posts))
	for _, post := range posts {
		if isSavedFeedAd(post) {
			continue
		}
		out = append(out, post)
	}
	return out
}

// classifyFeed 按应用号和操作文案区分说说、日志、相册、分享和转发。
func classifyFeed(appid, action string, blog bool) (string, string) {
	a := strings.TrimSpace(action)
	switch {
	case strings.Contains(a, "转发"):
		return "repost", "转发"
	case strings.Contains(a, "日志") || appid == "2" || (blog && appid != "4" && appid != "311" && appid != "202"):
		return "blog", "日志"
	case strings.Contains(a, "相册") || strings.Contains(a, "照片") || appid == "4":
		return "photo", "相册"
	case strings.Contains(a, "分享") || appid == "202":
		return "share", "分享"
	case strings.Contains(a, "说说") || appid == "311":
		return "shuoshuo", "说说"
	case strings.Contains(a, "投票"):
		return "vote", "投票"
	default:
		if blog {
			return "blog", "日志"
		}
		return "other", "动态"
	}
}

// feedStableID 给没有固定编号的动态生成稳定 id，增量备份才能认出同一条。
func feedStableID(appid, uin, tid, key, name, content, title, abstime string) string {
	key = strings.TrimSpace(key)
	if key != "" {
		return key
	}
	tid = strings.TrimSpace(tid)
	uin = strings.TrimSpace(uin)
	appid = strings.TrimSpace(appid)
	if appid != "" && uin != "" && tid != "" {
		return appid + "_" + uin + "_" + tid
	}
	if uin != "" && tid != "" {
		return uin + "_" + tid
	}
	sum := util.MD5(appid + "|" + uin + "|" + name + "|" + title + "|" + content + "|" + abstime)
	if len(sum) > 16 {
		sum = sum[:16]
	}
	return "feed_" + sum
}

// parseFeedClock 把接口时间戳或「昨天 12:30」「3月2日」收成秒级时间。
func parseFeedClock(abstime, text string, now time.Time) int64 {
	if n := unixSeconds(abstime); n > 0 {
		return n
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	if n := parseBoardTimeText(text); n > 0 {
		return n
	}
	if now.IsZero() {
		now = time.Now().In(shanghaiLoc)
	} else {
		now = now.In(shanghaiLoc)
	}
	hour, min := clockOf(text)
	switch {
	case strings.Contains(text, "刚刚"):
		return now.Unix()
	case strings.Contains(text, "昨天"):
		base := now.AddDate(0, 0, -1)
		return time.Date(base.Year(), base.Month(), base.Day(), hour, min, 0, 0, shanghaiLoc).Unix()
	case strings.Contains(text, "前天"):
		base := now.AddDate(0, 0, -2)
		return time.Date(base.Year(), base.Month(), base.Day(), hour, min, 0, 0, shanghaiLoc).Unix()
	}
	if m := feedMonthDayPattern.FindStringSubmatch(text); len(m) >= 3 {
		month, _ := strconv.Atoi(m[1])
		day, _ := strconv.Atoi(m[2])
		h, mi := hour, min
		if m[3] != "" {
			h, _ = strconv.Atoi(m[3])
			mi, _ = strconv.Atoi(m[4])
		}
		t := time.Date(now.Year(), time.Month(month), day, h, mi, 0, 0, shanghaiLoc)
		if t.After(now.Add(24 * time.Hour)) {
			t = t.AddDate(-1, 0, 0)
		}
		return t.Unix()
	}
	return 0
}

// formatFeedTime 把秒级时间格式化成查看页上的日期时间。
func formatFeedTime(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).In(shanghaiLoc).Format("2006-01-02 15:04")
}

// unixSeconds 把秒或毫秒时间戳收成秒。不像时间戳的数字返回 0。
func unixSeconds(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	if n > 1e12 {
		n /= 1000
	}
	if n < 1e9 {
		return 0
	}
	return n
}

// clockOf 从文字里取出时和分。没有钟点时返回 0 点 0 分。
func clockOf(text string) (int, int) {
	m := feedClockPattern.FindStringSubmatch(text)
	if len(m) != 3 {
		return 0, 0
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	return h, min
}

// feedImages 收集卡片里的图片和视频，跳过头像、操作栏和指定区域内的节点。
func feedImages(root, skip *xhtml.Node) []MoodMedia {
	if root == nil {
		return nil
	}
	var out []MoodMedia
	seen := map[string]bool{}
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n == nil || (skip != nil && n == skip) {
			return
		}
		if n != root && n.Type == xhtml.ElementNode && (hasClass(n, "f-aside") || hasClass(n, "f-user-avatar") || hasClass(n, "user-avatar") || hasClass(n, "f-repost") || hasClass(n, "f-op") || hasClass(n, "f-op-wrap")) {
			return
		}
		if n.Type == xhtml.ElementNode {
			if media, ok := mediaFromVideoAttrs(n); ok {
				if !seen[media.URL] {
					seen[media.URL] = true
					out = append(out, media)
				}
				if media.Poster != "" {
					seen[media.Poster] = true
				}
				// 封面图挂在视频节点里面，再往下走会把它收成一张静态图。
				return
			}
		}
		if n.Type == xhtml.ElementNode && (n.Data == "img" || n.Data == "video" || n.Data == "source") {
			if media, ok := mediaFromNode(n, photoLinkURL(n)); ok {
				if !seen[media.URL] {
					seen[media.URL] = true
					out = append(out, media)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// mediaFromVideoAttrs 从视频节点的 data-v_* 属性取出播放地址和封面。
// 个人中心有的视频只把地址放在 data-v_vidioswfurl，data-v_vidiourl 是空的，只读后者会把视频收成封面图。
func mediaFromVideoAttrs(n *xhtml.Node) (MoodMedia, bool) {
	play := ""
	for _, key := range []string{"data-v_h265", "data-v_vidiourl", "data-v_vidioswfurl", "data-v_playurl"} {
		v := strings.TrimSpace(attr(n, key))
		if strings.Contains(v, "://") {
			play = v
			break
		}
	}
	if play == "" {
		return MoodMedia{}, false
	}
	urls := filterFeedMediaURLs(compactURLs(play))
	if len(urls) == 0 {
		return MoodMedia{}, false
	}
	id := strings.TrimSpace(firstAttr(n, "data-v_itemid", "data-vid"))
	if id == "" {
		sum := util.MD5(urls[0])
		if len(sum) > 12 {
			sum = sum[:12]
		}
		id = sum
	}
	media := MoodMedia{ID: id, Type: "video", URL: urls[0], URLs: urls, VideoID: id}
	if poster := filterFeedMediaURLs(compactURLs(firstAttr(n, "data-v_picinfo_url"))); len(poster) > 0 {
		media.Poster = poster[0]
	}
	return media, true
}

// mediaFromNode 从图片或视频节点收集下载地址。图片按原图、大高清图、小图排列。
func mediaFromNode(n *xhtml.Node, extras ...string) (MoodMedia, bool) {
	origin := firstAttr(n, "data-originurl", "data-origin", "data-fullsrc", "data-img", "data-src")
	src := attr(n, "src")
	poster := firstAttr(n, "poster", "data-poster")
	var urls []string
	if n.Data == "video" || n.Data == "source" {
		urls = filterFeedMediaURLs(compactURLs(origin, src))
	} else {
		urls = filterFeedMediaURLs(rankFeedPhotoURLs(append([]string{origin, src}, extras...)...))
	}
	if len(urls) == 0 {
		return MoodMedia{}, false
	}
	kind := "image"
	if n.Data == "video" || n.Data == "source" || looksLikeVideo(urls[0]) {
		kind = "video"
	}
	id := firstAttr(n, "data-picid", "data-id", "data-vid")
	if id == "" {
		sum := util.MD5(urls[0])
		if len(sum) > 12 {
			sum = sum[:12]
		}
		id = sum
	}
	media := MoodMedia{ID: id, Type: kind, URL: urls[0], URLs: urls}
	if poster != "" {
		if p := compactURLs(poster); len(p) > 0 && isFeedImageURL(p[0]) {
			media.Poster = p[0]
		}
	}
	return media, true
}

// photoLinkURL 取出图片外层链接里的 pre，那是点开查看时用的更大预览。
func photoLinkURL(n *xhtml.Node) string {
	for p := n; p != nil && p.Parent != nil; p = p.Parent {
		if p.Type != xhtml.ElementNode || p.Data != "a" {
			continue
		}
		href := strings.TrimSpace(attr(p, "href"))
		if href == "" {
			return ""
		}
		parsed, err := url.Parse(href)
		if err != nil {
			return ""
		}
		pre := strings.TrimSpace(parsed.Query().Get("pre"))
		if pre == "" || pre == "||" {
			return ""
		}
		return pre
	}
	return ""
}

var qzonePhotoSpec = regexp.MustCompile(`/([abcoms])(&(?:ek|bo)=)`)

// largerQzonePhotoURLs 把一条图片地址展开成：原图、大高清图、小图。
func largerQzonePhotoURLs(raw string) []string {
	return rankFeedPhotoURLs(raw)
}

// rankFeedPhotoURLs 合并多条地址后按清晰度排序：原图、大高清图，最后才是列表小图。
// 下载按这个顺序尝试，前一张失败才用下一张。
func rankFeedPhotoURLs(raws ...string) []string {
	var origin, hd, mid, small []string
	for _, raw := range raws {
		raw = prepareFeedPhotoURL(raw)
		if raw == "" || raw == "||" {
			continue
		}
		loc := qzonePhotoSpec.FindStringSubmatchIndex(raw)
		if loc == nil {
			small = append(small, raw)
			continue
		}
		letter := raw[loc[2]:loc[3]]
		spec := func(name string) string {
			return dropFeedThumbSce(raw[:loc[2]] + name + raw[loc[3]:])
		}
		switch letter {
		case "o":
			origin = append(origin, dropFeedThumbSce(raw))
		case "b":
			origin = append(origin, spec("o"))
			hd = append(hd, dropFeedThumbSce(raw))
		case "m":
			origin = append(origin, spec("o"))
			hd = append(hd, spec("b"))
			mid = append(mid, raw)
		default:
			origin = append(origin, spec("o"))
			hd = append(hd, spec("b"))
			small = append(small, raw)
		}
	}
	return compactFeedPhotoURLs(append(append(append(origin, hd...), mid...), small...)...)
}

// prepareFeedPhotoURL 补全协议，并保留大图规格。普通地址整理会把 b&bo= 改成原图。
func prepareFeedPhotoURL(s string) string {
	const keep = "b&bo\x00="
	s = strings.Replace(s, "b&bo=", keep, 1)
	s = normalizeMediaURL(s)
	return strings.Replace(s, keep, "b&bo=", 1)
}

// compactFeedPhotoURLs 去掉空值和重复图片地址，并保留大图规格，不把大图改成原图。
func compactFeedPhotoURLs(urls ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range urls {
		u = prepareFeedPhotoURL(u)
		if u == "" || u == "||" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

// dropFeedThumbSce 去掉列表小图上的尺寸限制，避免原图和大图仍被压成缩略图。
func dropFeedThumbSce(u string) string {
	u = strings.ReplaceAll(u, "&sce=60-2-2", "")
	u = strings.ReplaceAll(u, "&sce=60-3-3", "")
	return u
}

// upgradeFeedImages 把已保存动态里的列表小图改成大图地址，并清掉旧文件路径以便重新下载。
func upgradeFeedImages(posts []MoodPost) bool {
	changed := false
	for i := range posts {
		if upgradeFeedMedia(posts[i].Media) {
			changed = true
		}
		if posts[i].Repost != nil && upgradeFeedMedia(posts[i].Repost.Media) {
			changed = true
		}
	}
	return changed
}

// feedNeedsMotionCheck 判断这条说说还要不要向详情确认实况图和视频。
// 信息流卡片上的实况图只有封面，播放地址在说说详情里；已经确认过或卡片上已有视频就不再请求。
func feedNeedsMotionCheck(post *MoodPost) bool {
	if post == nil || post.MediaMotionChecked {
		return false
	}
	if post.FeedType != "shuoshuo" && post.FeedType != "repost" {
		return false
	}
	if strings.TrimSpace(post.Author.UIN) == "" || strings.TrimSpace(post.OriginTID) == "" {
		return false
	}
	if mediaHasType(post.Media, "video") || (post.Repost != nil && mediaHasType(post.Repost.Media, "video")) {
		return false
	}
	if mediaHasType(post.Media, "image") {
		return true
	}
	return post.Repost != nil && mediaHasType(post.Repost.Media, "image")
}

// applyFeedMoodMedia 用说说详情里的视频和实况图替换卡片上的静态封面。
// 详情里没有可播放的视频时保持原样，已经下好的普通配图不换地址。
func applyFeedMoodMedia(post *MoodPost, item gjson.Result) bool {
	if post == nil || !item.Exists() {
		return false
	}
	media := parseMoodMedia(item)
	if !mediaHasType(media, "video") {
		return false
	}
	target := &post.Media
	if !mediaHasType(post.Media, "image") && post.Repost != nil && mediaHasType(post.Repost.Media, "image") {
		target = &post.Repost.Media
	}
	reuseDownloadedImages(*target, media)
	*target = media
	return true
}

// mediaHasType 判断媒体列表里有没有指定类型，用来决定要不要再向详情确认实况图。
func mediaHasType(list []MoodMedia, kind string) bool {
	for _, m := range list {
		if m.Type == kind {
			return true
		}
	}
	return false
}

// reuseDownloadedImages 把已经下好的普通配图路径接到同一地址的新记录上。
// 实况图会换成 mp4，不能沿用原来的 jpg 路径。
func reuseDownloadedImages(old, next []MoodMedia) {
	paths := map[string]string{}
	for _, m := range old {
		if m.Type == "video" || m.Path == "" {
			continue
		}
		if m.URL != "" {
			paths[m.URL] = m.Path
		}
		for _, u := range m.URLs {
			if u != "" {
				paths[u] = m.Path
			}
		}
	}
	for i := range next {
		if next[i].Type == "video" || next[i].Path != "" {
			continue
		}
		if p := paths[next[i].URL]; p != "" {
			next[i].Path = p
			continue
		}
		for _, u := range next[i].URLs {
			if p := paths[u]; p != "" {
				next[i].Path = p
				break
			}
		}
	}
}

// upgradeFeedMedia 把一份媒体的首选地址改成更清晰的图，并清空旧文件路径以便重新下载。
func upgradeFeedMedia(list []MoodMedia) bool {
	changed := false
	for i := range list {
		if list[i].Type == "video" {
			continue
		}
		cands := rankFeedPhotoURLs(append([]string{list[i].URL}, list[i].URLs...)...)
		if len(cands) == 0 || cands[0] == prepareFeedPhotoURL(list[i].URL) {
			continue
		}
		list[i].URL = cands[0]
		list[i].URLs = cands
		list[i].Path = ""
		changed = true
	}
	return changed
}

// filterFeedMediaURLs 只保留可以下载的图片或视频地址。
func filterFeedMediaURLs(urls []string) []string {
	var out []string
	for _, u := range urls {
		if isFeedImageURL(u) {
			out = append(out, u)
		}
	}
	return out
}

// isFeedImageURL 判断地址是不是动态配图。表情、头像和空白图不算。
func isFeedImageURL(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	if l == "" || strings.HasPrefix(l, "javascript:") || strings.HasPrefix(l, "data:") {
		return false
	}
	if strings.Contains(l, "qlogo") || strings.Contains(l, "qzonestyle.gtimg.cn/qzone/em/") || strings.Contains(l, "/qzone/em/") {
		return false
	}
	if strings.Contains(l, "blank.gif") || strings.Contains(l, "spacer.gif") {
		return false
	}
	if feedEmotePattern.MatchString(l) {
		return false
	}
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// looksLikeVideo 根据地址判断是不是视频文件。
func looksLikeVideo(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, ".mp4") || strings.Contains(l, ".m3u8") || strings.Contains(l, "video.qpic.cn")
}

// findContentRoot 找到卡片里真正放正文的节点。说说正文可能在文本框，也可能在信息区。
func findContentRoot(card *xhtml.Node) *xhtml.Node {
	inRepost := func(el *xhtml.Node) bool {
		return inside(el, card, func(p *xhtml.Node) bool {
			return p != card && (hasClass(p, "f-repost") || hasClass(p, "f-forward") || hasClass(p, "f-single"))
		})
	}
	if n := findFirst(card, func(el *xhtml.Node) bool {
		return hasClass(el, "content") && el.Data == "pre" && !inRepost(el)
	}); n != nil {
		return n
	}
	for _, class := range []string{"f-ct-txt", "f-info-txt", "txt-box", "f-ct", "f-single-content"} {
		if n := findFirst(card, func(el *xhtml.Node) bool {
			return hasClass(el, class) && !inRepost(el)
		}); n != nil {
			return n
		}
	}
	return card
}

// shareFromCard 取出分享卡片的标题和链接。
func shareFromCard(card *xhtml.Node) (string, string) {
	box := findFirst(card, func(n *xhtml.Node) bool {
		return hasClass(n, "f-share") || hasClass(n, "share-box") || hasClass(n, "f-ct-share")
	})
	if box == nil {
		return "", ""
	}
	link := findFirst(box, func(n *xhtml.Node) bool { return n.Type == xhtml.ElementNode && n.Data == "a" })
	title := cleanText(textOfClass(box, "f-share-title", "share-title"))
	href := ""
	if link != nil {
		href = strings.TrimSpace(attr(link, "href"))
		if title == "" {
			title = cleanText(nodeText(link, nil))
		}
	}
	if href != "" && !strings.HasPrefix(href, "http") {
		href = ""
	}
	return title, href
}

// blogIDFromNode 从节点属性或链接里找出日志编号。
func blogIDFromNode(n *xhtml.Node) string {
	if id := strings.TrimSpace(attr(n, "data-blogid")); id != "" {
		return id
	}
	blob := nodeHrefBlob(n)
	if m := feedBlogIDPattern.FindStringSubmatch(blob); len(m) == 2 {
		return m[1]
	}
	return ""
}

// nodeHrefBlob 拼出节点及其子节点上的链接，方便从中提取编号。
func nodeHrefBlob(n *xhtml.Node) string {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(el *xhtml.Node) {
		if el == nil {
			return
		}
		if el.Type == xhtml.ElementNode {
			if href := attr(el, "href"); href != "" {
				b.WriteString(href)
				b.WriteByte('\n')
			}
		}
		for c := el.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// firstUIN 从空间链接里取出第一个 QQ 号。
func firstUIN(s string) string {
	m := feedUINPattern.FindStringSubmatch(s)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

// textOfClass 读取第一个匹配样式节点里的文字，找不到时返回空。
func textOfClass(root *xhtml.Node, classes ...string) string {
	n := findFirst(root, func(el *xhtml.Node) bool {
		if el.Type != xhtml.ElementNode {
			return false
		}
		for _, class := range classes {
			if hasClass(el, class) {
				return true
			}
		}
		return false
	})
	if n == nil {
		return ""
	}
	return nodeText(n, nil)
}

// nodeText 读取节点文字。skip 返回真的子树会被跳过，用来避开昵称、时间和操作栏。
func nodeText(n *xhtml.Node, skip func(*xhtml.Node) bool) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(el *xhtml.Node) {
		if el == nil {
			return
		}
		if el != n && el.Type == xhtml.ElementNode {
			if skip != nil && skip(el) {
				return
			}
			if el.Data == "script" || el.Data == "style" {
				return
			}
			if hasClass(el, "f-repost") || hasClass(el, "f-forward") || (hasClass(el, "f-single") && el != n) {
				return
			}
			if el.Data == "br" || el.Data == "p" || el.Data == "div" || el.Data == "li" {
				b.WriteByte('\n')
			}
			if el.Data == "img" {
				src := firstAttr(el, "src", "data-src")
				if m := feedEmotePattern.FindStringSubmatch(src); len(m) == 2 {
					b.WriteString("[em]e")
					b.WriteString(m[1])
					b.WriteString("[/em]")
				}
			}
		}
		if el.Type == xhtml.TextNode {
			b.WriteString(el.Data)
		}
		for c := el.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// skipFeedChrome 判断节点是昵称、来源或操作栏，不属于正文。
func skipFeedChrome(n *xhtml.Node) bool {
	return hasClass(n, "f-nick") || hasClass(n, "f-info") || hasClass(n, "f-op") || hasClass(n, "f-op-wrap") || hasClass(n, "f-aside")
}

// findFirst 按深度优先返回第一个符合条件的子节点。
func findFirst(root *xhtml.Node, pred func(*xhtml.Node) bool) *xhtml.Node {
	if root == nil {
		return nil
	}
	var found *xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if found != nil || n == nil {
			return
		}
		if n != root && pred(n) {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	if pred(root) && root.Type == xhtml.ElementNode && hasClass(root, "f-single") {
		// 根卡片自己不算内部匹配，除非调用方就是在找它。
	}
	walk(root)
	return found
}

// inside 判断从当前节点往上、到达 stop 之前，有没有符合条件的祖先。
func inside(n, stop *xhtml.Node, pred func(*xhtml.Node) bool) bool {
	for p := n; p != nil && p != stop; p = p.Parent {
		if pred(p) {
			return true
		}
	}
	return false
}

// hasClass 判断节点的 class 是否包含给定类名，按整词匹配。
func hasClass(n *xhtml.Node, class string) bool {
	if n == nil || n.Type != xhtml.ElementNode || class == "" {
		return false
	}
	for _, part := range strings.Fields(attr(n, "class")) {
		if part == class {
			return true
		}
	}
	return false
}

// attr 读取节点上的一个属性，没有时返回空。
func attr(n *xhtml.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// firstAttr 按给定顺序返回第一个非空属性。
func firstAttr(n *xhtml.Node, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(attr(n, key)); v != "" {
			return v
		}
	}
	return ""
}

// cleanText 收掉多余空白，保留正文里的换行。
func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	prevBlank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if prevBlank || len(out) == 0 {
				continue
			}
			prevBlank = true
			out = append(out, "")
			continue
		}
		prevBlank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// firstInt 用正则取出第一段整数，没有时返回 0。
func firstInt(re *regexp.Regexp, s string) int {
	m := re.FindStringSubmatch(s)
	if len(m) != 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// runeLen 按字符数计算长度，用来比较摘要和日志全文哪个更完整。
func runeLen(s string) int {
	return len([]rune(s))
}
