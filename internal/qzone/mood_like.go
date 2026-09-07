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
 * @FileName: mood_like.go
 * @Description: [说说点赞：列表/详情接口不含点赞，需另走 qz_opcnt2 和 get_like_list_app]
 */

package qzone

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

const (
	likeCountBatchSize = 10    // qz_opcnt2 一次带多条 unikey；用 | 拼接时接口往往只回第一条
	moodOpCntUnikeySep = "<.>" // 空间页批量查询用这个分隔符，不是竖线
	likeListQueryCount = 60    // 点赞名单一页人数
	likeListStoredMax  = 60    // 写入备份的点赞人上限；空间页会列出当前能展开的全部名字
)

// MoodOpCnt 是 qz_opcnt2 一条说说的计数。空间网页的赞数、浏览数都来自这里，不在 msglist 里。
type MoodOpCnt struct {
	Like   int         // current.likedata.cnt，点赞人数
	Visit  int         // current.newdata.PRD 等，浏览次数
	Likers []MoodLiker // likedata.list 里可能已带部分点赞人，没有再另打名单接口
}

// MoodLiker 是一条说说的点赞人。
type MoodLiker struct {
	UIN  string // QQ 号，对应 like_uin_info.fuin
	Name string // 昵称，对应 like_uin_info.nick；备份时再用好友备注覆盖
}

// moodUnikey 空间点赞接口用的资源键。网页端说说是 http://user.qzone.qq.com/{uin}/mood/{tid}。
func moodUnikey(uin, tid string) string {
	return fmt.Sprintf("http://user.qzone.qq.com/%s/mood/%s", uin, tid)
}

// moodUnikeyVariants 同一条说说在点赞接口里可能带或不带 .1 后缀，两种都请求以免漏数。
func moodUnikeyVariants(uin, tid string) []string {
	key := moodUnikey(uin, tid)
	return []string{key, key + ".1"}
}

// tidFromMoodUnikey 从 unikey 末尾取出 tid，兼容 /mood/{tid} 和 /mood/{tid}.1。
func tidFromMoodUnikey(unikey string) string {
	unikey = strings.TrimSpace(unikey)
	idx := strings.LastIndex(unikey, "/mood/")
	if idx < 0 {
		return ""
	}
	tid := unikey[idx+len("/mood/"):]
	if q := strings.IndexAny(tid, "?#"); q >= 0 {
		tid = tid[:q]
	}
	tid = strings.TrimSuffix(tid, ".1")
	return strings.TrimSpace(tid)
}

// GetMoodLikeCounts 按 tid 批量取点赞数和浏览数。
// 空间网页不从 msglist 读 like，而是另打 qz_opcnt2；点赞 app 的 unikey 带 .1 后缀。
func (c *Client) GetMoodLikeCounts(ctx context.Context, targetUin string, tids []string) (map[string]MoodOpCnt, error) {
	out := map[string]MoodOpCnt{}
	if c == nil || len(tids) == 0 {
		return out, nil
	}

	uniq := uniqueMoodTIDs(tids)
	for i := 0; i < len(uniq); i += likeCountBatchSize {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		end := i + likeCountBatchSize
		if end > len(uniq) {
			end = len(uniq)
		}
		chunk := uniq[i:end]
		keys := make([]string, 0, len(chunk))
		for _, tid := range chunk {
			keys = append(keys, moodUnikey(targetUin, tid)+".1") // 点赞 app 的 unikey 带 .1
		}

		part, err := c.getMoodLikeCountsOnce(ctx, targetUin, keys)
		if err != nil {
			return out, err
		}
		// 分隔符不对或批量被截断时，返回条数会少于请求；缺的 tid 再单独拉。
		for _, tid := range chunk {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if _, ok := part[tid]; ok {
				continue
			}
			one, oneErr := c.getMoodLikeCountsOnce(ctx, targetUin, moodUnikeyVariants(targetUin, tid))
			if oneErr != nil {
				return out, oneErr
			}
			mergeMoodOpCnt(part, one)
		}
		mergeMoodOpCnt(out, part)
	}
	return out, nil
}

