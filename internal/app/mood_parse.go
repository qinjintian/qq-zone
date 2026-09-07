/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-07
 * @FileName: mood_parse.go
 * @Description: [把空间说说接口 JSON 归一成查看页使用的 MoodPost]
 */

package app

import (
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

var (
	// emotePattern 匹配空间正文里的 [em]e100[/em] 表情码。
	emotePattern = regexp.MustCompile(`\[em\]e(\d+)\[/em\]`)
	// wholeMentionHTML 匹配整段 HTML 被包成一个 @ 的错误结果（历史备份）。
	wholeMentionHTML = regexp.MustCompile(`(?s)^<span class="mention">@?(.*)</span>$`)
	// emoteImgHTML 空间小黄脸图片；编号对应 qzonestyle 的 eN.gif。
	emoteImgHTML = `<img class="emote" src="https://qzonestyle.gtimg.cn/qzone/em/e$1.gif" alt="">`
	// atBracePattern 匹配评论回复里的 @{uin:123,nick:昵称,who:1,auto:1}。
	atBracePattern = regexp.MustCompile(`@\{([^}]+)\}`)
	atBraceUin     = regexp.MustCompile(`(?:^|,)uin:(\d+)`)                                    // 从花括号字段里取 QQ 号
	atBraceNick    = regexp.MustCompile(`(?:^|,)nick:(.*?)(?:,who:|,auto:|$)`)                 // 取 nick，遇 who/auto 结束
	atBraceAuto    = regexp.MustCompile(`(?:^|,)auto:(\d+)`)                                   // auto:1 表示「回复 xxx」而不是普通 @
	mentionUinHTML = regexp.MustCompile(`<span class="mention" data-uin="(\d+)">[^<]*</span>`) // 已生成的 @ 标签，用来换成备注名
	// shanghaiLoc 用于按国内时区切年/月目录和查看页年份导航。
	shanghaiLoc = func() *time.Location {
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			return time.Local
		}
		return loc
	}()
)

// parseMoodPost 把列表或详情接口的一条 JSON 收成 MoodPost。
// extraComments 非空时优先用它（详情接口翻页拼好的评论），否则读 item.commentlist。
func parseMoodPost(item gjson.Result, extraComments []gjson.Result) MoodPost {
	created := moodUnixTime(item, "created_time", "createdTime")
	post := MoodPost{
		TID:          strings.TrimSpace(item.Get("tid").String()),
		Time:         created,
		TimeText:     formatMoodTime(created, item.Get("createTime").String()),
		Source:       strings.TrimSpace(item.Get("source_name").String()),
		Location:     moodLocation(item.Get("lbs")),
		LikeCount:    parseLikeCount(item), // 列表接口通常没有 like，备份时再走 qz_opcnt2
		Likes:        parseLikers(item),
		CommentCount: int(item.Get("cmtnum").Int()),
		HasMoreCon:   item.Get("has_more_con").Int() != 0,
		PicTotal:     int(item.Get("pictotal").Int()),
		Author: MoodPerson{
			UIN:  strings.TrimSpace(item.Get("uin").String()),
			Name: strings.TrimSpace(item.Get("name").String()),
		},
	}
	if post.LikeCount < len(post.Likes) {
		post.LikeCount = len(post.Likes)
	}
	if edited, text := parseMoodEditTime(item, created); edited > 0 || text != "" {
		post.EditTime = edited
		post.EditTimeText = text
	}

	plain, rich := renderMoodContent(item)
	post.Content = plain
	post.HTML = rich

	if title := strings.TrimSpace(firstMoodString(item, "story.title", "share.title", "cell_info.title")); title != "" {
		post.ShareTitle = title
	}
	if link := strings.TrimSpace(firstMoodString(item, "story.share_url", "story.url", "share.url", "cell_info.url")); link != "" {
		post.ShareURL = link
	}

	post.Media = parseMoodMedia(item)
	post.Repost = parseMoodRepost(item)

	comments := extraComments
	if len(comments) == 0 {
		comments = item.Get("commentlist").Array()
	}
	post.Comments = parseMoodComments(comments)
	if post.CommentCount < len(post.Comments) {
		post.CommentCount = len(post.Comments)
	}

	return post
}

