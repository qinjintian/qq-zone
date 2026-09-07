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
	emotePattern = regexp.MustCompile(`\[em\]e(\d+)\[/em\]`)
	shanghaiLoc  = func() *time.Location {
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			return time.Local
		}
		return loc
	}()
)

func parseMoodPost(item gjson.Result, extraComments []gjson.Result) MoodPost {
	created := item.Get("created_time").Int()
	post := MoodPost{
		TID:          strings.TrimSpace(item.Get("tid").String()),
		Time:         created,
		TimeText:     formatMoodTime(created, item.Get("createTime").String()),
		Source:       strings.TrimSpace(item.Get("source_name").String()),
		Location:     moodLocation(item.Get("lbs")),
		LikeCount:    parseLikeCount(item),
		Likes:        parseLikers(item),
		CommentCount: int(item.Get("cmtnum").Int()),
		HasMoreCon:   item.Get("has_more_con").Int() != 0,
		PicTotal:     int(item.Get("pictotal").Int()),
		Author: MoodPerson{
			UIN:  strings.TrimSpace(item.Get("uin").String()),
			Name: strings.TrimSpace(item.Get("name").String()),
		},
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

func pickPicURL(pic gjson.Result) string {
	for _, key := range []string{"origin_url", "raw", "o_url", "url3", "url2", "url1", "url", "custom_url"} {
		if u := normalizeMediaURL(pic.Get(key).String()); u != "" {
			return u
		}
	}
	return ""
}

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

func parseLikeCount(item gjson.Result) int {
	if v := item.Get("like.count"); v.Exists() && v.Type != gjson.Null {
		return int(v.Int())
	}
	if v := item.Get("like"); v.Exists() && v.Type == gjson.Number {
		return int(v.Int())
	}
	if v := item.Get("likes"); v.Exists() && v.Type == gjson.Number {
		return int(v.Int())
	}
	return int(item.Get("rt_like").Int())
}

func parseLikers(item gjson.Result) []MoodPerson {
	var out []MoodPerson
	seen := map[string]bool{}
	addArr := func(arr []gjson.Result) {
		for _, x := range arr {
			p := MoodPerson{
				UIN:  strings.TrimSpace(firstMoodString(x, "uin", "fuin")),
				Name: strings.TrimSpace(firstMoodString(x, "nick", "name", "nickname")),
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
	addArr(item.Get("likemans").Array())
	return out
}

func moodLocation(lbs gjson.Result) string {
	if !lbs.Exists() || lbs.Type == gjson.Null {
		return ""
	}
	return strings.TrimSpace(firstMoodString(lbs, "name", "idname", "sname"))
}

func renderMoodContent(item gjson.Result) (plain, rich string) {
	conlist := item.Get("conlist")
	if conlist.Exists() && conlist.Type != gjson.Null && len(conlist.Array()) > 0 {
		var plainB, htmlB strings.Builder
		conlist.ForEach(func(_, node gjson.Result) bool {
			typ := node.Get("type").Int()
			switch typ {
			case 2:
				nick := strings.TrimSpace(firstMoodString(node, "nick", "name", "con"))
				if nick == "" {
					nick = node.Get("uin").String()
				}
				plainB.WriteString("@" + nick)
				htmlB.WriteString(`<span class="mention">@` + html.EscapeString(nick) + `</span>`)
			default:
				text := node.Get("con").String()
				if text == "" {
					text = node.Get("content").String()
				}
				plainB.WriteString(text)
				htmlB.WriteString(moodTextToHTML(text))
			}
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

func moodTextToHTML(text string) string {
	if text == "" {
		return ""
	}
	escaped := html.EscapeString(text)
	escaped = strings.ReplaceAll(escaped, "\r\n", "\n")
	escaped = strings.ReplaceAll(escaped, "\n", "<br>")
	escaped = emotePattern.ReplaceAllString(escaped, `<img class="emote" src="https://qzonestyle.gtimg.cn/qzone/em/e$1.gif" alt="" loading="lazy">`)
	return escaped
}

func formatMoodTime(ts int64, fallback string) string {
	if ts > 0 {
		return time.Unix(ts, 0).In(shanghaiLoc).Format("2006-01-02 15:04")
	}
	return strings.TrimSpace(fallback)
}

func firstMoodString(v gjson.Result, keys ...string) string {
	for _, key := range keys {
		s := strings.TrimSpace(v.Get(key).String())
		if s != "" && s != "null" {
			return s
		}
	}
	return ""
}

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
		for _, like := range p.Likes {
			add(like)
		}
		walkComments(p.Comments)
	}
	out := make([]MoodPerson, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	return out
}

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