// getMoodLikeCountsOnce 请求一次 qz_opcnt2。多条 unikey 用 <.> 拼接；用 | 时接口常常只回第一条。
func (c *Client) getMoodLikeCountsOnce(ctx context.Context, targetUin string, unikeys []string) (map[string]MoodOpCnt, error) {
	if len(unikeys) == 0 {
		return map[string]MoodOpCnt{}, nil
	}

	params := url.Values{}
	params.Set("fupdate", "1")
	params.Set("face", "0")                                       // 不要头像数据，只要计数
	params.Set("_stp", fmt.Sprintf("%d", time.Now().UnixMilli())) // 前端会带时间戳，部分环境缺了会当未登录
	params.Set("unikey", strings.Join(unikeys, moodOpCntUnikeySep))
	params.Set("g_tk", c.GTK)

	apiURL := "https://user.qzone.qq.com/proxy/domain/r.qzone.qq.com/cgi-bin/user/qz_opcnt2?" + params.Encode()
	headers := c.moodHeaders(targetUin)

	cgiCtx, cancel := moodCGIContext(ctx)
	defer cancel()

	start := time.Now()
	_, body, code, err := c.Http.Get(cgiCtx, apiURL, headers)
	bodyStr := string(body)
	c.logAPI("GetMoodLikeCounts", apiURL, headers, bodyStr, code, time.Since(start), err)

	if err != nil {
		return nil, fmt.Errorf("拉取说说点赞数失败: %w", err)
	}
	if code != 200 {
		return nil, fmt.Errorf("拉取说说点赞数失败: HTTP %d", code)
	}
	return parseMoodLikeCounts(bodyStr, unikeys)
}

// GetMoodLikeList 拉取一条说说的点赞人名单。
// 对应空间「张三、李四 共10人觉得很赞」里的名字，字段在 data.like_uin_info；
// 第二个返回值是 data.total_number。uin 参数必须是当前登录号，不能填空间主人。
func (c *Client) GetMoodLikeList(ctx context.Context, targetUin, tid string) ([]MoodLiker, int, error) {
	tid = strings.TrimSpace(tid)
	if c == nil || tid == "" {
		return nil, 0, nil
	}

	// 点赞 app 的 unikey 带 .1；对不上再试不带后缀的写法。
	likers, total, err := c.getMoodLikeListOnce(ctx, targetUin, moodUnikey(targetUin, tid)+".1")
	if err != nil {
		return nil, 0, err
	}
	if len(likers) > 0 || total > 0 {
		return likers, total, nil
	}
	return c.getMoodLikeListOnce(ctx, targetUin, moodUnikey(targetUin, tid))
}

// getMoodLikeListOnce 请求一页 get_like_list_app；begin_uin=0 表示从名单开头取。
func (c *Client) getMoodLikeListOnce(ctx context.Context, targetUin, unikey string) ([]MoodLiker, int, error) {
	selfUin := firstNonEmpty(c.QQ, targetUin) // 接口校验登录态，必须是当前登录 QQ
	params := url.Values{}
	params.Set("uin", selfUin)
	params.Set("unikey", unikey)
	params.Set("begin_uin", "0") // 从名单开头取
	params.Set("query_count", fmt.Sprintf("%d", likeListQueryCount))
	params.Set("if_first_page", "1")
	params.Set("g_tk", c.GTK)
	params.Set("format", "jsonp")
	params.Set("inCharset", "utf-8")
	params.Set("outCharset", "utf-8")

	apiURL := "https://user.qzone.qq.com/proxy/domain/users.qzone.qq.com/cgi-bin/likes/get_like_list_app?" + params.Encode()
	headers := c.moodHeaders(targetUin)

	cgiCtx, cancel := moodCGIContext(ctx)
	defer cancel()

	start := time.Now()
	_, body, code, err := c.Http.Get(cgiCtx, apiURL, headers)
	bodyStr := string(body)
	c.logAPI("GetMoodLikeList", apiURL, headers, bodyStr, code, time.Since(start), err)

	if err != nil {
		return nil, 0, fmt.Errorf("拉取说说点赞名单失败: %w", err)
	}
	if code != 200 {
		return nil, 0, fmt.Errorf("拉取说说点赞名单失败: HTTP %d", code)
	}
	return parseMoodLikeList(bodyStr)
}

