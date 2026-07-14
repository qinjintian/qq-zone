/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-07-02
 * @LastEditors: qinjintian<514092640@qq.com>
 * @LastEditTime: 2026-07-03 17:30:00
 * @FileName: menu.go
 * @Description: [交互式命令行界面实现，包含主菜单导航、相册多选及下载任务调度]
 */

package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/qinjintian/qq-zone/internal/app"
	"github.com/qinjintian/qq-zone/internal/net/http"
	"github.com/qinjintian/qq-zone/internal/pkg/logger"
	"github.com/qinjintian/qq-zone/internal/pkg/util"
	"github.com/qinjintian/qq-zone/internal/qzone"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const (
	QRCodeSavePath = "qrcode.png"
)

// CLI 定义了命令行交互客户端
// 封装了所有的业务模块组件及状态
type CLI struct {
	client  *qzone.Client
	http    *http.Client
	config  *app.Config
	logger  *zap.SugaredLogger
	logFact *logger.Factory
}

// NewCLI 实例化一个新的命令行交互客户端
func NewCLI(httpClient *http.Client, config *app.Config, logFact *logger.Factory, logger *zap.SugaredLogger) *CLI {
	return &CLI{
		http:    httpClient,
		config:  config,
		logFact: logFact,
		logger:  logger,
	}
}

// Start 启动 CLI 工具的核心生命周期
// 包含信号拦截(Graceful Shutdown)、欢迎横幅展示和进入主菜单循环
func (c *CLI) Start() {
	// 同步 Config 中的调试模式到 Factory
	c.logFact.SetDebug(c.config.EnableDebug)

	c.showBanner()

	// 创建一个可以响应中断信号的 Context
	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		count := 0
		for range sigCh {
			count++
			if count == 1 {
				color.Yellow("\n\n⚠️ 正在安全停止任务，等待当前正在下载的文件完成... (再次按 Ctrl+C 强制退出)\n")
				cancel()
			} else {
				color.Red("\n❌ 强制退出！\n")
				os.Exit(1)
			}
		}
	}()

	c.Menu(ctx)
}

