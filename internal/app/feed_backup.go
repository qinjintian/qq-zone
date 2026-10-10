// 个人中心全部动态备份：按页拉取、补日志/赞/评论、下载配图，并随时写出查看页。

package app

import (
	"context"
	"fmt"
	nhttp "net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/qinjintian/qq-zone/internal/qzone"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"go.uber.org/zap"

	ihttp "github.com/qinjintian/qq-zone/internal/net/http"
)

// FeedBackup 把当前账号在个人中心看得到的好友动态存到本地。
type FeedBackup struct {
	client       *qzone.Client
	config       *Config
	logger       *zap.SugaredLogger
	results      DownloadResult
	notice       string
	spaceName    string
	reachedEnd   bool
	stoppedEarly bool
}

// NewFeedBackup 创建个人中心动态备份任务。
func NewFeedBackup(client *qzone.Client, config *Config, logger *zap.SugaredLogger) *FeedBackup {
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	return &FeedBackup{client: client, config: config, logger: logger}
}

// Backup 先下载最新的一小段动态，写好 index.html，再按 ask 决定要不要继续往前翻。
// ask 为空时只下一小段，方便测试。exclude 为真时，连续碰到已备份动态就停。
func (b *FeedBackup) Backup(ctx context.Context, targetUin string, exclude bool, ask FeedAskFunc) (*DownloadResult, error) {
	b.results = DownloadResult{}
	b.reachedEnd = false
	b.stoppedEarly = false
	root := feedRoot(targetUin)
	if err := os.MkdirAll(filepath.Join(root, "data"), os.ModePerm); err != nil {
		return nil, err
	}

	existing, _ := loadFeedBackup(root)
	known := map[string]MoodPost{}
	if existing != nil {
		existing.Posts = withoutFeedAds(existing.Posts)
		imagesChanged := upgradeFeedImages(existing.Posts)
		needMotion := b.client != nil && feedListNeedsMotion(existing.Posts)
		pendingVideo := feedListHasPendingVideo(existing.Posts)
		if imagesChanged || needMotion || pendingVideo {
			dp := mpb.NewWithContext(ctx)
			motionPersist, motionDownload := false, false
			if needMotion {
				motionPersist, motionDownload = b.repairFeedMotion(ctx, dp, existing.Posts)
			}
			if imagesChanged || motionDownload || pendingVideo {
				if err := b.downloadAllMedia(ctx, dp, root, targetUin, existing.Posts); err != nil && ctx.Err() == nil {
					b.logger.Warnf("配图和视频未全部完成: %v", err)
				}
			}
			dp.Wait()
			if imagesChanged || motionPersist || motionDownload || pendingVideo {
				if err := b.persist(root, targetUin, exclude, known, existing, nil); err != nil {
					return nil, err
				}
			}
		}
		for _, p := range existing.Posts {
			if p.TID != "" {
				known[p.TID] = p
			}
		}
	}

	cursor := qzone.FeedCursor{}
	seen := map[string]bool{}
	var fetched []MoodPost
	untilEnd := false
	b.logger.Info("正在拉取个人中心动态，请稍等......")

	for {
		if err := ctx.Err(); err != nil {
			b.persist(root, targetUin, exclude, known, existing, fetched)
			return &b.results, err
		}

		p := mpb.NewWithContext(ctx)
		batch, more, ferr := b.fetchPages(ctx, p, targetUin, exclude, known, seen, &cursor, feedDefaultPages)
		if ferr != nil && len(batch) == 0 && len(fetched) == 0 {
			p.Wait()
			return &b.results, ferr
		}
		if ferr != nil {
			b.logger.Warnf("拉取动态未全部完成: %v", ferr)
		}
		if ctx.Err() == nil && len(batch) > 0 {
			b.enrich(ctx, p, batch)
			b.applyRemarks(ctx, p, batch)
		}
		p.Wait()

		for i := range batch {
			if old, ok := known[batch[i].TID]; ok {
				batch[i] = keepExistingMedia(batch[i], old)
			}
		}
		if len(batch) > 0 && ctx.Err() == nil {
			dp := mpb.NewWithContext(ctx)
			if err := b.downloadAllMedia(ctx, dp, root, targetUin, batch); err != nil && ctx.Err() == nil {
				b.logger.Warnf("配图下载未全部完成: %v", err)
			}
			dp.Wait()
		}
		fetched = append(fetched, batch...)
		if err := b.persist(root, targetUin, exclude, known, existing, fetched); err != nil {
			return &b.results, err
		}

		if ctx.Err() != nil {
			return &b.results, ctx.Err()
		}
		if !more {
			b.reachedEnd = true
			b.logger.Info("个人中心目前能翻到的动态已经收完")
			break
		}
		if untilEnd {
			continue
		}
		if ask == nil {
			b.stoppedEarly = true
			break
		}
		switch ask(len(mergeFeed(fetched, existing, exclude)), b.oldestText(fetched, existing)) {
		case FeedChoiceMore:
			continue
		case FeedChoiceAll:
			untilEnd = true
			b.logger.Info("继续往前下载，直到没有更多动态")
			continue
		default:
			// 用户选择就到这里。写在 switch 里的 break 只会结束分支，停不了这个循环。
			b.stoppedEarly = true
		}
		break
	}

	if err := b.persist(root, targetUin, exclude, known, existing, fetched); err != nil {
		return &b.results, err
	}
	return &b.results, nil
}

