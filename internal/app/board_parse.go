/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-08
 * @FileName: board_parse.go
 * @Description: [把空间留言板接口 JSON 归一成查看页使用的 MoodPost]
 */

package app

import (
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/tidwall/gjson"
)

// boardSecretPlaceholder 私密留言看不到正文时写进查看页的占位文案。
const boardSecretPlaceholder = "这是一条私密留言，当前账号看不到内容。"

var (
	boardImgSrcPattern       = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["']`)                                                              // 抽出 HTML <img> 的 src
	boardImgTagPattern       = regexp.MustCompile(`(?i)<img\b[^>]*>`)                                                                               // 整段 <img> 标签，用来从正文里摘掉配图
	boardImgBBPattern        = regexp.MustCompile(`(?i)\[img\](https?://[^\s\[\]]+)\[/img\]`)                                                       // UBB [img]url[/img]
	boardBrPattern           = regexp.MustCompile(`(?i)<br\s*/?>`)                                                                                  // 换行标签收成纯文本 \n
	boardTagPattern          = regexp.MustCompile(`(?i)<[^>]+>`)                                                                                    // 其余 HTML 标签
	boardDatePattern         = regexp.MustCompile(`(\d{4})\s*[年/\-.]\s*(\d{1,2})\s*[月/\-.]\s*(\d{1,2})[日号]?(?:\s+(\d{1,2}):(\d{2})(?::(\d{2}))?)?`) // 中文日期，如 2012年5月1日 12:00
	boardRelEmoteSrcPattern  = regexp.MustCompile(`(?i)(<img\b[^>]*\bsrc=["']?)/qzone/em/(e\d+\.gif)`)                                              // htmlContent 里的相对表情路径
	boardOfficialEmoteImgTag = regexp.MustCompile(`(?i)<img\b[^>]*src=["']https://qzonestyle\.gtimg\.cn/qzone/em/e\d+\.gif["'][^>]*>`)              // 已指向官方 CDN 的表情图
)

// parseBoardMessage 把 get_msgb 的一条 commentList 收成 MoodPost。
// tid 用留言 id；回复放进 comments；私密留言只保留占位文案，不暴露正文。
// floor 是本页推算的楼层（总数 - 偏移）；接口没给楼层字段时用它。
func parseBoardMessage(item gjson.Result, floor int) MoodPost {
	id := strings.TrimSpace(firstMoodString(item, "id", "msgid", "commentid"))
	secret := item.Get("secret").Int() != 0 || item.Get("isscret").Int() != 0 || item.Get("isSecret").Bool()
	created := parseBoardTime(item)
	post := MoodPost{
		TID:      id,
		Floor:    parseBoardFloor(item, id, floor),
		Time:     created,
		TimeText: formatMoodTime(created, firstMoodString(item, "pubtime", "pubTime", "time")),
		Secret:   secret,
		Author: MoodPerson{
			UIN:  strings.TrimSpace(firstMoodString(item, "uin", "fuin", "fromuin")),
			Name: strings.TrimSpace(firstMoodString(item, "nickname", "nick", "name", "uinname")),
		},
	}

	if secret {
		post.Content = boardSecretPlaceholder
		post.HTML = `<span class="secret-text">` + html.EscapeString(boardSecretPlaceholder) + `</span>`
		post.Source = "私密留言"
		if post.Author.UIN == "" || post.Author.UIN == "0" {
			post.Author.Name = "私密访客"
			post.Author.UIN = ""
		}
		return post
	}

	plain, rich := renderBoardContent(item)
	post.Content = plain
	post.Media = parseBoardMedia(item, rich)
	// 配图改走 media 网格本地文件，正文里的远程 <img> 去掉；表情改成官方 CDN，离线打开也能显示（需联网）。
	post.HTML = rewriteBoardEmoteSrc(stripBoardRemoteImages(rich))

	replies := item.Get("replyList")
	if !replies.Exists() || replies.Type == gjson.Null {
		replies = item.Get("replylist")
	}
	if !replies.Exists() || replies.Type == gjson.Null {
		replies = item.Get("replys")
	}
	if replies.Exists() && replies.Type != gjson.Null {
		post.Comments = parseBoardReplies(replies.Array())
	}
	post.CommentCount = int(item.Get("replyCount").Int())
	if n := item.Get("replyNum").Int(); int(n) > post.CommentCount {
		post.CommentCount = int(n)
	}
	if post.CommentCount < len(post.Comments) {
		post.CommentCount = len(post.Comments)
	}
	return post
}

// parseBoardFloor 取出空间页上的「第 N 楼」。
// 接口偶尔给 floor/index；没有时用调用方按 total-偏移推的值；再没有才从 id 末尾数字猜。
func parseBoardFloor(item gjson.Result, id string, fallback int) int {
	for _, key := range []string{"floor", "index", "pos"} {
		if n := int(item.Get(key).Int()); n > 0 {
			return n
		}
	}
	if fallback > 0 {
		return fallback
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	if r := int(n % 10000); r > 0 {
		return r
	}
	return 0
}

// parseBoardIntro 取出主人寄语；没有寄语时返回 nil，查看页就不渲染置顶卡片。
func parseBoardIntro(nickname, uin, sign string) *BoardIntro {
	sign = strings.TrimSpace(sign)
	if sign == "" {
		return nil
	}
	if nickname == "" {
		nickname = uin
	}
	return &BoardIntro{
		Author:  MoodPerson{UIN: uin, Name: nickname},
		Content: sign,
		HTML:    moodTextToHTML(sign),
	}
}

// renderBoardContent 优先用 ubb/纯文本，没有再用 htmlContent 去标签。
func renderBoardContent(item gjson.Result) (plain, rich string) {
	ubb := firstMoodString(item, "ubbContent", "ubb", "content", "con")
	htmlContent := firstMoodString(item, "htmlContent", "html")
	if ubb != "" {
		return strings.TrimSpace(stripBoardTags(ubb)), boardRichHTML(ubb, htmlContent)
	}
	if htmlContent != "" {
		plain = strings.TrimSpace(stripBoardTags(htmlContent))
		return plain, boardRichHTML(plain, htmlContent)
	}
	return "", ""
}

// boardRichHTML 生成查看页 HTML：已有标签则保留表情图，否则按纯文本转义。
func boardRichHTML(plainOrUBB, htmlContent string) string {
	if looksLikeHTML(htmlContent) {
		s := replaceEmoteCodes(htmlContent)
		s = replaceAtBraceTokens(s, nil, true)
		return s
	}
	return moodTextToHTML(plainOrUBB)
}

// looksLikeHTML 判断接口给的 htmlContent 是不是已经带标签，避免再转义一层。
func looksLikeHTML(s string) bool {
	s = strings.TrimSpace(s)
	return strings.Contains(s, "<") && strings.Contains(s, ">")
}

// stripBoardTags 去掉 HTML/UBB 图标签，留下可搜索的纯文本。
func stripBoardTags(s string) string {
	s = boardImgBBPattern.ReplaceAllString(s, "")
	s = boardBrPattern.ReplaceAllString(s, "\n")
	s = boardTagPattern.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(s)
}

// parseBoardReplies 解析一条留言下的回复列表。
// 空间 get_msgb 的回复通常只有 content / nick / time，没有 ubbContent 和 id。
func parseBoardReplies(list []gjson.Result) []MoodComment {
	if len(list) == 0 {
		return nil
	}
	out := make([]MoodComment, 0, len(list))
	for _, raw := range list {
		created := moodUnixTime(raw, "time", "timestamp", "create_time", "createTime")
		if created == 0 {
			created = parseBoardTime(raw)
		}
		plain, rich := renderBoardContent(raw)
		if plain == "" && rich == "" {
			plain = strings.TrimSpace(firstMoodString(raw, "content", "con", "ubbContent"))
			rich = moodTextToHTML(plain)
		}
		c := MoodComment{
			ID:       strings.TrimSpace(firstMoodString(raw, "id", "replyid", "tid")),
			Content:  plain,
			HTML:     rewriteBoardEmoteSrc(stripBoardRemoteImages(rich)),
			Time:     created,
			TimeText: formatMoodTime(created, firstMoodString(raw, "pubtime", "time", "createTime")),
			Author: MoodPerson{
				UIN:  strings.TrimSpace(firstMoodString(raw, "uin", "fuin")),
				Name: strings.TrimSpace(firstMoodString(raw, "nick", "nickname", "name", "uinname")),
			},
			Media: parseBoardMedia(raw, rich),
		}
		if c.ID == "" && (c.Author.UIN != "" || created > 0) {
			c.ID = c.Author.UIN + "-" + strconv.FormatInt(created, 10)
		}
		if c.Content == "" && len(c.Media) == 0 && c.Author.Name == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// parseBoardMedia 从 bmp / 图片字段 / HTML img / UBB [img] 收集配图，空间表情 gif 不落盘。
func parseBoardMedia(item gjson.Result, htmlBody string) []MoodMedia {
	var urls []string
	push := func(raw string) {
		u := normalizeMediaURL(raw)
		// bmp 常是气泡样式 ID（如 18d195a001008101），不是下载地址；表情走正文 CDN。
		if u == "" || isBoardEmoteURL(u) || !looksLikeRemoteMediaURL(u) {
			return
		}
		urls = append(urls, u)
	}

	// bmp 只有真的是 http(s) 图才收；不要和 pic 拼在一起优先取 bmp。
	push(firstMoodString(item, "bmp"))
	push(firstMoodString(item, "picUrl", "picurl"))
	item.Get("pic").ForEach(func(_, v gjson.Result) bool {
		if v.Type == gjson.String {
			push(v.String())
		} else {
			push(firstMoodString(v, "url", "url3", "url2", "o_url", "origin_url"))
		}
		return true
	})
	item.Get("richinfo").ForEach(func(_, v gjson.Result) bool {
		push(firstMoodString(v, "url", "url3", "picurl"))
		return true
	})

	for _, m := range boardImgSrcPattern.FindAllStringSubmatch(htmlBody, -1) {
		if len(m) > 1 {
			push(m[1])
		}
	}
	ubb := firstMoodString(item, "ubbContent", "ubb", "content", "htmlContent")
	for _, m := range boardImgBBPattern.FindAllStringSubmatch(ubb, -1) {
		if len(m) > 1 {
			push(m[1])
		}
	}

	seen := map[string]bool{}
	out := make([]MoodMedia, 0, len(urls))
	for _, u := range compactURLs(urls...) {
		if seen[u] {
			continue
		}
		seen[u] = true
		id := util.MD5(u)
		if len(id) > 16 {
			id = id[:16]
		}
		out = append(out, MoodMedia{
			ID:   id,
			Type: "image",
			URL:  u,
		})
	}
	return out
}

// isBoardEmoteURL 判断是不是空间自带表情。这类图走官方 CDN，不进 media/。
func isBoardEmoteURL(u string) bool {
	lu := strings.ToLower(u)
	return strings.Contains(lu, "qzonestyle.gtimg.cn/qzone/em/") ||
		strings.Contains(lu, "/qzone/em/")
}

// looksLikeRemoteMediaURL 只有带协议的地址才去下载；相对路径和气泡 ID 都会被丢掉。
func looksLikeRemoteMediaURL(u string) bool {
	lu := strings.ToLower(strings.TrimSpace(u))
	return strings.HasPrefix(lu, "https://") || strings.HasPrefix(lu, "http://")
}

// rewriteBoardEmoteSrc 把 htmlContent 里的 /qzone/em/e182.gif 收成官方 CDN，并标成小表情。
func rewriteBoardEmoteSrc(s string) string {
	if s == "" {
		return s
	}
	s = boardRelEmoteSrcPattern.ReplaceAllString(s, `${1}https://qzonestyle.gtimg.cn/qzone/em/${2}`)
	return boardOfficialEmoteImgTag.ReplaceAllStringFunc(s, func(tag string) string {
		if strings.Contains(strings.ToLower(tag), "emote") {
			return tag
		}
		return strings.Replace(tag, "<img", `<img class="emote"`, 1)
	})
}

// dropNonRemoteBoardMedia 丢掉气泡 ID 这类伪地址，已下好的本地文件保留。
func dropNonRemoteBoardMedia(list []MoodMedia) []MoodMedia {
	if len(list) == 0 {
		return list
	}
	out := list[:0]
	for _, m := range list {
		if strings.TrimSpace(m.Path) != "" || looksLikeRemoteMediaURL(m.URL) {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// repairBoardPost 生成查看页或再次备份前，修正表情相对路径并清掉误收的 bmp。
func repairBoardPost(p MoodPost) MoodPost {
	p = repairMoodPost(p)
	p.HTML = rewriteBoardEmoteSrc(p.HTML)
	p.Media = dropNonRemoteBoardMedia(p.Media)
	p.Comments = repairBoardComments(p.Comments)
	return p
}

// repairBoardComments 递归修正回复里的表情路径和误收的配图。
func repairBoardComments(cs []MoodComment) []MoodComment {
	for i := range cs {
		cs[i].HTML = rewriteBoardEmoteSrc(cs[i].HTML)
		cs[i].Media = dropNonRemoteBoardMedia(cs[i].Media)
		cs[i].Replies = repairBoardComments(cs[i].Replies)
	}
	return cs
}

// stripBoardRemoteImages 去掉正文里待下载的配图标签，空间表情 gif 留下。
func stripBoardRemoteImages(s string) string {
	if s == "" {
		return s
	}
	s = boardImgTagPattern.ReplaceAllStringFunc(s, func(tag string) string {
		m := boardImgSrcPattern.FindStringSubmatch(tag)
		if len(m) > 1 && isBoardEmoteURL(m[1]) {
			return tag
		}
		return ""
	})
	s = boardImgBBPattern.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// parseBoardTime 留言接口的时间可能是 unix，也可能是「2012-05-01 12:00」或「2012年5月1日」。
func parseBoardTime(item gjson.Result) int64 {
	text := firstMoodString(item, "pubtime", "pubTime", "time", "createTime")
	if n := parseBoardTimeText(text); n > 1e9 {
		return n
	}
	if n := moodUnixTime(item, "timestamp", "create_time", "created_time", "pubtime", "time"); n > 1e9 {
		return n
	}
	return parseBoardTimeText(text)
}

// parseBoardTimeText 把「2012-05-01 12:00」「2012年5月1日」或纯数字 unix 收成秒。
func parseBoardTimeText(text string) int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	if n, err := strconv.ParseInt(text, 10, 64); err == nil && n > 0 {
		if n > 1e12 {
			n /= 1000
		}
		return n
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, text, shanghaiLoc); err == nil {
			return t.Unix()
		}
	}
	m := boardDatePattern.FindStringSubmatch(text)
	if len(m) < 4 {
		return 0
	}
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	hour, min, sec := 0, 0, 0
	if len(m) > 4 && m[4] != "" {
		hour, _ = strconv.Atoi(m[4])
	}
	if len(m) > 5 && m[5] != "" {
		min, _ = strconv.Atoi(m[5])
	}
	if len(m) > 6 && m[6] != "" {
		sec, _ = strconv.Atoi(m[6])
	}
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, shanghaiLoc).Unix()
}

// keepBoardMedia 合并新旧留言时，把已经下好的配图路径拷回去（含回复里的图）。
func keepBoardMedia(dst, src MoodPost) MoodPost {
	dst = keepExistingMedia(dst, src)
	if len(src.Comments) == 0 || len(dst.Comments) == 0 {
		return dst
	}
	byID := map[string]MoodComment{}
	for _, c := range src.Comments {
		if c.ID != "" {
			byID[c.ID] = c
		}
	}
	for i := range dst.Comments {
		old, ok := byID[dst.Comments[i].ID]
		if !ok {
			continue
		}
		for j, m := range dst.Comments[i].Media {
			if found, ok := matchMedia(indexMedia(old.Media), m); ok && found.Path != "" {
				dst.Comments[i].Media[j].Path = found.Path
			}
		}
	}
	return dst
}

// indexMedia 按 id 和原始 URL 建索引，合并新旧留言时用来对上同一张图。
func indexMedia(list []MoodMedia) map[string]MoodMedia {
	byID := map[string]MoodMedia{}
	for _, m := range list {
		if m.ID != "" {
			byID[m.ID] = m
		}
		if m.URL != "" {
			byID["url:"+m.URL] = m
		}
	}
	return byID
}
