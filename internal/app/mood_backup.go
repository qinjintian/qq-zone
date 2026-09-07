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
 * @FileName: mood_backup.go
 * @Description: [说说备份引擎：分页拉取、评论补全、配图/视频下载、增量合并]
 */

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	nhttp "net/http"

	ihttp "github.com/qinjintian/qq-zone/internal/net/http"
	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/qinjintian/qq-zone/internal/qzone"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"go.uber.org/zap"
)

// MoodBackup 负责把指定空间的说说拉到本地并生成查看页。
type MoodBackup struct {
	client  *qzone.Client
	config  *Config
	logger  *zap.SugaredLogger
	results DownloadResult

	archived  bool
	notice    string
	spaceName string
}

// NewMoodBackup 创建说说备份任务。
func NewMoodBackup(client *qzone.Client, config *Config, logger *zap.SugaredLogger) *MoodBackup {
	return &MoodBackup{
		client: client,
		config: config,
		logger: logger,
	}
}

// Backup 拉取 targetUin 的说说、下载媒体并生成 index.html。
// exclude=true 时从最新往回拉，碰到已备份的 tid 就停，只补新媒体文件。
func (b *MoodBackup) Backup(ctx context.Context, targetUin string, exclude bool) (*DownloadResult, error) {
	b.results = DownloadResult{}
	root := moodRoot(targetUin)
	if err := os.MkdirAll(filepath.Join(root, "data"), os.ModePerm); err != nil {
		return nil, err
	}

	existing, _ := loadMoodBackup(root)
	known := map[string]MoodPost{}
	if existing != nil {
		for _, p := range existing.Posts {
			if p.TID != "" {
				known[p.TID] = p
			}
		}
	}

	nickname := ""
	if b.client != nil && targetUin == b.client.QQ {
		nickname = b.client.Nickname
	}

	p := mpb.NewWithContext(ctx)
	fetched, err := b.fetchPosts(ctx, p, targetUin, exclude, known)
	if err != nil && len(fetched) == 0 {
		p.Wait()
		return &b.results, err
	}
	if err != nil {
		b.logger.Warnf("拉取说说未全部完成: %v", err)
	}

	var posts []MoodPost
	if exclude && existing != nil {
		// 新拉到的覆盖同 tid（例如上次媒体失败），其余沿用本地。
		posts = mergeMoodPosts(fetched, existing.Posts)
		for i, post := range posts {
			if old, ok := known[post.TID]; ok {
				posts[i] = keepExistingMedia(post, old)
			}
		}
	} else if existing != nil {
		for i, post := range fetched {
			if old, ok := known[post.TID]; ok {
				fetched[i] = keepExistingMedia(post, old)
			}
		}
		posts = fetched
	} else {
		posts = fetched
	}

	if nickname == "" {
		nickname = b.spaceName
	}
	if nickname == "" && existing != nil {
		nickname = existing.Nickname
	}
	if nickname == "" && len(posts) > 0 {
		nickname = posts[0].Author.Name
	}

	newCount := 0
	for _, post := range fetched {
		if _, ok := known[post.TID]; !ok {
			newCount++
		}
	}
	skippedPosts := 0
	if existing != nil {
		skippedPosts = len(existing.Posts)
		if !exclude {
			skippedPosts = 0
		} else {
			skippedPosts = len(posts) - newCount
		}
	}

	atomic.StoreUint64(&b.results.Total, uint64(len(posts)))
	atomic.StoreUint64(&b.results.NewAdded, uint64(newCount))
	atomic.StoreUint64(&b.results.Skipped, uint64(skippedPosts))

	if err := b.downloadAllMedia(ctx, p, root, targetUin, posts); err != nil && ctx.Err() == nil {
		b.logger.Warnf("媒体下载过程出现中断: %v", err)
	}

	avatars := b.downloadAvatars(ctx, p, root, posts)
	applyAvatars(posts, avatars)

	p.Wait()

	file := &MoodBackupFile{
		UIN:      targetUin,
		Nickname: nickname,
		Posts:    posts,
		Archived: b.archived,
		Notice:   b.notice,
	}
	if err := saveMoodBackup(root, file); err != nil {
		return &b.results, fmt.Errorf("写入 backup.json 失败: %w", err)
	}
	if err := writeMoodViewer(root, file); err != nil {
		return &b.results, fmt.Errorf("生成查看页失败: %w", err)
	}

	success := uint64(len(posts))
	if b.results.Failed > uint64(len(posts)) {
		success = 0
	}
	atomic.StoreUint64(&b.results.Success, success)
	return &b.results, nil
}

