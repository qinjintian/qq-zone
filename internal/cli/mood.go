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
 * @FileName: mood.go
 * @Description: [说说备份与本地查看页的终端交互]
 */

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
	"github.com/qinjintian/qq-zone/internal/pkg/util"
)

func (c *CLI) handleMoodBackup(ctx context.Context, targetUin string) {
	c.logger.Infof("🚀 正在为 [%s] 配置说说备份...", color.YellowString(targetUin))

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
				Message: "📦 开启增量备份 (跳过已有说说和已下载的配图)?",
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
	if err := survey.Ask(questions, &answers, opts, survey.WithStdio(os.Stdin, os.Stdout, os.Stderr)); err != nil {
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
	record := app.NewTaskRecord(app.TaskModeShuoShuo, c.client.QQ, targetUin, []string{"说说"}, c.config, answers.Exclude)
	c.saveTaskRecord(record, nil, nil, app.TaskStatusPending)

	backup := app.NewMoodBackup(c.client, c.config, taskLogger)

	fmt.Println(color.HiBlackString("\n━━━━━━━━━━━━━━━━━━━━━━ 正在备份说说 ━━━━━━━━━━━━━━━━━━━━━━"))
	results, runErr := backup.Backup(ctx, targetUin, answers.Exclude)
	fmt.Println(color.HiBlackString("━━━━━━━━━━━━━━━━━━━━━━ 备份完成 ━━━━━━━━━━━━━━━━━━━━━━"))

	status := c.determineTaskStatus(ctx, results, runErr)
	c.saveTaskRecord(record, results, runErr, status)

	if runErr != nil {
		c.logger.Errorf("❌ 说说备份中断: %v", runErr)
	}

	c.renderTaskSummary("⭐ 说说备份报告 ⭐", targetUin, results, record, true)
	c.printMoodViewerHint(targetUin)

	if answers.OpenViewer && runErr == nil && ctx.Err() == nil {
		c.openMoodViewer(app.MoodIndexPath(targetUin))
	}
}

func (c *CLI) handleMoodRetry(ctx context.Context, source *app.TaskRecord) {
	taskCfg := c.config.Clone()
	taskCfg.TaskLimit = source.Config.TaskLimit
	taskCfg.EnableDynamicTaskLimit = source.Config.EnableDynamicTaskLimit
	taskCfg.EnableMetadataExport = source.Config.EnableMetadataExport

	taskLogger := c.createTaskLogger(source.TargetUin)
	retryRecord := app.NewRetryTaskRecord(source, taskCfg)
	c.saveTaskRecord(retryRecord, nil, nil, app.TaskStatusPending)

	backup := app.NewMoodBackup(c.client, taskCfg, taskLogger)

	fmt.Println(color.HiBlackString("\n━━━━━━━━━━━━━━━━━━━━━━ 正在重试说说失败项 ━━━━━━━━━━━━━━━━━━━━━━"))
	results, runErr := backup.RetryFailed(ctx, source.TargetUin, source.OpenFailedItems)
	fmt.Println(color.HiBlackString("━━━━━━━━━━━━━━━━━━━━━━ 重试完成 ━━━━━━━━━━━━━━━━━━━━━━"))

	status := c.determineTaskStatus(ctx, results, runErr)
	c.saveTaskRecord(retryRecord, results, runErr, status)
	if runErr != nil {
		c.logger.Errorf("❌ 说说重试中断: %v", runErr)
	}

	remaining := []app.FailedItem(nil)
	if results != nil {
		remaining = results.FailedItems
	}
	if updateErr := app.ResolveOpenFailures(source.ID, remaining, retryRecord.ID); updateErr != nil {
		c.logger.Warnf("⚠️ 更新原任务失败项状态失败: %v", updateErr)
	}

	c.renderTaskSummary("⭐ 说说失败项重试报告 ⭐", source.TargetUin, results, retryRecord, true)
	c.printMoodViewerHint(source.TargetUin)

	open := true
	_ = survey.AskOne(&survey.Confirm{
		Message: "是否打开查看页确认结果?",
		Default: true,
	}, &open)
	if open {
		c.openMoodViewer(app.MoodIndexPath(source.TargetUin))
	}
}

func (c *CLI) handleViewMood() {
	viewers := app.ListMoodViewers()
	if len(viewers) == 0 {
		c.logger.Info("还没有说说备份。请先选择「备份自己的说说」或「备份好友的说说」。")
		return
	}

	if len(viewers) == 1 {
		c.openMoodViewer(viewers[0].IndexHTML)
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
	if err := survey.AskOne(&survey.Select{
		Message:  "请选择要打开的说说备份:",
		Options:  options,
		PageSize: 10,
	}, &selected, survey.WithIcons(func(icons *survey.IconSet) {
		icons.Question.Text = "📖"
		icons.SelectFocus.Text = "▶"
	})); err != nil || selected == "↩ 返回上一级" {
		return
	}

	c.openMoodViewer(indexMap[selected])
}

func (c *CLI) printMoodViewerHint(targetUin string) {
	index := app.MoodIndexPath(targetUin)
	cyan := color.New(color.FgCyan).SprintFunc()
	c.logger.Infof("📂 备份目录: %s", cyan(app.MoodRoot(targetUin)))
	c.logger.Infof("🌐 查看页: %s", cyan(index))
	c.logger.Info("   可以直接双击 index.html 打开，不需要联网。")
}

func (c *CLI) openMoodViewer(indexPath string) {
	if !util.Exists(indexPath) {
		c.logger.Warnf("⚠️ 找不到查看页: %s", indexPath)
		return
	}
	if err := util.OpenInBrowser(indexPath); err != nil {
		c.logger.Warnf("⚠️ 自动打开浏览器失败，请手动双击: %s (%v)", indexPath, err)
		return
	}
	c.logger.Infof("✅ 已尝试打开查看页: %s", color.CyanString(indexPath))
}