// fetchPages 从 cursor 继续往前拉，最多收 limit 页还没备份过的动态。
// 整页都是已保存内容时不计入这个额度。more 为 false 表示个人中心没有更早的动态了。
func (b *FeedBackup) fetchPages(ctx context.Context, p *mpb.Progress, targetUin string, exclude bool, known map[string]MoodPost, seen map[string]bool, cursor *qzone.FeedCursor, limit int) ([]MoodPost, bool, error) {
	bar := p.AddBar(int64(limit),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name("拉取动态 ", decor.WC{W: 16, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d 页"),
		),
		mpb.AppendDecorators(decor.Percentage()),
	)

	var (
		posts      []MoodPost
		pages      int
		newPages   int
		emptyHits  int
		knownPages int
		more       = true
	)

	// 已经备份过的页直接翻过去，不占用「最新 5 页」的额度，这样下次可以接着往前收。
	for newPages < limit && knownPages < limit*8 {
		if err := ctx.Err(); err != nil {
			bar.Abort(true)
			return posts, more, err
		}
		page, err := b.client.GetFeedPage(ctx, *cursor)
		if err != nil {
			bar.Abort(true)
			return posts, more, err
		}
		pages++
		bar.Increment()
		if page.Nickname != "" && b.spaceName == "" {
			b.spaceName = page.Nickname
		}
		if b.config != nil && b.config.EnableMetadataExport && page.Raw != "" {
			rawDir := filepath.Join(feedRoot(targetUin), "raw")
			_ = os.MkdirAll(rawDir, os.ModePerm)
			_ = os.WriteFile(filepath.Join(rawDir, fmt.Sprintf("page_%04d.json", pages)), []byte(page.Raw), 0644)
		}

		parsed := postsFromFeedPage(page.HTML, page.Items, time.Now())
		if len(parsed) == 0 && len(strings.TrimSpace(page.HTML)) > 80 && len(page.Items) == 0 {
			b.logger.Warn("这一页有内容，但没有认出动态卡片。可先打开调试模式，再把 API 日志里的 GetFeedPage 响应发出来")
		}
		if len(parsed) == 0 {
			emptyHits++
			if emptyHits >= 2 || (page.HasMoreSet && !page.HasMore) {
				more = false
				break
			}
			b.advanceCursor(cursor, parsed, page)
			continue
		}
		emptyHits = 0

		fresh := 0
		oldest := int64(0)
		for _, post := range parsed {
			if post.Time > 0 && (oldest == 0 || post.Time < oldest) {
				oldest = post.Time
			}
			if post.TID != "" && seen[post.TID] {
				continue
			}
			if post.TID != "" {
				seen[post.TID] = true
			}
			if exclude && post.TID != "" {
				if _, ok := known[post.TID]; ok {
					continue
				}
			}
			fresh++
			posts = append(posts, post)
		}
		prevBegin := cursor.BeginTime
		b.advanceCursor(cursor, parsed, page)
		if page.HasMoreSet && !page.HasMore {
			more = false
			break
		}
		if oldest > 0 && cursor.BeginTime == prevBegin {
			more = false
			break
		}
		if fresh == 0 {
			knownPages++
			continue
		}
		newPages++
	}
	finishBar(bar)
	return posts, more, nil
}

// finishBar 收起一张还没走满的进度条。总数大于 0 时 SetTotal 不会生效，不中止的话 Wait 会一直停住。
func finishBar(bar *mpb.Bar) {
	if bar == nil {
		return
	}
	bar.Abort(true)
}

// advanceCursor 用本页最早一条的发表时间减 1 秒作为下一页起点，并带上接口返回的翻页参数。
func (b *FeedBackup) advanceCursor(cursor *qzone.FeedCursor, posts []MoodPost, page *qzone.FeedPage) {
	oldest := int64(0)
	for _, post := range posts {
		if post.Time > 0 && (oldest == 0 || post.Time < oldest) {
			oldest = post.Time
		}
	}
	if oldest == 0 && page != nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(page.LastFeedTime), 10, 64); err == nil {
			oldest = n
		}
	}
	if oldest > 1 {
		cursor.BeginTime = strconv.FormatInt(oldest-1, 10)
	}
	if page != nil && page.ExternParam != "" {
		cursor.ExternParam = page.ExternParam
	}
	if cursor.Page < 1 {
		cursor.Page = 1
	}
	cursor.Page++
}