// RetryFailed 针对说说失败项：按 tid 重新拉详情换新地址，再下丢失的文件，最后重写查看页。
func (b *MoodBackup) RetryFailed(ctx context.Context, targetUin string, items []FailedItem) (*DownloadResult, error) {
	b.results = DownloadResult{}
	root := moodRoot(targetUin)
	file, err := loadMoodBackup(root)
	if err != nil {
		return nil, fmt.Errorf("读取已有说说备份失败: %w", err)
	}

	byTID := map[string]*MoodPost{}
	for i := range file.Posts {
		p := &file.Posts[i]
		if p.TID != "" {
			byTID[p.TID] = p
		}
	}

	atomic.StoreUint64(&b.results.Total, uint64(len(items)))
	p := mpb.NewWithContext(ctx)
	bar := p.AddBar(int64(len(items)),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name("重试说说媒体 ", decor.WC{W: 18, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(decor.Percentage()),
	)

	refreshed := map[string]bool{}
	for _, item := range items {
		select {
		case <-ctx.Done():
			bar.Abort(true)
			p.Wait()
			return &b.results, ctx.Err()
		default:
		}

		tid := item.MoodTID
		if tid != "" && !refreshed[tid] {
			if raw, ferr := b.fetchOneRaw(ctx, targetUin, tid); ferr == nil {
				parsed := parseMoodPost(raw.Item, raw.Comments)
				if old, ok := byTID[tid]; ok {
					parsed = keepExistingMedia(parsed, *old)
					*old = parsed
					byTID[tid] = old
				}
				refreshed[tid] = true
			} else {
				b.logger.Debugf("刷新说说 %s 失败: %v", tid, ferr)
			}
		}

		post := byTID[tid]
		media := findMoodMedia(post, item)
		urls := compactURLs(item.MediaURL)
		videoID := ""
		isVideo := item.IsVideo
		if media != nil {
			urls = compactURLs(append([]string{media.URL, item.MediaURL}, media.URLs...)...)
			videoID = media.VideoID
			isVideo = media.Type == "video"
		}

		rel := item.Name
		if rel == "" && media != nil {
			rel = media.Path
		}
		if rel == "" || strings.Contains(rel, "..") {
			b.results.addFailedItem(item)
			bar.Increment()
			continue
		}
		dest := filepath.Join(root, filepath.FromSlash(rel))
		if util.Exists(dest) {
			if fi, statErr := os.Stat(dest); statErr == nil && fi.Size() > 0 {
				atomic.AddUint64(&b.results.Success, 1)
				atomic.AddUint64(&b.results.Skipped, 1)
				bar.Increment()
				continue
			}
		}

		res, dlErr := b.downloadCandidates(ctx, targetUin, urls, videoID, dest, filepath.Base(rel), isVideo)
		if dlErr != nil {
			fail := item
			fail.Error = dlErr.Error()
			b.results.addFailedItem(fail)
			bar.Increment()
			continue
		}
		if media != nil && res != nil {
			if path, ok := res["path"].(string); ok {
				if relPath, relErr := filepath.Rel(root, path); relErr == nil {
					media.Path = filepath.ToSlash(relPath)
				}
			}
		}
		b.noteMediaSuccess(isVideo)
		atomic.AddUint64(&b.results.Success, 1)
		bar.Increment()
	}

	avatars := b.downloadAvatars(ctx, p, root, file.Posts)
	applyAvatars(file.Posts, avatars)
	p.Wait()

	if err := saveMoodBackup(root, file); err != nil {
		return &b.results, err
	}
	if err := writeMoodViewer(root, file); err != nil {
		return &b.results, err
	}
	return &b.results, nil
}

func findMoodMedia(post *MoodPost, item FailedItem) *MoodMedia {
	if post == nil {
		return nil
	}
	match := func(m *MoodMedia) bool {
		if item.MediaID != "" && m.ID == item.MediaID {
			return true
		}
		if item.Name != "" && m.Path == filepath.ToSlash(item.Name) {
			return true
		}
		if item.MediaURL != "" && (m.URL == item.MediaURL) {
			return true
		}
		return false
	}
	for i := range post.Media {
		if match(&post.Media[i]) {
			return &post.Media[i]
		}
	}
	if post.Repost != nil {
		for i := range post.Repost.Media {
			if match(&post.Repost.Media[i]) {
				return &post.Repost.Media[i]
			}
		}
	}
	var walk func(cs []MoodComment) *MoodMedia
	walk = func(cs []MoodComment) *MoodMedia {
		for i := range cs {
			for j := range cs[i].Media {
				if match(&cs[i].Media[j]) {
					return &cs[i].Media[j]
				}
			}
			if found := walk(cs[i].Replies); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(post.Comments)
}

func (b *MoodBackup) fetchPosts(ctx context.Context, p *mpb.Progress, targetUin string, exclude bool, known map[string]MoodPost) ([]MoodPost, error) {
	first, err := b.client.GetMoodList(ctx, targetUin, 0)
	if err != nil {
		return nil, err
	}

	totalHint := first.Total
	if totalHint <= 0 {
		totalHint = len(first.Messages)
	}
	if totalHint <= 0 {
		b.logger.Infof("空间 [%s] 没有可见的说说", targetUin)
		return nil, nil
	}

	bar := p.AddBar(int64(totalHint),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name("拉取说说 ", decor.WC{W: 16, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(decor.Percentage()),
	)

	var (
		posts     []MoodPost
		pos       = 0
		emptyHits = 0
		knownHits = 0
		page      = first
		pageIdx   = 0
	)

	for {
		select {
		case <-ctx.Done():
			bar.Abort(true)
			return posts, ctx.Err()
		default:
		}

		if pageIdx > 0 {
			page, err = b.client.GetMoodList(ctx, targetUin, pos)
			if err != nil {
				bar.Abort(true)
				return posts, err
			}
		}
		pageIdx++

		if b.config != nil && b.config.EnableMetadataExport && page.Raw != "" {
			rawDir := filepath.Join(moodRoot(targetUin), "raw")
			_ = os.MkdirAll(rawDir, os.ModePerm)
			_ = os.WriteFile(filepath.Join(rawDir, fmt.Sprintf("page_%04d.json", pos)), []byte(page.Raw), 0644)
		}

		if page.Nickname != "" && b.spaceName == "" {
			b.spaceName = page.Nickname
		}

		if page.Archived && len(page.Messages) == 0 {
			b.archived = true
			b.notice = "更早的说说可能因空间封存或权限限制无法继续获取，已保存当前可见部分。"
			b.logger.Warn(b.notice)
			break
		}

		if len(page.Messages) == 0 {
			emptyHits++
			if emptyHits >= 3 || (page.Total > 0 && pos >= page.Total) {
				break
			}
			pos += qzone.MoodPageSize()
			continue
		}
		emptyHits = 0

		stop := false
		for _, msg := range page.Messages {
			tid := strings.TrimSpace(msg.Get("tid").String())
			if exclude && tid != "" {
				if _, ok := known[tid]; ok {
					knownHits++
					if knownHits >= 3 {
						stop = true
						break
					}
					continue
				}
				knownHits = 0
			}

			post := parseMoodPost(msg, nil)
			if needMoodDetail(msg, post) {
				if detail, dErr := b.client.GetMoodDetail(ctx, targetUin, post.TID); dErr != nil {
					b.logger.Debugf("补全说说 %s 失败: %v", post.TID, dErr)
				} else if detail != nil {
					post = parseMoodPost(detail.Item, detail.Comments)
					if post.TID == "" {
						post.TID = tid
					}
				}
			}
			if needMoodPics(post) {
				if urls, pErr := b.client.GetMoodPics(ctx, targetUin, post.TID); pErr != nil {
					b.logger.Debugf("补拉说说配图 %s 失败: %v", post.TID, pErr)
				} else {
					appendMoodPicURLs(&post, urls)
				}
			}
			posts = append(posts, post)
			bar.Increment()
		}

		if stop {
			b.logger.Infof("增量模式：已遇到本地已有说说，停止继续往前翻页")
			break
		}

		pos += qzone.MoodPageSize()
		if page.Total > 0 && pos >= page.Total {
			break
		}
	}

	if len(posts) == 0 {
		bar.SetTotal(0, true)
	} else {
		bar.SetCurrent(int64(len(posts)))
		bar.SetTotal(int64(len(posts)), true)
	}
	return posts, nil
}

func (b *MoodBackup) fetchOneRaw(ctx context.Context, targetUin, tid string) (*qzone.MoodDetail, error) {
	return b.client.GetMoodDetail(ctx, targetUin, tid)
}

func (b *MoodBackup) downloadAllMedia(ctx context.Context, p *mpb.Progress, root, targetUin string, posts []MoodPost) error {
	ptrs := collectMediaPtrs(posts)
	if len(ptrs) == 0 {
		return nil
	}

	bar := p.AddBar(int64(len(ptrs)),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name("下载配图/视频 ", decor.WC{W: 16, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(decor.Percentage()),
	)

	limit := 10
	if b.config != nil && b.config.TaskLimit > 0 {
		limit = b.config.TaskLimit
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup

	for i, media := range ptrs {
		select {
		case <-ctx.Done():
			bar.Abort(true)
			wg.Wait()
			return ctx.Err()
		default:
		}

		wg.Add(1)
		go func(idx int, m *MoodMedia) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			b.downloadOneMedia(ctx, root, targetUin, posts, idx, m)
			bar.Increment()
		}(i, media)
	}

	wg.Wait()
	bar.SetTotal(int64(len(ptrs)), true)
	return nil
}

func (b *MoodBackup) downloadOneMedia(ctx context.Context, root, targetUin string, posts []MoodPost, idx int, m *MoodMedia) {
	if m == nil {
		return
	}

	ownerTID := mediaOwnerTID(posts, idx, m)
	created := mediaOwnerTime(posts, idx, m)
	rel := m.Path
	if rel == "" {
		rel = moodMediaRelPath(m, ownerTID, created, idx)
		m.Path = rel
	}
	dest := filepath.Join(root, filepath.FromSlash(rel))

	if util.Exists(dest) {
		if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
			b.noteMediaSuccess(m.Type == "video")
			return
		}
	}

	res, err := b.downloadCandidates(ctx, targetUin, compactURLs(append([]string{m.URL}, m.URLs...)...), m.VideoID, dest, filepath.Base(rel), m.Type == "video")
	if err != nil {
		b.results.addFailedItem(FailedItem{
			Album:     "说说",
			Name:      rel,
			Error:     err.Error(),
			TargetUin: targetUin,
			Kind:      FailedKindShuoShuo,
			MoodTID:   ownerTID,
			MediaURL:  m.URL,
			MediaID:   m.ID,
			IsVideo:   m.Type == "video",
		})
		m.Path = ""
		return
	}

	if res != nil {
		if path, ok := res["path"].(string); ok {
			if relPath, relErr := filepath.Rel(root, path); relErr == nil {
				m.Path = filepath.ToSlash(relPath)
			}
		}
	}

	if created > 0 {
		t := time.Unix(created, 0)
		_ = os.Chtimes(filepath.Join(root, filepath.FromSlash(m.Path)), t, t)
	}
	b.noteMediaSuccess(m.Type == "video")
}

func mediaOwnerTID(posts []MoodPost, _ int, target *MoodMedia) string {
	for i := range posts {
		p := &posts[i]
		for j := range p.Media {
			if &p.Media[j] == target {
				return p.TID
			}
		}
		if p.Repost != nil {
			for j := range p.Repost.Media {
				if &p.Repost.Media[j] == target {
					return p.TID
				}
			}
		}
		var walk func([]MoodComment) bool
		walk = func(cs []MoodComment) bool {
			for k := range cs {
				for j := range cs[k].Media {
					if &cs[k].Media[j] == target {
						return true
					}
				}
				if walk(cs[k].Replies) {
					return true
				}
			}
			return false
		}
		if walk(p.Comments) {
			return p.TID
		}
	}
	return ""
}

func mediaOwnerTime(posts []MoodPost, _ int, target *MoodMedia) int64 {
	for i := range posts {
		p := &posts[i]
		for j := range p.Media {
			if &p.Media[j] == target {
				return p.Time
			}
		}
		if p.Repost != nil {
			for j := range p.Repost.Media {
				if &p.Repost.Media[j] == target {
					return p.Time
				}
			}
		}
		var walk func([]MoodComment) bool
		walk = func(cs []MoodComment) bool {
			for k := range cs {
				for j := range cs[k].Media {
					if &cs[k].Media[j] == target {
						return true
					}
				}
				if walk(cs[k].Replies) {
					return true
				}
			}
			return false
		}
		if walk(p.Comments) {
			return p.Time
		}
	}
	return 0
}

func moodMediaRelPath(m *MoodMedia, tid string, ts int64, idx int) string {
	t := time.Unix(ts, 0).In(shanghaiLoc)
	if ts <= 0 {
		t = time.Now().In(shanghaiLoc)
	}
	hash := util.MD5(tid + m.ID + m.URL)
	if len(hash) < 24 {
		hash = hash + "0000000000000000"
	}
	prefix := "IMG"
	ext := ".jpg"
	switch m.Type {
	case "video":
		prefix = "VID"
		ext = ".mp4"
	case "voice":
		prefix = "AUD"
		ext = ".mp3"
	}
	name := fmt.Sprintf("%s_%s_%s_%02d%s", prefix, t.Format("20060102"), hash[8:24], idx%100, ext)
	return filepath.ToSlash(filepath.Join("media", t.Format("2006"), t.Format("01"), name))
}

func (b *MoodBackup) downloadCandidates(ctx context.Context, targetUin string, urls []string, videoID, dest, name string, isVideo bool) (map[string]interface{}, error) {
	var lastErr error
	tried := map[string]bool{}

	try := func(raw string) (map[string]interface{}, error) {
		raw = normalizeMediaURL(raw)
		if raw == "" || tried[raw] {
			return nil, fmt.Errorf("empty url")
		}
		tried[raw] = true
		return b.downloadOneURL(ctx, targetUin, raw, dest, name, isVideo)
	}

	for _, u := range urls {
		res, err := try(u)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !isVideo && !ihttp.IsDeadURL(err) {
			continue
		}
	}

	if isVideo && videoID != "" {
		for _, cand := range b.client.ResolveTencentVideo(ctx, videoID) {
			res, err := try(cand.URL)
			if err == nil {
				return res, nil
			}
			lastErr = err
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的下载地址")
	}
	return nil, lastErr
}

func (b *MoodBackup) downloadOneURL(ctx context.Context, targetUin, rawURL, dest, name string, isVideo bool) (map[string]interface{}, error) {
	headers := map[string]string{
		"cookie":     b.client.Cookie,
		"user-agent": qzone.UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/mood", targetUin),
	}
	if isVideo {
		headers["Accept"] = "*/*"
		headers["Accept-Encoding"] = "identity;q=1, *;q=0"
	}

	opts := []ihttp.DownloadOption{}
	if isVideo {
		opts = append(opts, ihttp.WithFatalStatuses(
			nhttp.StatusForbidden,
			nhttp.StatusNotFound,
			nhttp.StatusGone,
		))
	}

	var (
		res map[string]interface{}
		err error
	)
	if ihttp.IsHLSURL(rawURL) {
		res, err = b.client.Http.DownloadHLS(ctx, rawURL, dest, headers, nil, name, name, b.trackWrittenBytes, opts...)
	} else {
		retry := 3
		if isVideo {
			retry = 2
		}
		res, err = b.client.Http.Download(ctx, rawURL, dest, headers, retry, 600, nil, name, name, b.trackWrittenBytes, opts...)
	}

	if err != nil && isVideo && ihttp.IsDeadURL(err) && headers["cookie"] != "" {
		delete(headers, "cookie")
		if ihttp.IsHLSURL(rawURL) {
			res, err = b.client.Http.DownloadHLS(ctx, rawURL, dest, headers, nil, name, name, b.trackWrittenBytes, opts...)
		} else {
			res, err = b.client.Http.Download(ctx, rawURL, dest, headers, 1, 600, nil, name, name, b.trackWrittenBytes, opts...)
		}
	}
	return res, err
}

func (b *MoodBackup) downloadAvatars(ctx context.Context, p *mpb.Progress, root string, posts []MoodPost) map[string]string {
	people := collectPeople(posts)
	dir := filepath.Join(root, "avatars")
	_ = os.MkdirAll(dir, os.ModePerm)

	out := map[string]string{}
	var mu sync.Mutex
	if len(people) == 0 {
		return out
	}

	bar := p.AddBar(int64(len(people)),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name("下载头像 ", decor.WC{W: 16, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(decor.Percentage()),
	)

	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, person := range people {
		select {
		case <-ctx.Done():
			bar.Abort(true)
			wg.Wait()
			return out
		default:
		}
		wg.Add(1)
		go func(person MoodPerson) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer bar.Increment()

			rel := filepath.ToSlash(filepath.Join("avatars", person.UIN+".jpg"))
			dest := filepath.Join(root, filepath.FromSlash(rel))
			if util.Exists(dest) {
				if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
					mu.Lock()
					out[person.UIN] = rel
					mu.Unlock()
					return
				}
			}

			avatarURL := fmt.Sprintf("https://q1.qlogo.cn/g?b=qq&nk=%s&s=140", person.UIN)
			_, err := b.downloadOneURL(ctx, person.UIN, avatarURL, dest, person.UIN+".jpg", false)
			if err != nil {
				b.logger.Debugf("头像 %s 下载失败: %v", person.UIN, err)
				return
			}
			mu.Lock()
			out[person.UIN] = rel
			mu.Unlock()
		}(person)
	}
	wg.Wait()
	bar.SetTotal(int64(len(people)), true)
	return out
}

func (b *MoodBackup) trackWrittenBytes(delta int64) {
	if delta <= 0 {
		return
	}
	atomic.AddUint64(&b.results.BytesDone, uint64(delta))
}

func (b *MoodBackup) noteMediaSuccess(isVideo bool) {
	if isVideo {
		atomic.AddUint64(&b.results.VideoCount, 1)
	} else {
		atomic.AddUint64(&b.results.ImageCount, 1)
	}
}

// MoodRoot 返回说说备份目录，给 CLI 打印路径用。
func MoodRoot(targetUin string) string {
	return moodRoot(targetUin)
}

// MoodIndexPath 返回查看页路径。
func MoodIndexPath(targetUin string) string {
	return moodIndexPath(moodRoot(targetUin))
}