// showBanner 打印启动时的 ASCII 艺术字横幅
func (c *CLI) showBanner() {
	cyan := color.New(color.FgCyan, color.Bold).SprintFunc()
	gray := color.New(color.FgWhite, color.Faint).SprintFunc()

	banner := `
   ____   ____                      ____                      
  / __ \ / __ \  ____  ____  ____  / / / ____  ____  ___ 
 / / / // / / / /_  / / __ \/ __ \/ / / / __ \/ __ \/ _ \
/ /_/ // /_/ / / /_/ /_/ / / / / / / / /_/ / / / /  __/
\___\_\\___\_\/___/\____/_/ /_/_/_/_/\____/_/ /_/\___/ 
`
	fmt.Print(cyan(banner))
	fmt.Printf("%s %s\n", cyan("    >> QQ 空间相册备份工具 <<"), gray("By qinjintian"))
	fmt.Println(gray("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))
}

// Menu 渲染主菜单并处理用户的选择分发
func (c *CLI) Menu(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			color.Cyan("\n✅ 已收到退出信号，再见！👋")
			return
		default:
		}

		// 1. 确保已登录 (启动时或切换账号后)
		// 移除自动强制登录逻辑，让用户先看到主菜单
		// 只有当用户选择需要登录的功能时，再触发登录校验

		// 2. 显示主菜单
		var option string
		menuMsg := "请选择您要执行的操作:"
		if c.client != nil {
			menuMsg = fmt.Sprintf("请选择操作 [%s (%s)]:", color.CyanString(c.client.Nickname), color.YellowString(c.client.QQ))
		}

		prompt := &survey.Select{
			Message: color.New(color.FgCyan, color.Bold).Sprint(menuMsg),
			Options: []string{
				"🏠 下载自己的相册",
				"👥 下载好友的相册",
				"🔁 重试上次失败项",
				"🔍 查看对我开放的好友",
				"⚙️ 开启/关闭调试模式",
				"🔄 切换账号/重新登录",
				"👋 退出程序",
			},
			Description: func(value string, index int) string {
				switch index {
				case 0:
					return "快速备份您当前登录账号下的所有照片和视频"
				case 1:
					return "输入好友 QQ 号，备份其公开或对您开放的相册内容"
				case 2:
					return "浏览历史失败任务列表，手动选择要重试的任务，仅重试尚未成功的文件"
				case 3:
					return "自动扫描并列出所有允许您访问空间的好友及其相册概况"
				case 4:
					status := "关闭"
					if c.logFact.IsDebug() {
						status = "开启"
					}
					return fmt.Sprintf("控制是否记录详细的 API 请求日志 (当前: %s)", status)
				case 5:
					return "注销当前登录状态，并准备扫码登录新账号"
				case 6:
					return "结束本次备份任务并安全退出"
				default:
					return ""
				}
			},
		}

		if err := survey.AskOne(prompt, &option, survey.WithIcons(func(icons *survey.IconSet) {
			icons.Question.Text = "❓"
			icons.SelectFocus.Text = "▶"
		})); err != nil {
			return
		}

		if strings.Contains(option, "👋 退出程序") {
			color.Cyan("\n✅ 感谢使用，再见！👋")
			os.Exit(0)
		}

		switch {
		case strings.Contains(option, "下载自己的相册"):
			if c.client == nil {
				if err := c.ensureLogin(ctx); err != nil {
					continue
				}
			}
			c.handleSpider(ctx, c.client.QQ)
		case strings.Contains(option, "下载好友的相册"):
			if c.client == nil {
				if err := c.ensureLogin(ctx); err != nil {
					continue
				}
			}
			var targetUin string
			survey.AskOne(&survey.Input{
				Message: color.New(color.FgCyan).Sprint("请输入目标 QQ 号:"),
			}, &targetUin, survey.WithValidator(survey.Required))
			c.handleSpider(ctx, targetUin)
		case strings.Contains(option, "重试上次失败项"):
			if c.client == nil {
				if err := c.ensureLogin(ctx); err != nil {
					continue
				}
			}
			c.handleRetryLastFailed(ctx)
		case strings.Contains(option, "查看对我开放的好友"):
			if c.client == nil {
				if err := c.ensureLogin(ctx); err != nil {
					continue
				}
			}
			c.handleAccessList(ctx)
		case strings.Contains(option, "开启/关闭调试模式"):
			c.handleDebugToggle()
		case strings.Contains(option, "切换账号/重新登录"):
			c.handleSwitchAccount()
			// 立即触发登录校验逻辑，这样就能显示账号管理列表了
			if err := c.ensureLogin(ctx); err != nil {
				continue
			}
		}
	}
}

// ensureLogin 验证当前客户端是否已登录，若未登录则触发账号管理/扫码流程
func (c *CLI) ensureLogin(ctx context.Context) error {
	sessions, _ := qzone.LoadSessions()

	// 如果没有任何历史账号，直接进入新扫码登录流程
	if len(sessions) == 0 {
		return c.loginNew(ctx)
	}

	// 准备历史账号选项
	var options []string
	qqMap := make(map[string]string)

	// 按最后使用时间排序
	var list []*qzone.Session
	for _, s := range sessions {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].LastUsed.After(list[j].LastUsed)
	})

	for _, s := range list {
		label := fmt.Sprintf("👤 %-15s (%s)", s.Nickname, s.QQ)
		options = append(options, label)
		qqMap[label] = s.QQ
	}
	options = append(options, "🆕 扫码登录新账号")
	options = append(options, "🗑️ 清空所有历史账号")

	var choice string
	prompt := &survey.Select{
		Message: "检测到历史登录记录，请选择账号:",
		Options: options,
	}

	if err := survey.AskOne(prompt, &choice, survey.WithIcons(func(icons *survey.IconSet) {
		icons.Question.Text = "🔑"
		icons.SelectFocus.Text = "▶"
	})); err != nil {
		return err
	}

	if choice == "🆕 扫码登录新账号" {
		return c.loginNew(ctx)
	}

	if choice == "🗑️ 清空所有历史账号" {
		for qq := range sessions {
			_ = qzone.RemoveSession(qq)
		}
		c.logger.Info("✅ 已清空所有历史账号")
		return c.loginNew(ctx)
	}

	// 尝试加载选中的账号
	targetQQ := qqMap[choice]
	sess := sessions[targetQQ]

	c.logger.Infof("📡 正在校验账号 [%s] 的登录状态...", sess.Nickname)
	client, err := qzone.NewClientWithSession(ctx, sess, c.http, c.logFact)
	if err != nil {
		c.logger.Warnf("⚠️  账号 [%s] 登录已失效，请重新扫码", sess.Nickname)
		return c.loginNew(ctx)
	}

	return c.setupClient(client)
}

