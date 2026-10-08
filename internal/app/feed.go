// 个人中心全部动态的本地备份：目录、backup.json，以及菜单里的查看入口。

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	feedBackupVersion = 1
	feedDirName       = "dongtai"
	feedBackupFile    = "backup.json"
	feedIndexFile     = "index.html"
	feedDefaultPages  = 5 // 先下最新 5 页，大约 50 条，然后再问要不要继续
)

// FeedChoice 是下完默认页数后，用户在终端里的选择。
type FeedChoice int

const (
	FeedChoiceStop FeedChoice = iota // 就到这里
	FeedChoiceMore                   // 再下一小段
	FeedChoiceAll                    // 一直往前，直到没有更多
)

// FeedAskFunc 在每一小段下载完、查看页已经写好之后调用。返回用户要不要继续。
type FeedAskFunc func(saved int, oldest string) FeedChoice

// FeedBackupFile 是个人中心动态 backup.json。
type FeedBackupFile struct {
	Version    int        `json:"version"`
	UIN        string     `json:"uin"`
	Nickname   string     `json:"nickname"`
	ExportedAt string     `json:"exported_at"`
	Total      int        `json:"total"`
	Notice     string     `json:"notice,omitempty"`
	Posts      []MoodPost `json:"posts"`
}

// FeedViewerInfo 描述一份已经生成的动态查看页。
type FeedViewerInfo struct {
	UIN        string
	Nickname   string
	Total      int
	ExportedAt string
	IndexHTML  string
}

// FeedDefaultPages 返回每次连续拉取的页数。一页大约 10 条。
func FeedDefaultPages() int { return feedDefaultPages }

func feedRoot(targetUin string) string {
	return filepath.Join("storage", "qzone", targetUin, feedDirName)
}

func feedBackupPath(root string) string {
	return filepath.Join(root, "data", feedBackupFile)
}

func feedIndexPath(root string) string {
	return filepath.Join(root, feedIndexFile)
}

func loadFeedBackup(root string) (*FeedBackupFile, error) {
	data, err := os.ReadFile(feedBackupPath(root))
	if err != nil {
		return nil, err
	}
	var file FeedBackupFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return &file, nil
}

func saveFeedBackup(root string, file *FeedBackupFile) error {
	if file == nil {
		return nil
	}
	file.Version = feedBackupVersion
	file.ExportedAt = time.Now().Format("2006-01-02 15:04:05")
	file.Total = len(file.Posts)
	for i := range file.Posts {
		file.Posts[i] = repairMoodPost(file.Posts[i])
	}
	sortMoodPosts(file.Posts)

	path := feedBackupPath(root)
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// ListFeedViewers 扫描已生成的个人中心动态查看页。
func ListFeedViewers() []FeedViewerInfo {
	matches, err := filepath.Glob(filepath.Join("storage", "qzone", "*", feedDirName, feedIndexFile))
	if err != nil {
		return nil
	}
	out := make([]FeedViewerInfo, 0, len(matches))
	for _, index := range matches {
		root := filepath.Dir(index)
		info := FeedViewerInfo{
			IndexHTML: index,
			UIN:       filepath.Base(filepath.Dir(root)),
		}
		if file, err := loadFeedBackup(root); err == nil && file != nil {
			info.UIN = file.UIN
			info.Nickname = file.Nickname
			info.Total = file.Total
			info.ExportedAt = file.ExportedAt
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ExportedAt == out[j].ExportedAt {
			return out[i].UIN > out[j].UIN
		}
		return out[i].ExportedAt > out[j].ExportedAt
	})
	return out
}

// IsFeedTask 判断任务是否来自个人中心动态备份。重试任务要看失败项上的 Kind。
func IsFeedTask(record *TaskRecord) bool {
	if record == nil {
		return false
	}
	if record.Mode == TaskModeFeed {
		return true
	}
	if len(record.OpenFailedItems) > 0 && record.OpenFailedItems[0].Kind == FailedKindFeed {
		return true
	}
	if len(record.FailedItems) > 0 && record.FailedItems[0].Kind == FailedKindFeed {
		return true
	}
	return false
}

// FeedRoot 返回动态备份目录。
func FeedRoot(targetUin string) string { return feedRoot(targetUin) }

// FeedIndexPath 返回动态查看页 index.html。
func FeedIndexPath(targetUin string) string { return feedIndexPath(feedRoot(targetUin)) }
