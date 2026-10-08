// 把个人中心动态写成可双击打开的离线时间线。

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qinjintian/qq-zone/internal/app/viewer"
)

func writeFeedViewer(root string, file *FeedBackupFile) error {
	if file == nil {
		return fmt.Errorf("empty feed backup")
	}

	assetsDir := filepath.Join(root, "assets")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(assetsDir, os.ModePerm); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, os.ModePerm); err != nil {
		return err
	}
	if err := copyViewerAsset("assets/viewer.css", filepath.Join(assetsDir, "viewer.css")); err != nil {
		return err
	}
	if err := copyViewerAsset("assets/viewer.js", filepath.Join(assetsDir, "viewer.js")); err != nil {
		return err
	}

	posts := stripMoodURLs(file.Posts)
	for i := range posts {
		posts[i] = repairMoodPost(posts[i])
		posts[i].Unikey = ""
	}
	byYear := map[string][]MoodPost{}
	for _, post := range posts {
		year := "未知"
		if post.Time > 0 {
			year = time.Unix(post.Time, 0).In(shanghaiLoc).Format("2006")
		}
		byYear[year] = append(byYear[year], post)
	}
	years := make([]string, 0, len(byYear))
	for year := range byYear {
		years = append(years, year)
	}
	sort.Slice(years, func(i, j int) bool { return years[i] > years[j] })

	var scriptTags strings.Builder
	for _, year := range years {
		filename := fmt.Sprintf("posts-%s.js", sanitizePath(year))
		payload, err := json.Marshal(byYear[year])
		if err != nil {
			return err
		}
		js := fmt.Sprintf("window.__QQZONE_YEARS__=window.__QQZONE_YEARS__||{};\nwindow.__QQZONE_YEARS__[%q]=%s;\n", year, payload)
		if err := os.WriteFile(filepath.Join(dataDir, filename), []byte(js), 0644); err != nil {
			return err
		}
		fmt.Fprintf(&scriptTags, "  <script src=\"data/%s\"></script>\n", filename)
	}

	meta := moodViewerMeta{
		UIN:        file.UIN,
		Nickname:   file.Nickname,
		ExportedAt: file.ExportedAt,
		Total:      len(file.Posts),
		Years:      years,
		Notice:     file.Notice,
		Kind:       "feed",
	}
	if meta.ExportedAt == "" {
		meta.ExportedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dataDir, "meta.js"), []byte("window.__QQZONE_META__="+string(metaJSON)+";\n"), 0644); err != nil {
		return err
	}

	index, err := viewer.Files.ReadFile("index.html")
	if err != nil {
		return err
	}
	index = bytes.Replace(index, []byte("<!--YEAR_SCRIPTS-->"), []byte(scriptTags.String()), 1)
	title := strings.TrimSpace(file.Nickname)
	if title == "" {
		title = "个人中心动态"
	} else {
		title = title + " 的个人中心动态"
	}
	index = bytes.Replace(index, []byte("QQ 空间说说备份"), []byte(title), 1)
	return os.WriteFile(filepath.Join(root, feedIndexFile), index, 0644)
}