func (c *CLI) loginNew(ctx context.Context) error {
	c.logger.Info("正在准备登录，请扫描弹出的二维码...")
	client, err := qzone.NewClientWithQR(ctx, c.http, c.logFact)
	if err != nil {
		c.logger.Errorf("❌ 登录失败: %v", err)
		return err
	}
	return c.setupClient(client)
}

func (c *CLI) setupClient(client *qzone.Client) error {
	c.client = client
	// 登录成功后，切换主日志到当前账号名下
	if userLogger, err := c.logFact.Create(client.QQ); err == nil {
		c.logger = userLogger
	}
	c.logger.Infof("✅ 登录成功: %s (%s)", color.CyanString(client.Nickname), color.YellowString(client.QQ))
	if util.Exists(QRCodeSavePath) {
		_ = os.Remove(QRCodeSavePath)
	}
	return nil
}

// handleSwitchAccount 处理多账号切换及管理逻辑
func (c *CLI) handleSwitchAccount() {
	c.client = nil
	c.logger.Info("🔄 已准备切换账号")
}

// handleDebugToggle 切换并持久化调试模式 (API 日志开关)
func (c *CLI) handleDebugToggle() {
	current := c.logFact.IsDebug()
	newStatus := !current
	c.logFact.SetDebug(newStatus)

	// 同步并保存配置
	c.config.EnableDebug = newStatus
	_ = c.config.Save()

	statusStr := color.RedString("已关闭")
	if newStatus {
		statusStr = color.GreenString("已开启")
	}
	c.logger.Infof("⚙️  调试模式 (API 日志) %s", statusStr)

	// 如果已经登录，需要重新创建 client 的 APILogger
	if c.client != nil {
		if apiLogger, err := c.logFact.CreateAPILogger(c.client.QQ); err == nil {
			c.client.APILogger = apiLogger
		}
	}
}

