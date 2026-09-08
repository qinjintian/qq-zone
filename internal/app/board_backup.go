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
 * @FileName: board_backup.go
 * @Description: [留言板备份引擎：分页拉取、回复与配图下载、增量合并]
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

	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/qinjintian/qq-zone/internal/qzone"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"go.uber.org/zap"
)

// BoardBackup 负责把指定空间的留言板拉到本地并生成查看页。
type BoardBackup struct {
	client  *qzone.Client      // 已登录的空间客户端
	config  *Config            // 并发、是否落盘 raw JSON 等
	logger  *zap.SugaredLogger // 任务日志
	results DownloadResult     // Total/Success 记留言条数，Failed 记配图失败

	notice    string      // 无权查看等提示，写入 backup.json 给查看页顶部展示
	spaceName string      // 被备份空间的昵称，查看页标题用
	spaceUin  string      // 主人 QQ，寄语头像用
	intro     *BoardIntro // 本轮接口里拿到的主人寄语
}

// NewBoardBackup 创建留言板备份任务。
func NewBoardBackup(client *qzone.Client, config *Config, logger *zap.SugaredLogger) *BoardBackup {
	return &BoardBackup{
		client: client,
		config: config,
		logger: logger,
	}
}

// Backup 拉取 targetUin 的留言、下载配图并生成 index.html。
// exclude=true 时从最新往回拉，连续碰到已备份的留言 id 就停。
func (b *BoardBackup) Backup(ctx context.Context, targetUin string, exclude bool) (*DownloadResult, error) {
	b.results = DownloadResult{}
	root := boardRoot(targetUin)
	if err := os.MkdirAll(filepath.Join(root, "data"), os.ModePerm); err != nil {
		return nil, err
	}

	// 增量对比用：本地 backup.json 里已有的留言 id → 整条记录（配图路径要靠它回写）。
	existing, _ := loadBoardBackup(root)
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
	fetched, fetchErr := b.fetchMessages(ctx, p, targetUin, exclude, known)
	// 无权查看且一行都没拉到时，保留本地已有备份，只把提示写到查看页，不当整次任务崩溃。
	blocked := qzone.IsBoardPermissionError(fetchErr) && len(fetched) == 0
	if qzone.IsBoardPermissionError(fetchErr) {
		if b.notice == "" {
			b.notice = "没有权限查看该留言板，已跳过。"
		}
		b.logger.Warn(b.notice)
		fetchErr = nil
		if blocked && existing != nil {
			exclude = true // 走增量合并，避免全量刷新把本地留言清空
		}
	}
	if fetchErr != nil && len(fetched) == 0 {
		p.Wait()
		return &b.results, fetchErr
	}
	if fetchErr != nil {
		b.logger.Warnf("拉取留言未全部完成: %v", fetchErr)
	}

	var posts []MoodPost
	if exclude && existing != nil {
		// 新拉到的覆盖同 id（例如上次配图失败），其余沿用本地。
		posts = mergeMoodPosts(fetched, existing.Posts)
		for i, post := range posts {
			if old, ok := known[post.TID]; ok {
				posts[i] = keepBoardMedia(post, old)
			}
		}
	} else if existing != nil {
		// 全量刷新文案，但已下好的配图路径拷回去，避免重复下载。
		for i, post := range fetched {
			if old, ok := known[post.TID]; ok {
				fetched[i] = keepBoardMedia(post, old)
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

	newCount := 0
	for _, post := range fetched {
		if _, ok := known[post.TID]; !ok {
			newCount++
		}
	}
	skippedPosts := 0
	if existing != nil && exclude {
		skippedPosts = len(posts) - newCount
		if skippedPosts < 0 {
			skippedPosts = 0
		}
	}

	atomic.StoreUint64(&b.results.Total, uint64(len(posts)))
	atomic.StoreUint64(&b.results.NewAdded, uint64(newCount))
	atomic.StoreUint64(&b.results.Skipped, uint64(skippedPosts))

	// 旧备份可能把气泡 ID 当成配图、表情还是相对路径；下载前先修一遍。
	for i := range posts {
		posts[i] = repairBoardPost(posts[i])
	}

	// 完全无权时不要再打好友备注和配图接口，免得雪上加霜。
	if !blocked {
		// 接口只给昵称；查看页要和空间网页一样显示好友备注。
		b.applyFriendRemarks(ctx, p, posts)

		if err := b.downloadAllMedia(ctx, p, root, targetUin, posts); err != nil && ctx.Err() == nil {
			b.logger.Warnf("配图下载过程出现中断: %v", err)
		}

		avatars := b.downloadAvatars(ctx, p, root, posts, b.intro)
		applyAvatars(posts, avatars)
		if b.intro != nil {
			if path, ok := avatars[b.intro.Author.UIN]; ok {
				b.intro.Author.Avatar = path
			}
		}
	}

	p.Wait()

	intro := b.intro
	if intro == nil && existing != nil {
		intro = existing.Intro // 本轮接口没给寄语时，沿用上次备份的
	}

	file := &BoardBackupFile{
		UIN:      targetUin,
		Nickname: nickname,
		Posts:    posts,
		Notice:   b.notice,
		Intro:    intro,
	}
	if err := saveBoardBackup(root, file); err != nil {
		return &b.results, fmt.Errorf("写入 backup.json 失败: %w", err)
	}
	if err := writeBoardViewer(root, file); err != nil {
		return &b.results, fmt.Errorf("生成查看页失败: %w", err)
	}

	success := uint64(len(posts))
	if b.results.Failed > uint64(len(posts)) {
		success = 0
	}
	atomic.StoreUint64(&b.results.Success, success)
	return &b.results, nil
}

// RetryFailed 只重试失败配图，不重新翻整本留言板。
// 留言没有单条详情接口，地址以 backup.json / 失败项上记下的 URL 为准。
func (b *BoardBackup) RetryFailed(ctx context.Context, targetUin string, items []FailedItem) (*DownloadResult, error) {
	b.results = DownloadResult{}
	root := boardRoot(targetUin)
	file, err := loadBoardBackup(root)
	if err != nil {
		return nil, fmt.Errorf("读取已有留言板备份失败: %w", err)
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
			decor.Name("重试留言配图 ", decor.WC{W: 18, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(decor.Percentage()),
	)

	for _, item := range items {
		select {
		case <-ctx.Done():
			bar.Abort(true)
			p.Wait()
			return &b.results, ctx.Err()
		default:
		}

		post := byTID[item.MoodTID]
		media := findMoodMedia(post, item)
		urls := compactURLs(item.MediaURL)
		if media != nil {
			urls = compactURLs(append([]string{media.URL, item.MediaURL}, media.URLs...)...)
		}

		rel := item.Name
		if rel == "" && media != nil {
			rel = media.Path
		}
		// 没有落盘路径，或路径想跳出备份目录，都不下，避免写到别处。
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

		res, dlErr := b.downloadFirstURL(ctx, targetUin, urls, dest, filepath.Base(rel))
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
		b.noteImageSuccess()
		atomic.AddUint64(&b.results.Success, 1)
		bar.Increment()
	}

	avatars := b.downloadAvatars(ctx, p, root, file.Posts, file.Intro)
	applyAvatars(file.Posts, avatars)
	p.Wait()

	if err := saveBoardBackup(root, file); err != nil {
		return &b.results, err
	}
	if err := writeBoardViewer(root, file); err != nil {
		return &b.results, err
	}
	return &b.results, nil
}

// fetchMessages 分页拉留言。增量模式下连续碰到 3 条本地已有 id 就停，
// 避免把整本历史再翻一遍；start 必须按 20 递增，接口不会按实际返回条数对齐。
func (b *BoardBackup) fetchMessages(ctx context.Context, p *mpb.Progress, targetUin string, exclude bool, known map[string]MoodPost) ([]MoodPost, error) {
	wait := waitSpinner(p, "正在请求留言板")
	first, err := b.client.GetMsgBoard(ctx, targetUin, 0)
	stopWaitSpinner(wait)
	if err != nil {
		if qzone.IsBoardPermissionError(err) {
			b.notice = "没有权限查看该留言板，已跳过。"
		}
		return nil, err
	}

	if len(first.Items) == 0 && first.Total <= 0 {
		b.logger.Infof("空间 [%s] 没有可见的留言", targetUin)
		b.captureIntro(first)
		return nil, nil
	}

	// 进度只按实际保存的留言走。total 含不可见条目时，用它当总数会把条卡死。
	bar := p.AddBar(1,
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name("拉取留言 ", decor.WC{W: 16, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(
			decor.Percentage(),
			decor.Name(" "),
			decor.Elapsed(decor.ET_STYLE_MMSS),
		),
	)

	var (
		posts     []MoodPost
		start     = 0
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
			page, err = b.client.GetMsgBoard(ctx, targetUin, start)
			if err != nil {
				bar.Abort(true)
				return posts, err
			}
		}
		pageIdx++
		b.captureIntro(page)

		if b.config != nil && b.config.EnableMetadataExport && page.Raw != "" {
			rawDir := filepath.Join(boardRoot(targetUin), "raw")
			_ = os.MkdirAll(rawDir, os.ModePerm)
			_ = os.WriteFile(filepath.Join(rawDir, fmt.Sprintf("page_%04d.json", start)), []byte(page.Raw), 0644)
		}

		if len(page.Items) == 0 {
			emptyHits++
			if emptyHits >= 3 || (page.Total > 0 && start >= page.Total) {
				break
			}
			start += qzone.BoardPageSize()
			continue
		}
		emptyHits = 0

		stop := false
		for _, item := range page.Items {
			msg := parseBoardMessage(item)
			if msg.TID == "" {
				continue
			}
			if exclude {
				if _, ok := known[msg.TID]; ok {
					knownHits++
					if knownHits >= 3 {
						stop = true
						break
					}
					continue
				}
				knownHits = 0 // 中间又出现新留言，说明还没接到本地已有区间
			}
			posts = append(posts, msg)
			// 总数始终比已保存条数多 1，避免中途 current==total 把条关掉。
			bar.SetTotal(int64(len(posts))+1, false)
			bar.Increment()
		}

		if stop {
			b.logger.Infof("增量模式：已遇到本地已有留言，停止继续往前翻页")
			break
		}

		start += qzone.BoardPageSize()
		if page.Total > 0 && start >= page.Total {
			break
		}
	}

	if len(posts) == 0 {
		bar.Abort(true)
	} else {
		bar.SetTotal(int64(len(posts)), true)
	}
	return posts, nil
}

// captureIntro 记下空间昵称和主人寄语。寄语只在部分页返回，拿到一次就不再覆盖。
func (b *BoardBackup) captureIntro(page *qzone.MsgBoardPage) {
	if page == nil {
		return
	}
	if page.Nickname != "" && b.spaceName == "" {
		b.spaceName = page.Nickname
	}
	if page.SignUin != "" && b.spaceUin == "" {
		b.spaceUin = page.SignUin
	}
	if b.intro != nil {
		return
	}
	uin := page.SignUin
	if uin == "" {
		uin = b.spaceUin
	}
	b.intro = parseBoardIntro(page.Nickname, uin, page.Sign)
}

// applyFriendRemarks 用登录账号的好友备注覆盖留言者/回复者昵称，和空间网页一致。
func (b *BoardBackup) applyFriendRemarks(ctx context.Context, p *mpb.Progress, posts []MoodPost) {
	if b.client == nil {
		applyFriendNamesToPosts(posts, nil)
		return
	}
	if len(posts) == 0 && (b.intro == nil || b.intro.Author.UIN == "") {
		return
	}
	wait := waitSpinner(p, "正在拉取好友备注")
	names, err := b.client.GetFriendDisplayNames(ctx)
	stopWaitSpinner(wait)
	if err != nil {
		b.logger.Warnf("拉取好友备注失败，留言仍显示昵称: %v", err)
		applyFriendNamesToPosts(posts, nil)
		return
	}
	applyFriendNamesToPosts(posts, names)
	if b.intro != nil && b.intro.Author.UIN != "" {
		if n := strings.TrimSpace(names[b.intro.Author.UIN]); n != "" {
			b.intro.Author.Name = n
		}
	}
}

// downloadAllMedia 并发下载留言和回复里的配图；本地已有非空文件会跳过。
func (b *BoardBackup) downloadAllMedia(ctx context.Context, p *mpb.Progress, root, targetUin string, posts []MoodPost) error {
	ptrs := collectMediaPtrs(posts)
	if len(ptrs) == 0 {
		return nil
	}

	bar := p.AddBar(int64(len(ptrs)),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name("下载留言配图 ", decor.WC{W: 16, C: decor.DindentRight}),
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

// downloadOneMedia 下载单张配图并回写相对路径；失败记进 FailedItems 且清空 Path，查看页就不会链到半截文件。
func (b *BoardBackup) downloadOneMedia(ctx context.Context, root, targetUin string, posts []MoodPost, idx int, m *MoodMedia) {
	if m == nil {
		return
	}
	urls := compactURLs(append([]string{m.URL}, m.URLs...)...)
	if len(urls) == 0 || !looksLikeRemoteMediaURL(urls[0]) {
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
			b.noteImageSuccess()
			return
		}
	}

	res, err := b.downloadFirstURL(ctx, targetUin, urls, dest, filepath.Base(rel))
	if err != nil {
		b.results.addFailedItem(FailedItem{
			Album:     "留言板",
			Name:      rel,
			Error:     err.Error(),
			TargetUin: targetUin,
			Kind:      FailedKindBoard,
			MoodTID:   ownerTID,
			MediaURL:  urls[0],
			MediaID:   m.ID,
		})
		m.Path = "" // 清空，避免查看页链到 0 字节文件
		return
	}

	if res != nil {
		if path, ok := res["path"].(string); ok {
			if relPath, relErr := filepath.Rel(root, path); relErr == nil {
				m.Path = filepath.ToSlash(relPath)
			}
		}
	}
	if created > 0 && m.Path != "" {
		t := time.Unix(created, 0)
		_ = os.Chtimes(filepath.Join(root, filepath.FromSlash(m.Path)), t, t)
	}
	b.noteImageSuccess()
}

// downloadFirstURL 按候选地址依次下载，成功即停；全部失败返回最后一个错误。
func (b *BoardBackup) downloadFirstURL(ctx context.Context, targetUin string, urls []string, dest, name string) (map[string]interface{}, error) {
	var lastErr error
	tried := map[string]bool{}
	for _, raw := range urls {
		raw = normalizeMediaURL(raw)
		if raw == "" || tried[raw] {
			continue
		}
		tried[raw] = true
		res, err := b.downloadOneURL(ctx, targetUin, raw, dest, name)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的下载地址")
	}
	return nil, lastErr
}

// downloadOneURL 下载单条地址。Referer 指到空间首页，留言板图不走 /mood。
func (b *BoardBackup) downloadOneURL(ctx context.Context, targetUin, rawURL, dest, name string) (map[string]interface{}, error) {
	rawURL = normalizeMediaURL(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("没有可用的下载地址")
	}
	headers := map[string]string{
		"cookie":     b.client.Cookie,
		"user-agent": qzone.UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s", targetUin),
	}
	return b.client.Http.Download(ctx, rawURL, dest, headers, 3, 600, nil, name, name, b.trackWrittenBytes)
}

// downloadAvatars 把出现过的 QQ 头像下到 avatars/<uin>.jpg；失败不记任务失败，查看页会改用首字母。
func (b *BoardBackup) downloadAvatars(ctx context.Context, p *mpb.Progress, root string, posts []MoodPost, intro *BoardIntro) map[string]string {
	people := collectPeople(posts)
	// 寄语作者可能没在留言列表里出现，单独补进待下载名单。
	if intro != nil && intro.Author.UIN != "" {
		found := false
		for _, x := range people {
			if x.UIN == intro.Author.UIN {
				found = true
				break
			}
		}
		if !found {
			people = append(people, intro.Author)
		}
	}
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
			_, err := b.downloadOneURL(ctx, person.UIN, avatarURL, dest, person.UIN+".jpg")
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

// trackWrittenBytes 累加本次实际写入磁盘的字节数，给任务报告用。
func (b *BoardBackup) trackWrittenBytes(delta int64) {
	if delta <= 0 {
		return
	}
	atomic.AddUint64(&b.results.BytesDone, uint64(delta))
}

// noteImageSuccess 配图（含增量跳过的已有文件）计数 +1，任务报告里的「图片」用这个。
func (b *BoardBackup) noteImageSuccess() {
	atomic.AddUint64(&b.results.ImageCount, 1)
}
