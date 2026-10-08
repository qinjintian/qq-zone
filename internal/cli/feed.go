// 个人中心全部动态的终端交互：先下最新几页，再问要不要继续。

package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/fatih/color"
	"github.com/qinjintian/qq-zone/internal/app"
)

// handleFeedBackup 备份当前账号在个人中心看到的好友动态，并生成本地查看页。
func (c *CLI) handleFeedBackup(ctx context.Context) {
	targetUin := c.client.QQ
	c.logger.Infof("🚀 正在为 [%s] 配置个人中心动态备份...", color.YellowString(targetUin))
	c.logger.Info("备份的是你在个人中心看到的好友动态，包含头像、备注、正文、图片视频、时间和赞评。")
	c.logger.Infof("默认先下载最新 %d 页（每页约 10 条）。这一批写好查看页后，再问你要不要继续往前翻。", app.FeedDefaultPages())

	var answers struct {
		TaskLimit            string
		Exclude              bool
		EnableMetadataExport bool
		OpenViewer           bool
	}
	questions := []*survey.Question{
		{
			Name: "TaskLimit",
			Prompt: &survey.Input{
				Message: "🚀 配图下载并发 (输入 'auto' 为智能默认，输入 1-50 为固定并发) [默认: auto]?",
				Default: func() string {
					if c.config.EnableDynamicTaskLimit {
						return "auto"
					}
					return strconv.Itoa(c.config.TaskLimit)
				}(),
			},
		},
		{
			Name: "Exclude",
			Prompt: &survey.Confirm{
				Message: "📦 开启增量备份 (已备份的动态会跳过，接着往更早的翻)?",
				Default: true,
			},
		},
		{
			Name: "EnableMetadataExport",
			Prompt: &survey.Confirm{
				Message: "📝 额外保存原始接口 JSON?",
				Default: c.config.EnableMetadataExport,
			},
		},
		{
			Name: "OpenViewer",
			Prompt: &survey.Confirm{
				Message: "🌐 完成后用浏览器打开查看页?",
				Default: true,
			},
		},
	}
	opts := survey.WithIcons(func(icons *survey.IconSet) {
		icons.Question.Text = "?"
		icons.Question.Format = "cyan"
	})
	if err := ask(questions, &answers, opts, survey.WithStdio(os.Stdin, os.Stdout, os.Stderr)); err != nil {
		return
	}

	if strings.ToLower(answers.TaskLimit) == "auto" || answers.TaskLimit == "" {
		c.config.EnableDynamicTaskLimit = true
		c.config.TaskLimit = 10
	} else {
		c.config.EnableDynamicTaskLimit = false
		c.config.TaskLimit, _ = strconv.Atoi(answers.TaskLimit)
		if c.config.TaskLimit < 1 {
			c.config.TaskLimit = 1
		}
		if c.config.TaskLimit > 50 {
			c.config.TaskLimit = 50
		}
	}
	c.config.EnableMetadataExport = answers.EnableMetadataExport
	_ = c.config.Save()

	taskLogger := c.createTaskLogger(targetUin)
	record := app.NewTaskRecord(app.TaskModeFeed, c.client.QQ, targetUin, []string{"全部动态"}, c.config, answers.Exclude)
	c.saveTaskRecord(record, nil, nil, app.TaskStatusPending)

	backup := app.NewFeedBackup(c.client, c.config, taskLogger)
	fmt.Println(color.HiBlackString("\n━━━━━━━━━━━━━━━━━━━━━━ 正在备份个人中心动态 ━━━━━━━━━━━━━━━━━━━━━━"))
	results, runErr := backup.Backup(ctx, targetUin, answers.Exclude, c.askFeedContinue)
	fmt.Println(color.HiBlackString("━━━━━━━━━━━━━━━━━━━━━━ 备份完成 ━━━━━━━━━━━━━━━━━━━━━━"))

	status := c.determineTaskStatus(ctx, results, runErr)
	c.saveTaskRecord(record, results, runErr, status)
	if runErr != nil {
		c.logger.Errorf("❌ 个人中心动态备份中断: %v", runErr)
	}
	c.renderTaskSummary("⭐ 个人中心动态备份报告 ⭐", targetUin, results, record, true)
	c.printFeedViewerHint(targetUin)
	if answers.OpenViewer && runErr == nil && ctx.Err() == nil {
		c.openLocalViewer(app.FeedIndexPath(targetUin))
	}
}