// handleSpider 配置并执行指定账号的相册批量备份任务
func (c *CLI) handleSpider(ctx context.Context, targetUin string) {
	c.logger.Infof("🚀 正在为 [%s] 配置备份任务...", color.YellowString(targetUin))

	// 定义配置表单
	var answers struct {
		TaskLimit            string
		Exclude              bool
		EnableTimeline       bool
		EnableMetadataExport bool
	}

	questions := []*survey.Question{
		{
			Name: "TaskLimit",
			Prompt: &survey.Input{
				Message: "🚀 并发下载策略 (输入 'auto' 为智能动态并发，输入 1-50 为固定并发) [默认: auto]?",
				Default: func() string {
					if c.config.EnableDynamicTaskLimit {
						return "auto"
					}
					return strconv.Itoa(c.config.TaskLimit)
				}(),
			},
			Validate: func(val interface{}) error {
				str, ok := val.(string)
				if !ok {
					return fmt.Errorf("无效的输入")
				}
				if str == "" || strings.ToLower(str) == "auto" {
					return nil
				}
				i, err := strconv.Atoi(str)
				if err != nil {
					return fmt.Errorf("请输入 'auto' 或 1-50 之间的数字")
				}
				if i < 1 || i > 50 {
					return fmt.Errorf("范围必须在 1-50 之间")
				}
				return nil
			},
		},
		{
			Name: "Exclude",
			Prompt: &survey.Confirm{
				Message: "📦 开启增量下载 (跳过已存在)?",
				Default: true,
			},
		},
		{
			Name: "EnableTimeline",
			Prompt: &survey.Confirm{
				Message: "📁 按拍摄时间整理 (年/月)?",
				Default: c.config.EnableTimeline,
			},
		},
		{
			Name: "EnableMetadataExport",
			Prompt: &survey.Confirm{
				Message: "📝 导出相册元数据 (metadata.json)?",
				Default: c.config.EnableMetadataExport,
			},
		},
	}

	// 统一配置图标样式
	opts := survey.WithIcons(func(icons *survey.IconSet) {
		icons.Question.Text = "?"
		icons.Question.Format = "cyan"
	})

	// 使用批量提问模式，这是解决 Windows 终端重复输出和空行问题的最稳健方案
	if err := survey.Ask(questions, &answers, opts, survey.WithStdio(os.Stdin, os.Stdout, os.Stderr)); err != nil {
		return
	}

	// 更新配置
	if strings.ToLower(answers.TaskLimit) == "auto" || answers.TaskLimit == "" {
		c.config.EnableDynamicTaskLimit = true
		c.config.TaskLimit = 10 // 设置一个基础的底线并发
	} else {
		c.config.EnableDynamicTaskLimit = false
		c.config.TaskLimit, _ = strconv.Atoi(answers.TaskLimit)
	}

	c.config.EnableTimeline = answers.EnableTimeline
	c.config.EnableMetadataExport = answers.EnableMetadataExport
	_ = c.config.Save() // 持久化备份任务配置
	exclude := answers.Exclude

	// 2. 获取相册列表
	c.logger.Infof("📡 正在从腾讯服务器拉取相册列表...")
	allAlbums, err := c.client.GetAlbumList(ctx, targetUin)
	if err != nil {
		c.logger.Errorf("❌ 获取相册列表失败: %v", err)
		return
	}

	if len(allAlbums) == 0 {
		c.logger.Warnf("⚠️  该账号 [%s] 没有任何公开相册或登录已失效", targetUin)
		return
	}

	// 2. 准备多选菜单
	albumOptions := []string{"[全选/全不选]"}
	albumMap := make(map[string]gjson.Result)
	for _, album := range allAlbums {
		name := album.Get("name").String()
		count := album.Get("total").Int()
		// 格式化选项显示：相册名 (数量)
		label := fmt.Sprintf("%-30s (%d)", name, count)
		albumOptions = append(albumOptions, label)
		albumMap[label] = album
	}

	selectedLabels := []string{}
	promptSelect := &survey.MultiSelect{
		Message:  color.New(color.FgCyan).Sprint("📂 请勾选要备份的相册 (空格选中):"),
		Options:  albumOptions,
		PageSize: 15,
	}

	// 使用自定义图标美化勾选框
	iconOpt := survey.WithIcons(func(icons *survey.IconSet) {
		icons.Question.Text = "❓"
		icons.SelectFocus.Text = "▶"
		icons.MarkedOption.Text = "✅"
		icons.UnmarkedOption.Text = "⬜"
	})

	if err := survey.AskOne(promptSelect, &selectedLabels, iconOpt); err != nil {
		return
	}

	// 3. 处理选择逻辑
	var finalAlbums []string
	isSelectAll := false
	for _, label := range selectedLabels {
		if label == "[全选/全不选]" {
			isSelectAll = true
			break
		}
	}

	if isSelectAll || len(selectedLabels) == 0 {
		finalAlbums = nil // nil 表示全部下载
		c.logger.Info("✅ 已确认: 备份全部相册")
	} else {
		for _, label := range selectedLabels {
			if album, ok := albumMap[label]; ok {
				finalAlbums = append(finalAlbums, album.Get("name").String())
			}
		}
		c.logger.Infof("✅ 已确认: 备份 %d 个指定相册", len(finalAlbums))
	}

	taskLogger := c.createTaskLogger(targetUin)
	record := app.NewTaskRecord(app.TaskModeBackup, c.client.QQ, targetUin, finalAlbums, c.config, exclude)
	c.saveTaskRecord(record, nil, nil, app.TaskStatusPending)

	spider := app.NewSpider(c.client, c.config, finalAlbums, taskLogger)

	fmt.Println(color.HiBlackString("\n━━━━━━━━━━━━━━━━━━━━━━ 正在下载 ━━━━━━━━━━━━━━━━━━━━━━"))
	results, runErr := spider.Download(ctx, targetUin, exclude)
	fmt.Println(color.HiBlackString("━━━━━━━━━━━━━━━━━━━━━━ 下载完成 ━━━━━━━━━━━━━━━━━━━━━━"))

	status := c.determineTaskStatus(ctx, results, runErr)
	c.saveTaskRecord(record, results, runErr, status)

	if runErr != nil {
		c.logger.Errorf("❌ 备份过程中发生异常中断: %v", runErr)
	}

	c.renderTaskSummary("⭐ 备份任务报告 ⭐", targetUin, results, record, true)
}

