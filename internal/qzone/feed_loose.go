package qzone

import (
	"strings"
	"unicode/utf8"
)

// applyLooseFeed 解析 feeds3_html_more 的宽松响应。
// 外层 code/message 是 JSON，data 的值却是 JS 对象，卡片 HTML 写在 html:'\x3C...' 里。
func applyLooseFeed(body string, page *FeedPage) {
	if page == nil {
		return
	}
	js := looseDataObject(body)
	if js == "" {
		return
	}
	var htmls []string
	var oldest string
	seenHTML := map[string]bool{}
	iterJSKeys(js, func(key, value string) {
		switch key {
		case "hasMoreFeeds", "hasMore", "hasmore":
			if page.HasMoreSet {
				return
			}
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "true", "1":
				page.HasMore, page.HasMoreSet = true, true
			case "false", "0":
				page.HasMore, page.HasMoreSet = false, true
			}
		case "externparam", "externParam":
			if page.ExternParam == "" {
				page.ExternParam = strings.TrimSpace(value)
			}
		case "begintime", "lastFeedTime":
			if page.LastFeedTime == "" && isUnixSeconds(value) {
				page.LastFeedTime = strings.TrimSpace(value)
			}
		case "abstime":
			value = strings.TrimSpace(value)
			if isUnixSeconds(value) && (oldest == "" || value < oldest) {
				oldest = value
			}
		case "html":
			value = strings.TrimSpace(value)
			if value == "" || seenHTML[value] {
				return
			}
			seenHTML[value] = true
			htmls = append(htmls, value)
		}
	})
	if page.LastFeedTime == "" {
		page.LastFeedTime = oldest
	}
	if len(htmls) == 0 {
		return
	}
	page.HTML = normalizeFeedHTML(strings.Join(htmls, "\n"))
}

func looseDataObject(body string) string {
	idx := strings.Index(body, `"data"`)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimLeft(body[idx+len(`"data"`):], " \t\r\n:")
	if rest == "" || rest[0] != '{' {
		return ""
	}
	return matchJSValue(rest)
}

func isUnixSeconds(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 10 || len(s) > 13 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func matchJSValue(s string) string {
	if s == "" {
		return ""
	}
	end := walkJSValue(s, 0)
	if end <= 0 || end > len(s) {
		return ""
	}
	return s[:end]
}

func walkJSValue(s string, i int) int {
	if i >= len(s) {
		return -1
	}
	switch s[i] {
	case '{', '[':
		open := s[i]
		close := byte('}')
		if open == '[' {
			close = ']'
		}
		depth := 0
		for i < len(s) {
			if s[i] == '\'' || s[i] == '"' {
				_, next := consumeJSString(s, i)
				if next < 0 {
					return -1
				}
				i = next
				continue
			}
			switch s[i] {
			case open:
				depth++
			case close:
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return -1
	case '\'', '"':
		_, next := consumeJSString(s, i)
		return next
	default:
		j := i
		for j < len(s) && s[j] != ',' && s[j] != '}' && s[j] != ']' {
			j++
		}
		return j
	}
}

func iterJSKeys(src string, on func(key, value string)) {
	if on == nil || src == "" {
		return
	}
	i := 0
	for i < len(src) {
		if src[i] == '\'' || src[i] == '"' {
			_, next := consumeJSString(src, i)
			if next < 0 {
				return
			}
			i = next
			continue
		}
		if !isIdentStart(src[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(src) && isIdentCont(src[j]) {
			j++
		}
		key := src[i:j]
		k := skipSpace(src, j)
		if k >= len(src) || src[k] != ':' {
			i = j
			continue
		}
		k = skipSpace(src, k+1)
		if k >= len(src) {
			return
		}
		if src[k] == '\'' || src[k] == '"' {
			val, next := consumeJSString(src, k)
			if next < 0 {
				return
			}
			on(key, val)
			i = next
			continue
		}
		// 对象和数组继续往里扫，html、abstime 都在内层。
		if src[k] == '{' || src[k] == '[' {
			i = k + 1
			continue
		}
		next := walkJSValue(src, k)
		if next < 0 {
			return
		}
		on(key, strings.TrimSpace(src[k:next]))
		i = next
	}
}

func consumeJSString(s string, i int) (string, int) {
	if i >= len(s) {
		return "", -1
	}
	quote := s[i]
	i++
	var b strings.Builder
	for i < len(s) {
		c := s[i]
		if c == '\\' {
			i = appendJSEscape(&b, s, i+1)
			continue
		}
		if c == quote {
			return b.String(), i + 1
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), -1
}

func appendJSEscape(b *strings.Builder, s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case 'n':
		b.WriteByte('\n')
		return i + 1
	case 'r':
		b.WriteByte('\r')
		return i + 1
	case 't':
		b.WriteByte('\t')
		return i + 1
	case 'b':
		b.WriteByte('\b')
		return i + 1
	case 'f':
		b.WriteByte('\f')
		return i + 1
	case 'x':
		if i+2 < len(s) {
			if v, ok := hexByte(s[i+1], s[i+2]); ok {
				b.WriteByte(v)
				return i + 3
			}
		}
	case 'u':
		if i+4 < len(s) {
			if r, ok := hexRune(s[i+1 : i+5]); ok {
				if utf8.ValidRune(r) {
					b.WriteRune(r)
				}
				return i + 5
			}
		}
	}
	b.WriteByte(s[i])
	return i + 1
}

func hexByte(a, b byte) (byte, bool) {
	hi, ok1 := hexVal(a)
	lo, ok2 := hexVal(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	return hi<<4 | lo, true
}

func hexRune(s string) (rune, bool) {
	if len(s) != 4 {
		return 0, false
	}
	var v rune
	for i := 0; i < 4; i++ {
		n, ok := hexVal(s[i])
		if !ok {
			return 0, false
		}
		v = v<<4 | rune(n)
	}
	return v, true
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func skipSpace(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}