// enrich 补日志正文、说说的点赞人和评论。动态列表里通常只有赞数，没有评论正文。
func (b *FeedBackup) enrich(ctx context.Context, p *mpb.Progress, posts []MoodPost) {
	if b.client == nil || len(posts) == 0 {
		return
	}
	bar := p.AddBar(int64(len(posts)),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name("补全赞和评论 ", decor.WC{W: 16, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(decor.Percentage()),
	)
	defer finishBar(bar)

	byAuthor := map[string][]string{}
	moodIdx := map[int]bool{}
	done := map[int]bool{}
	for i := range posts {
		post := &posts[i]
		if post.FeedType == "blog" && post.BlogID != "" && post.Author.UIN != "" {
			b.enrichBlog(ctx, post)
		}
		if (post.FeedType == "shuoshuo" || post.FeedType == "repost") && post.OriginTID != "" && post.Author.UIN != "" {
			byAuthor[post.Author.UIN] = append(byAuthor[post.Author.UIN], post.OriginTID)
			moodIdx[i] = true
		}
		if ctx.Err() != nil {
			return
		}
	}
	for uin, tids := range byAuthor {
		if ctx.Err() != nil {
			return
		}
		counts, err := b.client.GetMoodLikeCounts(ctx, uin, uniqueStrings(tids))
		if err != nil {
			b.logger.Debugf("拉取点赞数失败 %s: %v", uin, err)
			counts = map[string]qzone.MoodOpCnt{}
		}
		for i := range posts {
			if !moodIdx[i] || posts[i].Author.UIN != uin {
				continue
			}
			post := &posts[i]
			if n, ok := counts[post.OriginTID]; ok {
				if n.Like > 0 {
					post.LikeCount = n.Like
				}
				if n.Visit > 0 {
					post.VisitCount = n.Visit
				}
				if len(post.Likes) == 0 && len(n.Likers) > 0 {
					post.Likes = likersToPeople(n.Likers)
				}
			}
			if post.LikeCount > 0 && len(post.Likes) == 0 {
				likers, total, lerr := b.client.GetMoodLikeList(ctx, uin, post.OriginTID)
				if lerr != nil {
					b.logger.Debugf("拉取点赞名单失败 %s: %v", post.OriginTID, lerr)
				} else {
					applyLikers(post, likers, total)
				}
			}
			if needFeedComments(post) {
				detail, derr := b.client.GetMoodDetail(ctx, uin, post.OriginTID)
				if derr != nil {
					b.logger.Debugf("拉取评论失败 %s: %v", post.OriginTID, derr)
				} else if detail != nil {
					comments := parseMoodComments(detail.Comments)
					if len(comments) > 0 {
						post.Comments = comments
					}
					if post.CommentCount < len(post.Comments) {
						post.CommentCount = len(post.Comments)
					}
					if feedNeedsMotionCheck(post) {
						applyFeedMoodMedia(post, detail.Item)
						post.MediaMotionChecked = true
					}
				}
			}
			if feedNeedsMotionCheck(post) {
				b.fillFeedMotion(ctx, post)
			}
			bar.Increment()
			done[i] = true
			delete(moodIdx, i)
		}
	}
	for i := range posts {
		if done[i] {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		post := &posts[i]
		if post.FeedType != "shuoshuo" && post.FeedType != "repost" {
			unikey := strings.TrimSpace(post.Unikey)
			if unikey == "" && post.FeedType == "blog" && post.BlogID != "" && post.Author.UIN != "" {
				unikey = fmt.Sprintf("http://user.qzone.qq.com/%s/blog/%s", post.Author.UIN, post.BlogID)
			}
			if unikey != "" && (post.LikeCount > 0 || !post.LikeKnown) && len(post.Likes) == 0 {
				likers, total, err := b.client.GetLikeListByUnikey(ctx, post.Author.UIN, unikey)
				if err != nil {
					b.logger.Debugf("拉取点赞名单失败: %v", err)
				} else {
					applyLikers(post, likers, total)
				}
			}
		}
		bar.Increment()
	}
}

// enrichBlog 拉取单篇日志的正文和评论，写回这条动态。
func (b *FeedBackup) enrichBlog(ctx context.Context, post *MoodPost) {
	detail, err := b.client.GetBlogDetail(ctx, post.Author.UIN, post.BlogID)
	if err != nil {
		b.logger.Debugf("补拉日志 %s 失败: %v", post.BlogID, err)
	} else if detail != nil {
		applyBlogDetail(post, detail.Title, detail.HTML)
	}
	if !needFeedComments(post) {
		return
	}
	raws, err := b.client.GetBlogComments(ctx, post.Author.UIN, post.BlogID)
	if err != nil {
		b.logger.Debugf("拉取日志评论 %s 失败: %v", post.BlogID, err)
		return
	}
	comments := adaptFeedComments(raws)
	if len(comments) > 0 {
		post.Comments = comments
	}
	if post.CommentCount < len(post.Comments) {
		post.CommentCount = len(post.Comments)
	}
}

// feedListHasPendingVideo 判断已保存动态里有没有已经识别出、但本地文件还没落下来的视频。
// 上次下载中断时，下次备份靠这个把 mp4 补上，不必再问一遍详情。
func feedListHasPendingVideo(posts []MoodPost) bool {
	for i := range posts {
		if mediaPendingVideo(posts[i].Media) || (posts[i].Repost != nil && mediaPendingVideo(posts[i].Repost.Media)) {
			return true
		}
	}
	return false
}

// mediaPendingVideo 判断这份媒体是不是已经有播放地址、但 Path 还是空的视频。
func mediaPendingVideo(list []MoodMedia) bool {
	for _, m := range list {
		if m.Type == "video" && m.Path == "" && (m.URL != "" || len(m.URLs) > 0) {
			return true
		}
	}
	return false
}

// feedListNeedsMotion 判断已保存动态里还有没有没确认过的实况图或视频。
// 有的话备份开始时会先问说说详情，避免增量跳过已经存成静态图的卡片。
func feedListNeedsMotion(posts []MoodPost) bool {
	for i := range posts {
		if feedNeedsMotionCheck(&posts[i]) {
			return true
		}
	}
	return false
}

// repairFeedMotion 对已保存、还没确认过的说说再问一次详情，把实况图和视频从静态封面改回来。
// 返回值分别表示要不要重写 backup.json，以及有没有新的视频需要下载。
func (b *FeedBackup) repairFeedMotion(ctx context.Context, p *mpb.Progress, posts []MoodPost) (bool, bool) {
	var idx []int
	for i := range posts {
		if feedNeedsMotionCheck(&posts[i]) {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 || b.client == nil {
		return false, false
	}
	bar := p.AddBar(int64(len(idx)),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name("识别实况和视频 ", decor.WC{W: 16, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(decor.Percentage()),
	)
	defer finishBar(bar)

	persist, download := false, false
	for _, i := range idx {
		if ctx.Err() != nil {
			return persist, download
		}
		changed, replaced := b.fillFeedMotion(ctx, &posts[i])
		if changed {
			persist = true
		}
		if replaced {
			download = true
		}
		bar.Increment()
	}
	return persist, download
}

// fillFeedMotion 用说说详情把这条动态里的实况图和视频补上。
// 第一个返回值表示记录有改动（含「已确认没有视频」），第二个返回值表示确实换成了视频、需要下载。
// 没有权限时也记成已确认，避免每次备份都打同一条看不见的说说。
func (b *FeedBackup) fillFeedMotion(ctx context.Context, post *MoodPost) (bool, bool) {
	if b == nil || b.client == nil || !feedNeedsMotionCheck(post) {
		return false, false
	}
	item, err := b.client.GetMoodItem(ctx, post.Author.UIN, post.OriginTID)
	if err != nil {
		b.logger.Debugf("识别实况图失败 %s: %v", post.OriginTID, err)
		if qzone.IsMoodPermissionError(err) {
			post.MediaMotionChecked = true
			return true, false
		}
		return false, false
	}
	replaced := applyFeedMoodMedia(post, item)
	post.MediaMotionChecked = true
	return true, replaced
}

// needFeedComments 判断这条动态还要不要再请求评论。列表已标明没有评论，或本地条数已经齐，就不再打接口。
func needFeedComments(post *MoodPost) bool {
	if post == nil {
		return false
	}
	if post.CommentKnown && post.CommentCount == 0 {
		return false
	}
	if len(post.Comments) > 0 && post.CommentKnown && len(post.Comments) >= post.CommentCount {
		return false
	}
	return true
}

// applyLikers 把点赞名单写回动态。人数取接口总数和名单长度中较大的一个。
func applyLikers(post *MoodPost, likers []qzone.MoodLiker, total int) {
	if len(likers) > 0 {
		post.Likes = likersToPeople(likers)
	}
	if total > post.LikeCount {
		post.LikeCount = total
	}
	if post.LikeCount < len(post.Likes) {
		post.LikeCount = len(post.Likes)
	}
}

// likersToPeople 把接口返回的点赞人转成查看页使用的人员信息。
func likersToPeople(likers []qzone.MoodLiker) []MoodPerson {
	out := make([]MoodPerson, 0, len(likers))
	for _, x := range likers {
		out = append(out, MoodPerson{UIN: x.UIN, Name: x.Name})
	}
	return out
}

// applyRemarks 用当前登录账号的好友备注替换动态里的昵称。备注拉失败时仍保留原来的名字。
func (b *FeedBackup) applyRemarks(ctx context.Context, p *mpb.Progress, posts []MoodPost) {
	if len(posts) == 0 {
		return
	}
	if b.client == nil {
		applyFriendNamesToPosts(posts, nil)
		return
	}
	wait := waitSpinner(p, "正在对应好友备注")
	names, err := b.client.GetFriendDisplayNames(ctx)
	stopWaitSpinner(wait)
	if err != nil {
		b.logger.Warnf("拉取好友备注失败，动态仍显示昵称: %v", err)
		applyFriendNamesToPosts(posts, nil)
		return
	}
	applyFriendNamesToPosts(posts, names)
}

// persist 合并新旧动态并下载头像，然后重写 backup.json 和 index.html。每收完一批就写一次，中途停下也能打开已有内容。
func (b *FeedBackup) persist(root, targetUin string, exclude bool, known map[string]MoodPost, existing *FeedBackupFile, fetched []MoodPost) error {
	merged := mergeFeed(fetched, existing, exclude)
	if len(merged) == 0 && existing == nil {
		merged = fetched
	}
	avatars := b.downloadAvatars(context.Background(), root, merged)
	applyAvatars(merged, avatars)

	nickname := ""
	if b.client != nil && targetUin == b.client.QQ {
		nickname = b.client.Nickname
	}
	if nickname == "" {
		nickname = b.spaceName
	}
	if nickname == "" && existing != nil {
		nickname = existing.Nickname
	}

	newCount := 0
	for _, post := range fetched {
		if post.TID == "" {
			newCount++
			continue
		}
		if _, ok := known[post.TID]; !ok {
			newCount++
		}
	}
	skipped := 0
	if exclude && existing != nil {
		skipped = len(merged) - newCount
		if skipped < 0 {
			skipped = 0
		}
	}

	if existing != nil && existing.Notice != "" && len(fetched) == 0 && !b.reachedEnd && !b.stoppedEarly {
		b.notice = existing.Notice
	} else {
		b.notice = feedNotice(b.reachedEnd, b.stoppedEarly)
	}
	file := &FeedBackupFile{
		UIN:      targetUin,
		Nickname: nickname,
		Notice:   b.notice,
		Posts:    merged,
	}
	if err := saveFeedBackup(root, file); err != nil {
		return fmt.Errorf("写入 backup.json 失败: %w", err)
	}
	if err := writeFeedViewer(root, file); err != nil {
		return fmt.Errorf("生成查看页失败: %w", err)
	}
	atomic.StoreUint64(&b.results.Total, uint64(len(merged)))
	atomic.StoreUint64(&b.results.Success, uint64(len(merged)))
	atomic.StoreUint64(&b.results.NewAdded, uint64(newCount))
	atomic.StoreUint64(&b.results.Skipped, uint64(skipped))
	return nil
}

// mergeFeed 把本轮新拉到的动态和本地已有记录合成一份，供保存和统计跳过条数。
func mergeFeed(fetched []MoodPost, existing *FeedBackupFile, exclude bool) []MoodPost {
	if existing == nil {
		return append([]MoodPost{}, fetched...)
	}
	if exclude || len(fetched) > 0 {
		return mergeMoodPosts(fetched, existing.Posts)
	}
	return existing.Posts
}

// feedNotice 按这次是翻到头还是用户中途停下，生成查看页顶部的说明。
func feedNotice(reachedEnd, stoppedEarly bool) string {
	if reachedEnd && !stoppedEarly {
		return "个人中心目前能翻到的动态已经收在这里，含好友备注、正文、图片视频、时间和赞评。再早的内容可能已经不在信息中心里。"
	}
	return "这里是个人中心已经保存的好友动态。网页上继续往下拉还能看到的更早内容，回到程序里再备份即可接着收。"
}

// oldestText 找出目前已保存动态里最早的日期，询问要不要继续时显示给用户。
func (b *FeedBackup) oldestText(fetched []MoodPost, existing *FeedBackupFile) string {
	var oldest int64
	consider := func(posts []MoodPost) {
		for _, post := range posts {
			if post.Time > 0 && (oldest == 0 || post.Time < oldest) {
				oldest = post.Time
			}
		}
	}
	consider(fetched)
	if existing != nil {
		consider(existing.Posts)
	}
	if oldest <= 0 {
		return ""
	}
	return time.Unix(oldest, 0).In(shanghaiLoc).Format("2006-01-02")
}

// downloadAllMedia 并发下载这批动态的图片和视频。本地已经有内容的文件会跳过。
func (b *FeedBackup) downloadAllMedia(ctx context.Context, p *mpb.Progress, root, targetUin string, posts []MoodPost) error {
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
		if ctx.Err() != nil {
			bar.Abort(true)
			wg.Wait()
			return ctx.Err()
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
	finishBar(bar)
	return nil
}

// downloadOneMedia 下载单份图片或视频，并把本地相对路径写回去。图片按原图、大高清图、小图的顺序尝试。
func (b *FeedBackup) downloadOneMedia(ctx context.Context, root, targetUin string, posts []MoodPost, idx int, m *MoodMedia) {
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
	cands := append([]string{m.URL}, m.URLs...)
	if m.Type == "video" {
		cands = compactURLs(cands...)
	} else {
		cands = compactFeedPhotoURLs(cands...)
	}
	res, err := b.downloadCandidates(ctx, targetUin, cands, dest, filepath.Base(rel), m.Type == "video")
	if err != nil {
		b.results.addFailedItem(FailedItem{
			Album:     "全部动态",
			Name:      rel,
			Error:     err.Error(),
			TargetUin: targetUin,
			Kind:      FailedKindFeed,
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
	if created > 0 && m.Path != "" {
		t := time.Unix(created, 0)
		_ = os.Chtimes(filepath.Join(root, filepath.FromSlash(m.Path)), t, t)
	}
	b.noteMediaSuccess(m.Type == "video")
}

// downloadCandidates 按给定顺序逐个尝试地址，第一张下载成功就停止。
func (b *FeedBackup) downloadCandidates(ctx context.Context, targetUin string, urls []string, dest, name string, isVideo bool) (map[string]interface{}, error) {
	var lastErr error
	tried := map[string]bool{}
	for _, raw := range urls {
		if isVideo {
			raw = normalizeMediaURL(raw)
		} else {
			raw = prepareFeedPhotoURL(raw)
		}
		if raw == "" || raw == "||" || tried[raw] {
			continue
		}
		tried[raw] = true
		res, err := b.downloadOneURL(ctx, targetUin, raw, dest, name, isVideo)
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

// downloadOneURL 以个人中心页面为来源下载一条地址。视频链接失效时会去掉 Cookie 再试一次。
func (b *FeedBackup) downloadOneURL(ctx context.Context, targetUin, rawURL, dest, name string, isVideo bool) (map[string]interface{}, error) {
	headers := map[string]string{
		"cookie":     b.client.Cookie,
		"user-agent": qzone.UserAgent,
		"referer":    fmt.Sprintf("https://user.qzone.qq.com/%s/infocenter", targetUin),
	}
	if isVideo {
		headers["Accept"] = "*/*"
		headers["Accept-Encoding"] = "identity;q=1, *;q=0"
	}
	var opts []ihttp.DownloadOption
	if isVideo {
		opts = append(opts, ihttp.WithFatalStatuses(nhttp.StatusForbidden, nhttp.StatusNotFound, nhttp.StatusGone))
	}
	var res map[string]interface{}
	var err error
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

// downloadAvatars 把头像保存为 avatars/<QQ号>.jpg。单个头像失败不影响这条动态，查看页会改用名字首字。
func (b *FeedBackup) downloadAvatars(ctx context.Context, root string, posts []MoodPost) map[string]string {
	people := collectPeople(posts)
	dir := filepath.Join(root, "avatars")
	_ = os.MkdirAll(dir, os.ModePerm)
	out := map[string]string{}
	if len(people) == 0 || b.client == nil {
		return out
	}
	var mu sync.Mutex
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, person := range people {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(person MoodPerson) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
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
			if _, err := b.downloadOneURL(ctx, person.UIN, avatarURL, dest, person.UIN+".jpg", false); err != nil {
				b.logger.Debugf("头像 %s 下载失败: %v", person.UIN, err)
				return
			}
			mu.Lock()
			out[person.UIN] = rel
			mu.Unlock()
		}(person)
	}
	wg.Wait()
	return out
}

// trackWrittenBytes 把这次写出的字节数累加到任务摘要里。
func (b *FeedBackup) trackWrittenBytes(delta int64) {
	if delta > 0 {
		atomic.AddUint64(&b.results.BytesDone, uint64(delta))
	}
}

// noteMediaSuccess 累加成功的图片或视频数量。动态条数在保存查看页时另行统计。
func (b *FeedBackup) noteMediaSuccess(isVideo bool) {
	if isVideo {
		atomic.AddUint64(&b.results.VideoCount, 1)
	} else {
		atomic.AddUint64(&b.results.ImageCount, 1)
	}
}

// RetryFailed 只重下动态里失败的配图或视频，然后刷新查看页。
func (b *FeedBackup) RetryFailed(ctx context.Context, targetUin string, items []FailedItem) (*DownloadResult, error) {
	b.results = DownloadResult{}
	root := feedRoot(targetUin)
	file, err := loadFeedBackup(root)
	if err != nil {
		return nil, fmt.Errorf("读取已有动态备份失败: %w", err)
	}
	byTID := map[string]*MoodPost{}
	for i := range file.Posts {
		if file.Posts[i].TID != "" {
			byTID[file.Posts[i].TID] = &file.Posts[i]
		}
	}
	atomic.StoreUint64(&b.results.Total, uint64(len(items)))
	p := mpb.NewWithContext(ctx)
	bar := p.AddBar(int64(len(items)),
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name("重试动态配图 ", decor.WC{W: 18, C: decor.DindentRight}),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(decor.Percentage()),
	)
	for _, item := range items {
		if ctx.Err() != nil {
			bar.Abort(true)
			p.Wait()
			return &b.results, ctx.Err()
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
		isVideo := item.IsVideo
		if media != nil && media.Type == "video" {
			isVideo = true
		}
		res, dlErr := b.downloadCandidates(ctx, targetUin, urls, dest, filepath.Base(rel), isVideo)
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
	avatars := b.downloadAvatars(ctx, root, file.Posts)
	applyAvatars(file.Posts, avatars)
	p.Wait()
	if err := saveFeedBackup(root, file); err != nil {
		return &b.results, err
	}
	if err := writeFeedViewer(root, file); err != nil {
		return &b.results, err
	}
	return &b.results, nil
}

// uniqueStrings 去掉空值和重复的说说编号，便于按作者一次拉取点赞数。
func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