// handleRetryLastFailed 展示当前账号下全部可重试的历史任务，并允许用户手动选择一个任务重试。
func (c *CLI) handleRetryLastFailed(ctx context.Context) {
	records, err := app.ListRetryableTasks(c.client.QQ)
	if err != nil {
		c.logger.Errorf("❌ 读取历史任务记录失败: %v", err)
		return
	}
	if len(records) == 0 {
		c.logger.Info("✅ 当前没有可重试的失败任务")
		return
	}

	options := make([]string, 0, len(records)+1)
	recordMap := make(map[string]*app.TaskRecord, len(records))
	for _, record := range records {
		modeText := "备份"
		if record.Mode == app.TaskModeRetryFailed {
			modeText = "失败重试"
		}

		label := fmt.Sprintf(
			"[%s] %s | 目标:%s | 待重试:%d | 成功:%d/%d | %s",
			record.ID,
			record.CreatedAt.Format("2006-01-02 15:04:05"),
			record.TargetUin,
			len(record.OpenFailedItems),
			record.Summary.Success,
			record.Summary.Total,
			modeText,
		)
		options = append(options, label)
		recordMap[label] = record
	}
	options = append(options, "↩ 返回上一级")

	var selected string
	if err := survey.AskOne(&survey.Select{
		Message:  "请选择要重试的历史任务:",
		Options:  options,
		PageSize: 10,
	}, &selected, survey.WithIcons(func(icons *survey.IconSet) {
		icons.Question.Text = "🧾"
		icons.SelectFocus.Text = "▶"
	})); err != nil || selected == "↩ 返回上一级" {
		return
	}

	record := recordMap[selected]
	if record == nil {
		c.logger.Warn("⚠️ 未找到所选任务，请重试")
		return
	}

	c.logger.Infof("📦 已选择任务 [%s]，目标账号 [%s]，待重试 %d 个文件", record.ID, color.YellowString(record.TargetUin), len(record.OpenFailedItems))

	confirm := false
	if err := survey.AskOne(&survey.Confirm{
		Message: fmt.Sprintf("是否立即重试任务 [%s] 的 %d 个失败文件？", record.ID, len(record.OpenFailedItems)),
		Default: true,
	}, &confirm); err != nil || !confirm {
		return
	}

	taskCfg := c.config.Clone()
	taskCfg.TaskLimit = record.Config.TaskLimit
	taskCfg.EnableDynamicTaskLimit = record.Config.EnableDynamicTaskLimit
	taskCfg.EnableTimeline = record.Config.EnableTimeline
	taskCfg.EnableMetadataExport = record.Config.EnableMetadataExport

	taskLogger := c.createTaskLogger(record.TargetUin)
	retryRecord := app.NewRetryTaskRecord(record, taskCfg)
	c.saveTaskRecord(retryRecord, nil, nil, app.TaskStatusPending)

	spider := app.NewSpider(c.client, taskCfg, record.Albums, taskLogger)

	fmt.Println(color.HiBlackString("\n━━━━━━━━━━━━━━━━━━━━━━ 正在重试失败项 ━━━━━━━━━━━━━━━━━━━━━━"))
	results, runErr := spider.RetryFailed(ctx, record.TargetUin, record.OpenFailedItems)
	fmt.Println(color.HiBlackString("━━━━━━━━━━━━━━━━━━━━━━ 重试完成 ━━━━━━━━━━━━━━━━━━━━━━"))

	status := c.determineTaskStatus(ctx, results, runErr)
	c.saveTaskRecord(retryRecord, results, runErr, status)
	if runErr != nil {
		c.logger.Errorf("❌ 重试过程中发生异常中断: %v", runErr)
	}

	remainingFailures := []app.FailedItem(nil)
	if results != nil {
		remainingFailures = results.FailedItems
	}
	if updateErr := app.ResolveOpenFailures(record.ID, remainingFailures, retryRecord.ID); updateErr != nil {
		c.logger.Warnf("⚠️ 更新原任务失败项状态失败: %v", updateErr)
	}

	c.renderTaskSummary("⭐ 失败项重试报告 ⭐", record.TargetUin, results, retryRecord, true)
}