// parseMoodComments 解析一层评论列表；空内容且无配图、无回复的条目会丢掉。
func parseMoodComments(list []gjson.Result) []MoodComment {
	if len(list) == 0 {
		return nil
	}
	out := make([]MoodComment, 0, len(list))
	for _, raw := range list {
		c := parseMoodComment(raw)
		if c.Content == "" && len(c.Media) == 0 && len(c.Replies) == 0 && c.Author.Name == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// parseMoodComment 解析单条评论，楼中楼走 list_3（没有再试 list_2）。
func parseMoodComment(raw gjson.Result) MoodComment {
	created := raw.Get("create_time").Int()
	plain, rich := renderMoodContent(raw)
	c := MoodComment{
		ID:       strings.TrimSpace(firstMoodString(raw, "tid", "commentid", "id")),
		Content:  plain,
		HTML:     rich,
		Time:     created,
		TimeText: formatMoodTime(created, raw.Get("createTime").String()),
		Author: MoodPerson{
			UIN:  strings.TrimSpace(firstMoodString(raw, "uin", "fuin")),
			Name: strings.TrimSpace(firstMoodString(raw, "name", "nick", "uinname")),
		},
		Media: parseCommentMedia(raw),
	}

	replies := raw.Get("list_3").Array()
	if len(replies) == 0 {
		replies = raw.Get("list_2").Array()
	}
	if len(replies) > 0 {
		c.Replies = parseMoodComments(replies)
	}
	return c
}

// parseCommentMedia 收集评论配图，兼容 pic 数组和 rich_info 两种字段。
func parseCommentMedia(raw gjson.Result) []MoodMedia {
	var out []MoodMedia
	raw.Get("pic").ForEach(func(_, pic gjson.Result) bool {
		u := pickPicURL(pic)
		if u == "" {
			return true
		}
		out = append(out, MoodMedia{
			ID:   strings.TrimSpace(firstMoodString(pic, "pic_id", "picId")),
			Type: "image",
			URL:  u,
			URLs: compactURLs(u),
		})
		return true
	})
	raw.Get("rich_info").ForEach(func(_, info gjson.Result) bool {
		u := normalizeMediaURL(firstMoodString(info, "burl", "url"))
		if u == "" {
			return true
		}
		out = append(out, MoodMedia{
			Type: "image",
			URL:  u,
			URLs: compactURLs(u),
		})
		return true
	})
	return out
}

// parseMoodMedia 从说说正文收集图片、视频和语音；同一张图按 id/url 去重。
func parseMoodMedia(item gjson.Result) []MoodMedia {
	var out []MoodMedia
	seen := map[string]bool{}

	add := func(m MoodMedia) {
		if m.URL == "" && len(m.URLs) == 0 {
			return
		}
		key := m.ID
		if key == "" {
			key = m.URL
		}
		if key != "" && seen[key] {
			return
		}
		if key != "" {
			seen[key] = true
		}
		out = append(out, m)
	}

	item.Get("pic").ForEach(func(_, pic gjson.Result) bool {
		videoInfo := pic.Get("video_info")
		if pic.Get("is_video").Bool() || pic.Get("is_video").Int() == 1 || videoInfo.Exists() && videoInfo.Type != gjson.Null {
			vidURL := pickVideoURL(videoInfo)
			if vidURL == "" {
				vidURL = pickPicURL(pic)
			}
			add(MoodMedia{
				ID:      strings.TrimSpace(firstMoodString(pic, "pic_id", "video_id", "img_id")),
				Type:    "video",
				URL:     vidURL,
				URLs:    compactURLs(vidURL, pickVideoURL(videoInfo), pickPicURL(pic)),
				VideoID: strings.TrimSpace(firstMoodString(videoInfo, "video_id", "vid")),
				Width:   int(pic.Get("width").Int()),
				Height:  int(pic.Get("height").Int()),
			})
			return true
		}
		u := pickPicURL(pic)
		add(MoodMedia{
			ID:     strings.TrimSpace(firstMoodString(pic, "pic_id", "img_id")),
			Type:   "image",
			URL:    u,
			URLs:   compactURLs(u, pickPicURL(pic)),
			Width:  int(pic.Get("width").Int()),
			Height: int(pic.Get("height").Int()),
		})
		return true
	})

	item.Get("video").ForEach(func(_, video gjson.Result) bool {
		u := pickVideoURL(video)
		add(MoodMedia{
			ID:      strings.TrimSpace(firstMoodString(video, "video_id", "vid", "id")),
			Type:    "video",
			URL:     u,
			URLs:    compactURLs(u, pickVideoURL(video)),
			VideoID: strings.TrimSpace(firstMoodString(video, "video_id", "vid")),
			Width:   int(video.Get("width").Int()),
			Height:  int(video.Get("height").Int()),
		})
		return true
	})

	item.Get("voice").ForEach(func(_, voice gjson.Result) bool {
		u := normalizeMediaURL(firstMoodString(voice, "url", "voiceUrl", "voice_url"))
		add(MoodMedia{
			ID:   strings.TrimSpace(firstMoodString(voice, "id", "voiceID")),
			Type: "voice",
			URL:  u,
			URLs: compactURLs(u),
		})
		return true
	})

	if cover := normalizeMediaURL(firstMoodString(item, "story.cover", "story.img", "share.cover")); cover != "" {
		add(MoodMedia{Type: "image", URL: cover, URLs: compactURLs(cover)})
	}

	return out
}

// parseMoodRepost 解析转发的原说说；原帖被删时仍保留一段提示文案。
func parseMoodRepost(item gjson.Result) *MoodRepost {
	tid := strings.TrimSpace(item.Get("rt_tid").String())
	content := strings.TrimSpace(item.Get("rt_con.content").String())
	name := strings.TrimSpace(item.Get("rt_uinname").String())
	uin := strings.TrimSpace(item.Get("rt_uin").String())
	if tid == "" && content == "" && name == "" {
		return nil
	}

	rt := &MoodRepost{
		TID:     tid,
		Content: content,
		Author: MoodPerson{
			UIN:  uin,
			Name: name,
		},
	}
	if rt.Content == "" {
		rt.Content = "原说说已删除或不可见"
	}
	rt.HTML = moodTextToHTML(rt.Content)

	if pics := item.Get("rt_pic"); pics.Exists() && pics.Type != gjson.Null {
		wrapped := gjson.Parse(`{"pic":` + pics.Raw + `}`)
		rt.Media = parseMoodMedia(wrapped)
	}
	return rt
}

// appendMoodPicURLs 把 get_pics 补回来的地址追加进帖子，跳过已经有的 URL。
func appendMoodPicURLs(post *MoodPost, urls []string) {
	if post == nil {
		return
	}
	have := map[string]bool{}
	for _, m := range post.Media {
		have[m.URL] = true
		for _, u := range m.URLs {
			have[u] = true
		}
	}
	for i, raw := range urls {
		u := normalizeMediaURL(raw)
		if u == "" || have[u] {
			continue
		}
		have[u] = true
		post.Media = append(post.Media, MoodMedia{
			ID:   "extra-" + strconv.Itoa(i),
			Type: "image",
			URL:  u,
			URLs: compactURLs(u),
		})
	}
}

// needMoodDetail 判断列表数据是否不够用：正文被截断，或评论数多于已返回的列表。
func needMoodDetail(item gjson.Result, post MoodPost) bool {
	if post.HasMoreCon {
		return true
	}
	cmtNum := int(item.Get("cmtnum").Int())
	if cmtNum > len(post.Comments) {
		return true
	}
	if post.PicTotal > 0 && len(post.Media) < post.PicTotal {
		return false // 配图缺口走 get_pics，不必整页详情
	}
	return false
}

// needMoodPics 判断配图是否被列表截断，需要再打 get_pics。
func needMoodPics(post MoodPost) bool {
	if post.PicTotal <= 0 {
		return false
	}
	images := 0
	for _, m := range post.Media {
		if m.Type == "image" || m.Type == "video" {
			images++
		}
	}
	return images < post.PicTotal
}

// moodRightOnlySelf 是空间 ugc_right / right 里「仅自己可见」的取值。
const moodRightOnlySelf = 64

// moodHiddenFromViewer 判断当前登录账号是否看不到这条说说。
// 备份自己的空间时不过滤私密说说；备份好友时仅自己可见、无 tid 的条目直接跳过。
func moodHiddenFromViewer(item gjson.Result, viewerUin, ownerUin string) bool {
	if viewerUin != "" && ownerUin != "" && viewerUin == ownerUin {
		return false
	}
	if strings.TrimSpace(item.Get("tid").String()) == "" {
		return true
	}
	if item.Get("secret").Int() == 1 {
		return true
	}
	right := item.Get("ugc_right").Int()
	if right == 0 {
		right = item.Get("right").Int()
	}
	if right == 0 {
		right = item.Get("right_info.ugc_right").Int()
	}
	if right == moodRightOnlySelf {
		return true
	}
	text := strings.TrimSpace(item.Get("content").String())
	return strings.Contains(text, "仅自己可见") || strings.Contains(text, "无权查看")
}

// moodLooksEmpty 这条说说没有正文、配图、转发和分享，详情超时或无权时当作不可见跳过。
func moodLooksEmpty(post MoodPost) bool {
	return strings.TrimSpace(post.Content) == "" &&
		len(post.Media) == 0 &&
		post.Repost == nil &&
		strings.TrimSpace(post.ShareURL) == "" &&
		post.PicTotal == 0
}

// pickPicURL 按清晰度从高到低挑图片地址：原图 → url3 → 缩略图。
func pickPicURL(pic gjson.Result) string {
	for _, key := range []string{"origin_url", "raw", "o_url", "url3", "url2", "url1", "url", "custom_url"} {
		if u := normalizeMediaURL(pic.Get(key).String()); u != "" {
			return u
		}
	}
	return ""
}

// pickVideoURL 从 video / video_info 里挑可下载的播放地址。
func pickVideoURL(video gjson.Result) string {
	if !video.Exists() || video.Type == gjson.Null {
		return ""
	}
	for _, key := range []string{"url3", "play_url", "video_url", "url2", "url1", "url"} {
		if u := normalizeMediaURL(video.Get(key).String()); u != "" {
			return u
		}
	}
	return pickPicURL(video)
}

// normalizeMediaURL 把协议补成 https，解开 cgi_imgproxy，并尽量把预览参数改成原图。
func normalizeMediaURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" || s == "undefined" {
		return ""
	}
	if strings.HasPrefix(s, "//") {
		s = "https:" + s
	}
	if strings.HasPrefix(s, "http://") {
		s = "https://" + strings.TrimPrefix(s, "http://")
	}
	if strings.Contains(s, "cgi_imgproxy") {
		if parsed, err := url.Parse(s); err == nil {
			if inner := parsed.Query().Get("url"); inner != "" {
				if decoded, err := url.QueryUnescape(inner); err == nil && strings.HasPrefix(decoded, "http") {
					return normalizeMediaURL(decoded)
				}
			}
		}
	}
	if strings.Contains(s, "b&bo=") {
		s = strings.Replace(s, "b&bo=", "o&bo=", 1)
	}
	return s
}