// askFeedContinue 一小段动态落盘后，问用户要不要继续往前翻。回车默认停在这里。
func (c *CLI) askFeedContinue(saved int, oldest string) app.FeedChoice {
	hint := fmt.Sprintf("目前一共 %d 条", saved)
	if oldest != "" {
		hint += "，最早到 " + oldest
	}
	c.logger.Info(hint)

	var selected string
	options := []string{
		"就到这里",
		fmt.Sprintf("再下载 %d 页", app.FeedDefaultPages()),
		"一直往前下载，直到没有更多",
	}
	err := askOne(&survey.Select{
		Message: "还要继续往前下载更早的动态吗?",
		Options: options,
		Default: options[0],
	}, &selected, survey.WithIcons(func(icons *survey.IconSet) {
		icons.Question.Text = "🌟"
		icons.SelectFocus.Text = "▶"
	}))
	if err != nil {
		return app.FeedChoiceStop
	}
	switch selected {
	case options[1]:
		return app.FeedChoiceMore
	case options[2]:
		return app.FeedChoiceAll
	default:
		return app.FeedChoiceStop
	}
}

// handleFeedRetry 只重试动态里还没成功的配图或视频。
func (c *CLI) handleFeedRetry(ctx context.Context, source *app.TaskRecord) {
	taskCfg := c.config.Clone()
	taskCfg.TaskLimit = source.Config.TaskLimit
	taskCfg.EnableDynamicTaskLimit = source.Config.EnableDynamicTaskLimit
	taskCfg.EnableMetadataExport = source.Config.EnableMetadataExport

	taskLogger := c.createTaskLogger(source.TargetUin)
	retryRecord := app.NewRetryTaskRecord(source, taskCfg)
	c.saveTaskRecord(retryRecord, nil, nil, app.TaskStatusPending)

	backup := app.NewFeedBackup(c.client, taskCfg, taskLogger)
	fmt.Println(color.HiBlackString("\n━━━━━━━━━━━━━━━━━━━━━━ 正在重试动态失败项 ━━━━━━━━━━━━━━━━━━━━━━"))
	results, runErr := backup.RetryFailed(ctx, source.TargetUin, source.OpenFailedItems)
	fmt.Println(color.HiBlackString("━━━━━━━━━━━━━━━━━━━━━━ 重试完成 ━━━━━━━━━━━━━━━━━━━━━━"))

	status := c.determineTaskStatus(ctx, results, runErr)
	c.saveTaskRecord(retryRecord, results, runErr, status)
	if runErr != nil {
		c.logger.Errorf("❌ 动态重试中断: %v", runErr)
	}
	remaining := []app.FailedItem(nil)
	if results != nil {
		remaining = results.FailedItems
	}
	if updateErr := app.ResolveOpenFailures(source.ID, remaining, retryRecord.ID); updateErr != nil {
		c.logger.Warnf("⚠️ 更新原任务失败项状态失败: %v", updateErr)
	}
	c.renderTaskSummary("⭐ 动态失败项重试报告 ⭐", source.TargetUin, results, retryRecord, true)
	c.printFeedViewerHint(source.TargetUin)

	open := true
	_ = askOne(&survey.Confirm{Message: "是否打开查看页确认结果?", Default: true}, &open)
	if open {
		c.openLocalViewer(app.FeedIndexPath(source.TargetUin))
	}
}

// handleViewFeed 打开已经生成的个人中心动态查看页，不必重新登录。
func (c *CLI) handleViewFeed() {
	viewers := app.ListFeedViewers()
	if len(viewers) == 0 {
		c.logger.Info("还没有个人中心动态备份。请先选择「备份个人中心动态」。")
		return
	}
	if len(viewers) == 1 {
		c.openLocalViewer(viewers[0].IndexHTML)
		return
	}
	options := make([]string, 0, len(viewers)+1)
	indexMap := make(map[string]string, len(viewers))
	for _, v := range viewers {
		name := v.Nickname
		if name == "" {
			name = "未命名"
		}
		label := fmt.Sprintf("%s (%s)  ·  %d 条", name, v.UIN, v.Total)
		if v.ExportedAt != "" {
			label += "  ·  " + v.ExportedAt
		}
		options = append(options, label)
		indexMap[label] = v.IndexHTML
	}
	options = append(options, "↩ 返回上一级")
	var selected string
	if err := askOne(&survey.Select{
		Message:  "请选择要打开的动态备份:",
		Options:  options,
		PageSize: 10,
	}, &selected, survey.WithIcons(func(icons *survey.IconSet) {
		icons.Question.Text = "🗂️"
		icons.SelectFocus.Text = "▶"
	})); err != nil || selected == "↩ 返回上一级" {
		return
	}
	c.openLocalViewer(indexMap[selected])
}

// printFeedViewerHint 在备份结束后打印目录和查看页路径。
func (c *CLI) printFeedViewerHint(targetUin string) {
	index := app.FeedIndexPath(targetUin)
	cyan := color.New(color.FgCyan).SprintFunc()
	c.logger.Infof("📂 备份目录: %s", cyan(app.FeedRoot(targetUin)))
	c.logger.Infof("🌐 查看页: %s", cyan(index))
	c.logger.Info("👉 可以直接双击 index.html 打开。正文和图片不用联网，空间表情小图需要联网。")
}