func (c *CLI) createTaskLogger(targetUin string) *zap.SugaredLogger {
	taskLogger, err := c.logFact.Create(targetUin)
	if err != nil {
		c.logger.Errorf("❌ 无法创建任务日志文件: %v", err)
		return c.logger
	}
	return taskLogger
}

func (c *CLI) determineTaskStatus(ctx context.Context, results *app.DownloadResult, runErr error) app.TaskStatus {
	if ctx.Err() != nil {
		return app.TaskStatusCancelled
	}
	if runErr != nil && (results == nil || results.Success == 0) {
		return app.TaskStatusFailed
	}
	if results != nil && results.Failed > 0 {
		return app.TaskStatusPartial
	}
	return app.TaskStatusSuccess
}

func (c *CLI) saveTaskRecord(record *app.TaskRecord, results *app.DownloadResult, runErr error, status app.TaskStatus) {
	if record == nil {
		return
	}
	record.Finalize(results, runErr, status)
	if err := record.Save(); err != nil {
		c.logger.Warnf("⚠️ 写入任务记录失败: %v", err)
	}
}

func (c *CLI) renderTaskSummary(title string, targetUin string, results *app.DownloadResult, record *app.TaskRecord, showFailureTable bool) {
	if results == nil {
		return
	}

	green := color.New(color.FgGreen).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	summary := fmt.Sprintf("\n%s\n", bold(cyan(title)))
	summary += fmt.Sprintf(" 🕒 %-10s %s\n", "结束时间:", time.Now().Format("2006/01/02 15:04:05"))
	summary += fmt.Sprintf(" 👤 %-10s %s\n", "目标账号:", cyan(targetUin))
	if record != nil {
		summary += fmt.Sprintf(" 🧾 %-10s %s\n", "任务编号:", yellow(record.ID))
		summary += fmt.Sprintf(" 📄 %-10s %s\n", "任务记录:", cyan(record.Path))
	}
	summary += fmt.Sprintf(" 📊 %-10s 共 %s 个项目\n", "数据概览:", bold(results.Total))
	summary += fmt.Sprintf(" ✅ %-10s %s (图片: %d, 视频: %d)\n", "成功保存:", green(results.Success), results.ImageCount, results.VideoCount)
	summary += fmt.Sprintf(" ➕ %-10s %s\n", "新增文件:", green(results.NewAdded))
	summary += fmt.Sprintf(" ⏭  %-10s %s\n", "跳过已存:", yellow(results.Skipped))
	summary += fmt.Sprintf(" ❌ %-10s %s\n", "失败数量:", red(results.Failed))
	if record != nil && len(record.OpenFailedItems) > 0 {
		summary += fmt.Sprintf(" 🔁 %-10s %s\n", "待重试项:", red(len(record.OpenFailedItems)))
	}
	summary += fmt.Sprintf("%s\n", bold(cyan("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")))

	c.logger.Info(summary)

	if !showFailureTable || len(results.FailedItems) == 0 {
		return
	}

	color.Red("\n⚠️  失败详情清单:")
	fTable := tablewriter.NewWriter(os.Stdout)
	fTable.SetHeader([]string{"相册", "文件名", "错误原因"})
	fTable.SetAutoWrapText(true)
	fTable.SetColWidth(50)
	fTable.SetBorder(false)
	fTable.SetCenterSeparator("")
	fTable.SetColumnSeparator("")
	fTable.SetRowSeparator("")
	fTable.SetHeaderLine(false)
	fTable.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	fTable.SetAlignment(tablewriter.ALIGN_LEFT)

	for _, item := range results.FailedItems {
		fTable.Append([]string{item.Album, item.Name, item.Error})
	}
	fTable.Render()
	fmt.Println()
}