// parseMoodLikeCounts 解析 qz_opcnt2 的 JSON/JSONP。
// 有的返回带 unikey，没有时按 requested 的下标对回 tid，避免批量结果对不上。
func parseMoodLikeCounts(raw string, requested []string) (map[string]MoodOpCnt, error) {
	data, err := parseCGIBody(raw)
	if err != nil {
		return nil, fmt.Errorf("解析说说点赞数失败: %w", err)
	}
	res := gjson.Parse(data)
	if apiCode := res.Get("code").Int(); apiCode != 0 {
		return nil, moodAPIError(apiCode, res)
	}

	out := map[string]MoodOpCnt{}
	var items []gjson.Result
	field := res.Get("data")
	switch {
	case field.IsArray():
		field.ForEach(func(_, v gjson.Result) bool {
			if v.IsObject() {
				items = append(items, v)
			}
			return true
		})
	case field.IsObject():
		if field.Get("current").Exists() || field.Get("unikey").Exists() {
			items = append(items, field)
			break
		}
		field.ForEach(func(_, v gjson.Result) bool {
			if v.IsObject() {
				items = append(items, v)
			}
			return true
		})
	}

	for i, item := range items {
		unikey := strings.TrimSpace(firstNonEmpty(
			item.Get("unikey").String(),
			item.Get("key").String(),
			item.Get("current.key").String(),
			item.Get("current.unikey").String(),
		))
		if unikey == "" && i < len(requested) {
			unikey = requested[i] // 接口没回 unikey 时，按请求顺序对上 tid
		}
		tid := tidFromMoodUnikey(unikey)
		if tid == "" {
			continue
		}
		next := MoodOpCnt{
			Like:   moodLikeCountFromItem(item),
			Visit:  moodVisitCountFromItem(item),
			Likers: moodLikersFromItem(item),
		}
		if old, ok := out[tid]; ok {
			next = mergeOneMoodOpCnt(old, next)
		}
		out[tid] = next
	}
	return out, nil
}

// jsonInt 把接口里的数字或数字字符串收成 int；newdata 里 PRD 等有时是字符串。
func jsonInt(v gjson.Result) int {
	if !v.Exists() {
		return 0
	}
	switch v.Type {
	case gjson.Number:
		return int(v.Int())
	case gjson.String:
		n, _ := strconv.Atoi(strings.TrimSpace(v.String()))
		return n
	default:
		return 0
	}
}

// moodLikeCountFromItem 点赞数优先读 likedata.cnt，没有再试 newdata.LIKE。
func moodLikeCountFromItem(item gjson.Result) int {
	for _, key := range []string{
		"current.likedata.cnt",
		"current.like.cnt",
		"likedata.cnt",
		"current.likedata.count",
		"current.cntdata.like",
		"current.newdata.LIKE",
	} {
		if n := jsonInt(item.Get(key)); n > 0 {
			return n
		}
	}
	return jsonInt(item.Get("current.likedata.cnt"))
}

// moodVisitCountFromItem 浏览数在 newdata 里，说说页常见字段是 PRD。
func moodVisitCountFromItem(item gjson.Result) int {
	nd := item.Get("current.newdata")
	for _, key := range []string{"PRD", "SSRD", "BZRD", "RZRD", "VV", "visit", "browse"} {
		if n := jsonInt(nd.Get(key)); n > 0 {
			return n
		}
	}
	return jsonInt(item.Get("current.visit.cnt"))
}