// compactURLs 去掉空值和重复地址，保留换源顺序。
func compactURLs(urls ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range urls {
		u = normalizeMediaURL(u)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

// parseLikeCount 兼容 like.cnt / like.count / 纯数字；空间列表接口实际用得最多的是 cnt。
func parseLikeCount(item gjson.Result) int {
	for _, key := range []string{
		"like.cnt", "like.count", "like.num", "like.like_num",
		"like_num", "likenum", "rt_like.cnt", "rt_like.count",
	} {
		v := item.Get(key)
		if v.Exists() && v.Type == gjson.Number {
			return int(v.Int())
		}
	}
	if v := item.Get("like"); v.Exists() && v.Type == gjson.Number {
		return int(v.Int())
	}
	if v := item.Get("likes"); v.Exists() && v.Type == gjson.Number {
		return int(v.Int())
	}
	return 0
}

// parseLikers 从列表/详情 JSON 里顺手取点赞人。
// 空间网页实际不靠这些字段：msglist 根本没有 like，备份时会再打 get_like_list_app。
func parseLikers(item gjson.Result) []MoodPerson {
	var out []MoodPerson
	seen := map[string]bool{}
	addArr := func(arr []gjson.Result) {
		for _, x := range arr {
			p := MoodPerson{
				UIN:  strings.TrimSpace(firstMoodString(x, "fuin", "uin", "user.uin")),
				Name: strings.TrimSpace(firstMoodString(x, "nick", "name", "nickname", "user.nick")),
			}
			key := p.UIN
			if key == "" {
				key = p.Name
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, p)
		}
	}
	addArr(item.Get("like.list").Array())
	addArr(item.Get("like.info").Array())
	addArr(item.Get("like.like_uin_info").Array())
	addArr(item.Get("like.likemans").Array())
	addArr(item.Get("likemans").Array())
	addArr(item.Get("like_uin_info").Array())
	return out
}

// moodLocation 取出 lbs 里可读的地点名。
func moodLocation(lbs gjson.Result) string {
	if !lbs.Exists() || lbs.Type == gjson.Null {
		return ""
	}
	return strings.TrimSpace(firstMoodString(lbs, "name", "idname", "sname"))
}

// renderMoodContent 优先按 conlist 还原 @好友 和正文；没有 conlist 就用 content。
func renderMoodContent(item gjson.Result) (plain, rich string) {
	conlist := item.Get("conlist")
	if conlist.Exists() && conlist.Type != gjson.Null && len(conlist.Array()) > 0 {
		var plainB, htmlB strings.Builder
		conlist.ForEach(func(_, node gjson.Result) bool {
			if isMoodAtFriend(node) {
				nick := strings.TrimSpace(firstMoodString(node, "nick", "name"))
				if nick == "" {
					nick = strings.TrimSpace(node.Get("uin").String())
				}
				plainB.WriteString("@" + nick)
				htmlB.WriteString(`<span class="mention">@` + html.EscapeString(nick) + `</span>`)
				return true
			}
			text := node.Get("con").String()
			if text == "" {
				text = node.Get("content").String()
			}
			plainB.WriteString(text)
			htmlB.WriteString(moodTextToHTML(text))
			return true
		})
		return strings.TrimSpace(plainB.String()), htmlB.String()
	}

	text := item.Get("content").String()
	if text == "" {
		text = item.Get("con").String()
	}
	return strings.TrimSpace(text), moodTextToHTML(text)
}

// isMoodAtFriend 判断 conlist 节点是不是 @好友。
// 列表接口常把整段正文标成 type:2 且只有 con，没有 uin；那种要当普通文字，否则表情码不会转成图片。
func isMoodAtFriend(node gjson.Result) bool {
	if node.Get("type").Int() != 2 {
		return false
	}
	uin := strings.TrimSpace(node.Get("uin").String())
	if uin != "" && uin != "0" {
		return true
	}
	nick := strings.TrimSpace(firstMoodString(node, "nick", "name"))
	con := strings.TrimSpace(firstMoodString(node, "con", "content"))
	return nick != "" && con == ""
}

// moodTextToHTML 转义用户正文，换行变成 <br>，空间表情码变成 img。
func moodTextToHTML(text string) string {
	if text == "" {
		return ""
	}
	escaped := html.EscapeString(text)
	escaped = strings.ReplaceAll(escaped, "\r\n", "\n")
	escaped = strings.ReplaceAll(escaped, "\n", "<br>")
	escaped = replaceAtBraceTokens(escaped, nil, true)
	return replaceEmoteCodes(escaped)
}

// replaceEmoteCodes 把 [em]e120[/em] 换成空间官方表情 gif。
func replaceEmoteCodes(s string) string {
	if s == "" || !strings.Contains(strings.ToLower(s), "[em]") {
		return s
	}
	return emotePattern.ReplaceAllString(s, emoteImgHTML)
}

// parseAtBraceFields 拆 @{uin:...,nick:...,auto:...} 里的 QQ、昵称和是否「回复」前缀。
func parseAtBraceFields(inner string) (uin, nick string, auto bool) {
	if m := atBraceUin.FindStringSubmatch(inner); len(m) == 2 {
		uin = m[1]
	}
	if m := atBraceNick.FindStringSubmatch(inner); len(m) == 2 {
		nick = strings.TrimSpace(m[1])
	}
	if m := atBraceAuto.FindStringSubmatch(inner); len(m) == 2 && m[1] != "0" {
		auto = true
	}
	return uin, nick, auto
}

// displayNameForUin 优先用好友备注，没有备注才用接口昵称，再没有才显示 QQ 号。
func displayNameForUin(uin, fallback string, names map[string]string) string {
	if names != nil {
		if n := strings.TrimSpace(names[uin]); n != "" {
			return n
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return uin
}

// mentionHTML 生成查看页里的 @ 标签；data-uin 方便稍后用备注名替换 innerText。
func mentionHTML(uin, name string) string {
	return `<span class="mention" data-uin="` + html.EscapeString(uin) + `">` + html.EscapeString(name) + `</span>`
}

// replaceAtBraceTokens 把 @{uin:...,nick:...} 收成「回复 备注名」或 @名字。
func replaceAtBraceTokens(s string, names map[string]string, asHTML bool) string {
	if s == "" || !strings.Contains(s, "@{") {
		return s
	}
	return atBracePattern.ReplaceAllStringFunc(s, func(raw string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(raw, "@{"), "}")
		uin, nick, auto := parseAtBraceFields(inner)
		display := displayNameForUin(uin, nick, names)
		if asHTML {
			tag := mentionHTML(uin, display)
			if auto {
				return `<span class="reply-to">回复</span> ` + tag
			}
			return `@` + tag
		}
		if auto {
			return "回复 " + display
		}
		return "@" + display
	})
}

// applyNamesToMentionHTML 已生成的 mention 标签按 data-uin 换成好友备注。
func applyNamesToMentionHTML(rich string, names map[string]string) string {
	if rich == "" || names == nil || !strings.Contains(rich, "data-uin=") {
		return rich
	}
	return mentionUinHTML.ReplaceAllStringFunc(rich, func(raw string) string {
		m := mentionUinHTML.FindStringSubmatch(raw)
		if len(m) != 2 {
			return raw
		}
		if n := strings.TrimSpace(names[m[1]]); n != "" {
			return mentionHTML(m[1], n)
		}
		return raw
	})
}

// rewriteAtUinText 同时改纯文本和 HTML：把 @{uin:...} 收成「回复 备注名」，并给已有 @ 换备注。
func rewriteAtUinText(rich, plain string, names map[string]string) (string, string) {
	if rich == "" {
		rich = moodTextToHTML(plain)
	} else {
		rich = replaceAtBraceTokens(rich, names, true)
	}
	rich = applyNamesToMentionHTML(rich, names)
	plain = replaceAtBraceTokens(plain, names, false)
	return plain, rich
}

// applyFriendNamesToPosts 用登录账号的好友备注覆盖评论者、点赞人和 @ 里的昵称。
func applyFriendNamesToPosts(posts []MoodPost, names map[string]string) {
	if len(posts) == 0 || len(names) == 0 {
		for i := range posts {
			posts[i] = applyAtBraceToPost(posts[i], nil)
		}
		return
	}
	applyPerson := func(p *MoodPerson) {
		if p == nil || p.UIN == "" {
			return
		}
		if n := strings.TrimSpace(names[p.UIN]); n != "" {
			p.Name = n // 通讯录有备注就盖掉接口给的昵称
		}
	}
	var walkComments func([]MoodComment)
	walkComments = func(cs []MoodComment) {
		for i := range cs {
			applyPerson(&cs[i].Author)
			cs[i].Content, cs[i].HTML = rewriteAtUinText(cs[i].HTML, cs[i].Content, names)
			walkComments(cs[i].Replies)
		}
	}
	for i := range posts {
		applyPerson(&posts[i].Author)
		if posts[i].Repost != nil {
			applyPerson(&posts[i].Repost.Author)
			posts[i].Repost.Content, posts[i].Repost.HTML = rewriteAtUinText(posts[i].Repost.HTML, posts[i].Repost.Content, names)
		}
		for j := range posts[i].Likes {
			applyPerson(&posts[i].Likes[j])
		}
		posts[i].Content, posts[i].HTML = rewriteAtUinText(posts[i].HTML, posts[i].Content, names)
		walkComments(posts[i].Comments)
	}
}

// applyAtBraceToPost 只处理 @{uin:...} 和已有 mention，不改评论者/点赞人姓名（通讯录为空时走这条）。
func applyAtBraceToPost(p MoodPost, names map[string]string) MoodPost {
	p.Content, p.HTML = rewriteAtUinText(p.HTML, p.Content, names)
	if p.Repost != nil {
		p.Repost.Content, p.Repost.HTML = rewriteAtUinText(p.Repost.HTML, p.Repost.Content, names)
	}
	var walk func([]MoodComment) []MoodComment
	walk = func(cs []MoodComment) []MoodComment {
		for i := range cs {
			cs[i].Content, cs[i].HTML = rewriteAtUinText(cs[i].HTML, cs[i].Content, names)
			cs[i].Replies = walk(cs[i].Replies)
		}
		return cs
	}
	p.Comments = walk(p.Comments)
	return p
}

// repairMoodPost 修正历史备份里「整段正文被当成 @」以及没转成图片的表情码。
func repairMoodPost(p MoodPost) MoodPost {
	p.Content, p.HTML = repairMoodRichText(p.HTML, p.Content)
	if p.Repost != nil {
		p.Repost.Content, p.Repost.HTML = repairMoodRichText(p.Repost.HTML, p.Repost.Content)
	}
	p.Comments = repairMoodComments(p.Comments)
	return p
}

// repairMoodComments 递归修正评论和楼中楼里的表情码、误包成 @ 的正文。
func repairMoodComments(cs []MoodComment) []MoodComment {
	for i := range cs {
		cs[i].Content, cs[i].HTML = repairMoodRichText(cs[i].HTML, cs[i].Content)
		cs[i].Replies = repairMoodComments(cs[i].Replies)
	}
	return cs
}

// repairMoodRichText 拆掉错误的整段 mention 包裹，并把残留的 [em] 码换成图片。
func repairMoodRichText(rich, plain string) (string, string) {
	plain = strings.TrimSpace(plain)
	rich = strings.TrimSpace(rich)
	if m := wholeMentionHTML.FindStringSubmatch(rich); len(m) == 2 {
		inner := html.UnescapeString(m[1])
		if looksLikeMisparsedMoodBody(inner) {
			plain = strings.TrimPrefix(plain, "@")
			if strings.TrimSpace(plain) == "" {
				plain = inner
			}
			plain = strings.TrimSpace(plain)
			rich = moodTextToHTML(inner)
		}
	}
	if rich == "" {
		rich = moodTextToHTML(plain)
	} else {
		rich = replaceEmoteCodes(rich)
	}
	return rewriteAtUinText(rich, plain, nil)
}

// looksLikeMisparsedMoodBody 整段被包成 mention 时，用话题、表情码或长度判断这是正文而不是好友昵称。
func looksLikeMisparsedMoodBody(s string) bool {
	s = strings.TrimSpace(strings.TrimPrefix(s, "@"))
	if s == "" {
		return false
	}
	if strings.Contains(s, "[em]") || strings.Contains(s, "<img") || strings.Contains(s, "\n") {
		return true
	}
	if strings.HasPrefix(s, "#") {
		return true
	}
	return len([]rune(s)) > 16
}

// formatMoodTime 用东八区格式化 unix 时间；没有时间戳时退回接口给的中文日期。
func formatMoodTime(ts int64, fallback string) string {
	if ts > 0 {
		return time.Unix(ts, 0).In(shanghaiLoc).Format("2006-01-02 15:04")
	}
	return strings.TrimSpace(fallback)
}

// moodUnixTime 从候选字段取 unix 秒；接口有时给毫秒，大于 1e12 时先除掉。
func moodUnixTime(item gjson.Result, keys ...string) int64 {
	for _, key := range keys {
		v := item.Get(key)
		if !v.Exists() || v.Type == gjson.Null {
			continue
		}
		n := v.Int()
		if n <= 0 {
			continue
		}
		if n > 1e12 {
			n /= 1000
		}
		return n
	}
	return 0
}

// parseMoodEditTime 取出最后编辑时间。未改过、或只比发表时间晚几秒的忽略，避免把接口抖动当成编辑。
func parseMoodEditTime(item gjson.Result, created int64) (int64, string) {
	edited := moodUnixTime(item, "modifytime", "modify_time", "edit_time", "edittime", "lastedittime", "update_time", "updatetime")
	if edited > 0 && created > 0 && edited <= created+60 {
		edited = 0
	}
	text := ""
	if edited > 0 {
		text = formatMoodTime(edited, "")
	}
	if text == "" {
		fallback := firstMoodString(item, "modifyTime", "editTime", "lastEditTime")
		createText := strings.TrimSpace(item.Get("createTime").String())
		if fallback != "" && fallback != createText {
			text = fallback
		}
	}
	return edited, text
}

// firstMoodString 按候选字段名取第一个非空字符串，用来兼容接口字段别名。
func firstMoodString(v gjson.Result, keys ...string) string {
	for _, key := range keys {
		s := strings.TrimSpace(v.Get(key).String())
		if s != "" && s != "null" {
			return s
		}
	}
	return ""
}

// collectMediaPtrs 收集说说、转发和评论里所有媒体的指针，下载时原地回写 Path。
func collectMediaPtrs(posts []MoodPost) []*MoodMedia {
	var out []*MoodMedia
	var walkComments func(*[]MoodComment)
	walkComments = func(cs *[]MoodComment) {
		for i := range *cs {
			c := &(*cs)[i]
			for j := range c.Media {
				out = append(out, &c.Media[j])
			}
			walkComments(&c.Replies)
		}
	}
	for i := range posts {
		p := &posts[i]
		for j := range p.Media {
			out = append(out, &p.Media[j])
		}
		if p.Repost != nil {
			for j := range p.Repost.Media {
				out = append(out, &p.Repost.Media[j])
			}
		}
		walkComments(&p.Comments)
	}
	return out
}

// collectPeople 按 QQ 号去重，收集需要下头像的作者和评论者。
// 点赞栏只展示名字，不把头像人算进来，避免一条热门说说拖上几十个头像请求。
func collectPeople(posts []MoodPost) []MoodPerson {
	seen := map[string]MoodPerson{}
	add := func(p MoodPerson) {
		if p.UIN == "" {
			return
		}
		if old, ok := seen[p.UIN]; ok {
			if old.Name == "" && p.Name != "" {
				seen[p.UIN] = p
			}
			return
		}
		seen[p.UIN] = p
	}
	var walkComments func([]MoodComment)
	walkComments = func(cs []MoodComment) {
		for _, c := range cs {
			add(c.Author)
			walkComments(c.Replies)
		}
	}
	for _, p := range posts {
		add(p.Author)
		if p.Repost != nil {
			add(p.Repost.Author)
		}
		walkComments(p.Comments)
	}
	out := make([]MoodPerson, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	return out
}

// applyAvatars 把头像相对路径写回所有出现过该 QQ 号的人。
func applyAvatars(posts []MoodPost, avatars map[string]string) {
	set := func(p *MoodPerson) {
		if p == nil || p.UIN == "" {
			return
		}
		if path, ok := avatars[p.UIN]; ok {
			p.Avatar = path
		}
	}
	var walkComments func(*[]MoodComment)
	walkComments = func(cs *[]MoodComment) {
		for i := range *cs {
			c := &(*cs)[i]
			set(&c.Author)
			walkComments(&c.Replies)
		}
	}
	for i := range posts {
		p := &posts[i]
		set(&p.Author)
		if p.Repost != nil {
			set(&p.Repost.Author)
		}
		for j := range p.Likes {
			set(&p.Likes[j])
		}
		walkComments(&p.Comments)
	}
}

// keepExistingMedia 全量刷新文案时，把本地已经下好的文件路径拷回新解析的帖子，避免重复下载。
func keepExistingMedia(dst, src MoodPost) MoodPost {
	byID := map[string]MoodMedia{}
	index := func(list []MoodMedia) {
		for _, m := range list {
			if m.ID != "" {
				byID[m.ID] = m
			}
			if m.Path != "" {
				byID["path:"+m.Path] = m
			}
			if m.URL != "" {
				byID["url:"+m.URL] = m
			}
		}
	}
	index(src.Media)

	for i, m := range dst.Media {
		if old, ok := matchMedia(byID, m); ok && old.Path != "" {
			dst.Media[i].Path = old.Path
			if dst.Media[i].Poster == "" {
				dst.Media[i].Poster = old.Poster
			}
		}
	}
	if dst.Repost != nil && src.Repost != nil {
		index(src.Repost.Media)
		for i, m := range dst.Repost.Media {
			if old, ok := matchMedia(byID, m); ok && old.Path != "" {
				dst.Repost.Media[i].Path = old.Path
			}
		}
	}
	return dst
}

// matchMedia 用 pic_id 或原始 URL 把新旧两条媒体对上。
func matchMedia(byID map[string]MoodMedia, m MoodMedia) (MoodMedia, bool) {
	if m.ID != "" {
		if old, ok := byID[m.ID]; ok {
			return old, true
		}
	}
	if m.URL != "" {
		if old, ok := byID["url:"+m.URL]; ok {
			return old, true
		}
	}
	return MoodMedia{}, false
}