// handleAccessList 查询并渲染允许当前用户访问的好友列表
// 使用 tablewriter 实现流式表格渲染
func (c *CLI) handleAccessList(ctx context.Context) {
	yellow := color.New(color.FgYellow).SprintFunc()
	c.logger.Warnf("%s 正在获取好友列表并查询访问权限...", yellow("⚠️  警告：由于好友数量较多，高频查询可能导致腾讯临时封禁您的 IP 或账号（表现为相册列表变为空），请谨慎操作。"))

	// 增加一个确认环节
	confirm := false
	survey.AskOne(&survey.Confirm{
		Message: "确定要开始扫描吗？(建议不要频繁执行)",
		Default: true,
	}, &confirm)

	if !confirm {
		return
	}

	friends, err := c.client.GetFriendList(ctx)
	if err != nil {
		c.logger.Errorf("❌ 获取好友列表失败: %v", err)
		return
	}

	headerColor := color.New(color.FgHiCyan, color.Bold).SprintFunc()
	fmt.Printf("\n%s\n", headerColor("📋 权限查询结果 (共 "+strconv.Itoa(len(friends))+" 位好友)"))

	// 打印表头，由于我们要流式输出，不再使用 tablewriter 缓冲
	gray := color.New(color.FgWhite, color.Faint).SprintFunc()
	fmt.Printf(" %-12s %-20s %s\n", gray("QQ号"), gray("昵称"), gray("相册状态"))
	fmt.Println(gray(" ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"))

	type friendStatus struct {
		uin  string
		name string
		info string
	}
	statusChan := make(chan friendStatus, len(friends))

	var wg sync.WaitGroup
	// 降低并发数到 3，模拟更像人类的行为
	semaphore := make(chan struct{}, 3)

	for _, f := range friends {
		wg.Add(1)
		go func(v gjson.Result) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case semaphore <- struct{}{}:
			}
			defer func() { <-semaphore }()

			// 引入 500ms - 1500ms 的随机延迟，规避自动化检测
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(500+util.RandInt(0, 1000)) * time.Millisecond):
			}

			uin := v.Get("uin").String()
			name := v.Get("name").String()

			albums, err := c.client.GetAlbumList(ctx, uin)
			if err != nil {
				statusChan <- friendStatus{uin, name, "🔒 无法访问 (设置了权限)"}
				return
			}

			var accessible []string
			for _, a := range albums {
				if a.Get("allowAccess").Int() != 0 {
					accessible = append(accessible, a.Get("name").String())
				}
			}

			if len(accessible) > 0 {
				info := fmt.Sprintf("🔓 公开相册: %d 个 (%s)", len(accessible), strings.Join(accessible, ", "))
				if len(info) > 40 {
					info = info[:37] + "..."
				}
				statusChan <- friendStatus{uin, name, info}
			} else {
				statusChan <- friendStatus{uin, name, "📁 无公开相册"}
			}
		}(f)
	}

	go func() {
		wg.Wait()
		close(statusChan)
	}()

	// 实时渲染结果，不再等待全部完成
	count := 0
	for s := range statusChan {
		count++
		statusStr := s.info
		if strings.Contains(s.info, "🔓") {
			statusStr = color.GreenString(s.info)
		} else if strings.Contains(s.info, "🔒") {
			statusStr = color.RedString(s.info)
		} else {
			statusStr = color.YellowString(s.info)
		}

		// 格式化输出一行结果，使用 PadRight 保证列对齐
		uinStr := color.CyanString(util.PadRight(s.uin, 12))
		nameStr := util.PadRight(s.name, 20)
		progressStr := gray(fmt.Sprintf("[%d/%d]", count, len(friends)))

		fmt.Printf(" %s %s %s %s\n", uinStr, nameStr, statusStr, progressStr)
	}
	fmt.Printf("\n%s\n", color.GreenString("✅ 扫描完成！"))
}