// moodLikersFromItem 解析 qz_opcnt2 里自带的点赞人。list 经常是 [uin, 昵称, 标记] 数组。
func moodLikersFromItem(item gjson.Result) []MoodLiker {
	var out []MoodLiker
	seen := map[string]bool{}
	add := func(uin, name string) {
		uin = strings.TrimSpace(uin)
		name = strings.TrimSpace(name)
		key := uin
		if key == "" {
			key = name
		}
		if key == "" || seen[key] || len(out) >= likeListStoredMax {
			return
		}
		seen[key] = true
		out = append(out, MoodLiker{UIN: uin, Name: name})
	}
	item.Get("current.likedata.list").ForEach(func(_, x gjson.Result) bool {
		if x.IsArray() {
			add(jsonStringID(x.Get("0")), x.Get("1").String())
			return true
		}
		add(
			firstNonEmpty(jsonStringID(x.Get("fuin")), jsonStringID(x.Get("uin"))),
			firstNonEmpty(x.Get("nick").String(), x.Get("name").String(), x.Get("nickname").String()),
		)
		return true
	})
	return out
}

// jsonStringID 把接口里的 QQ 号收成字符串；likedata.list 里 uin 经常是数字。
func jsonStringID(v gjson.Result) string {
	if !v.Exists() {
		return ""
	}
	switch v.Type {
	case gjson.Number:
		return strconv.FormatInt(v.Int(), 10)
	default:
		return strings.TrimSpace(v.String())
	}
}

// parseMoodLikeList 解析 get_like_list_app 的 JSON/JSONP：like_uin_info 是名单，total_number 是总人数。
func parseMoodLikeList(raw string) ([]MoodLiker, int, error) {
	data, err := parseCGIBody(raw)
	if err != nil {
		return nil, 0, fmt.Errorf("解析说说点赞名单失败: %w", err)
	}
	res := gjson.Parse(data)
	if apiCode := res.Get("code").Int(); apiCode != 0 {
		return nil, 0, moodAPIError(apiCode, res)
	}

	total := jsonInt(res.Get("data.total_number"))
	if total == 0 {
		total = jsonInt(res.Get("data.total"))
	}

	var out []MoodLiker
	seen := map[string]bool{}
	// like_uin_info：fuin 是 QQ，nick 是昵称；备份时再按好友备注覆盖 Name。
	res.Get("data.like_uin_info").ForEach(func(_, x gjson.Result) bool {
		if len(out) >= likeListStoredMax {
			return false
		}
		p := MoodLiker{
			UIN:  strings.TrimSpace(firstNonEmpty(x.Get("fuin").String(), x.Get("uin").String())),
			Name: strings.TrimSpace(firstNonEmpty(x.Get("nick").String(), x.Get("name").String(), x.Get("nickname").String())),
		}
		key := p.UIN
		if key == "" {
			key = p.Name
		}
		if key == "" || seen[key] {
			return true
		}
		seen[key] = true
		out = append(out, p)
		return true
	})
	if total < len(out) {
		total = len(out) // 接口没给总数时，至少按已解析出的人数记
	}
	return out, total, nil
}

// uniqueMoodTIDs 去掉空 tid 和重复 tid，保持原顺序。
func uniqueMoodTIDs(tids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tids))
	for _, tid := range tids {
		tid = strings.TrimSpace(tid)
		if tid == "" || seen[tid] {
			continue
		}
		seen[tid] = true
		out = append(out, tid)
	}
	return out
}

// mergeMoodOpCnt 把 src 合进 dst；同一 tid 的赞数、浏览数取较大值，点赞人名单取更长的一份。
func mergeMoodOpCnt(dst, src map[string]MoodOpCnt) {
	if dst == nil {
		return
	}
	for tid, n := range src {
		dst[tid] = mergeOneMoodOpCnt(dst[tid], n)
	}
}

func mergeOneMoodOpCnt(old, next MoodOpCnt) MoodOpCnt {
	if next.Like < old.Like {
		next.Like = old.Like
	}
	if next.Visit < old.Visit {
		next.Visit = old.Visit
	}
	if len(next.Likers) < len(old.Likers) {
		next.Likers = old.Likers
	}
	return next
}

// IsMoodLoginError 判断点赞/说说接口是否因登录失效而失败。
func IsMoodLoginError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "登录已失效")
}
