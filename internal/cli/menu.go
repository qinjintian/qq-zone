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

type CLI struct {
	client  *qzone.Client
	http    *http.Client
	config  *app.Config
	logger  *zap.SugaredLogger
	logFact *logger.Factory
}

func NewCLI(httpClient *http.Client, config *app.Config, logFact *logger.Factory, logger *zap.SugaredLogger) *CLI {
	return &CLI{
		http:    httpClient,
		config:  config,
		logFact: logFact,
		logger:  logger,
	}
}

func (c *CLI) Start() {
	c.showBanner()

	// 创建一个可以响应中断信号的 Context
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c.Menu(ctx)
}

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

func (c *CLI) Menu(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			color.Cyan("\n✅ 已收到退出信号，再见！👋")
			return
		default:
		}

		var option string
		prompt := &survey.Select{
			Message: color.New(color.FgCyan, color.Bold).Sprint("请选择您要执行的操作:"),
			Options: []string{
				"🏠 下载自己的相册",
				"👥 下载好友的相册",
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
					return "自动扫描并列出所有允许您访问空间的好友及其相册概况"
				case 3:
					status := "关闭"
					if c.logFact.IsDebug() {
						status = "开启"
					}
					return fmt.Sprintf("控制是否记录详细的 API 请求日志 (当前: %s)", status)
				case 4:
					return "注销当前登录状态，并准备扫码登录新账号"
				case 5:
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

		if c.client == nil {
			if !qzone.HasSession() {
				c.logger.Info("正在准备登录，请扫描弹出的二维码...")
			}
			client, err := qzone.NewClient(ctx, c.http, c.logFact)
			if err != nil {
				c.logger.Errorf("❌ 登录失败: %v", err)
				continue
			}
			c.client = client
			// 登录成功后，切换主日志到当前账号名下
			if userLogger, err := c.logFact.Create(client.QQ); err == nil {
				c.logger = userLogger
			}
			c.logger.Infof("✅ 登录成功: %s (%s)", color.CyanString(client.Nickname), color.YellowString(client.QQ))
			if util.Exists(QRCodeSavePath) {
				_ = os.Remove(QRCodeSavePath)
			}
		}

		switch {
		case strings.Contains(option, "下载自己的相册"):
			c.handleSpider(ctx, c.client.QQ)
		case strings.Contains(option, "下载好友的相册"):
			var targetUin string
			survey.AskOne(&survey.Input{
				Message: color.New(color.FgCyan).Sprint("请输入目标 QQ 号:"),
			}, &targetUin, survey.WithValidator(survey.Required))
			c.handleSpider(ctx, targetUin)
		case strings.Contains(option, "查看对我开放的好友"):
			c.handleAccessList(ctx)
		case strings.Contains(option, "开启/关闭调试模式"):
			c.handleDebugToggle()
		case strings.Contains(option, "切换账号 / 重新登录"):
			c.handleSwitchAccount()
		}
	}
}

func (c *CLI) handleSwitchAccount() {
	if err := qzone.ClearSession(); err != nil {
		c.logger.Errorf("❌ 注销失败: %v", err)
		return
	}
	c.client = nil
	c.logger.Info("✅ 当前账号已注销，请选择操作以重新扫码登录")
}

func (c *CLI) handleDebugToggle() {
	current := c.logFact.IsDebug()
	newStatus := !current
	c.logFact.SetDebug(newStatus)

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
				Message: "🚀 并发下载数 (1-50) [默认:10]?",
				Default: strconv.Itoa(c.config.TaskLimit),
			},
			Validate: func(val interface{}) error {
				str, ok := val.(string)
				if !ok {
					return fmt.Errorf("无效的输入")
				}
				// 如果用户输入了内容（不为空），则进行数字范围校验
				if str != "" {
					i, err := strconv.Atoi(str)
					if err != nil {
						return fmt.Errorf("请输入数字")
					}
					if i < 1 || i > 50 {
						return fmt.Errorf("范围必须在 1-50 之间")
					}
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
	c.config.TaskLimit, _ = strconv.Atoi(answers.TaskLimit)
	c.config.EnableTimeline = answers.EnableTimeline
	c.config.EnableMetadataExport = answers.EnableMetadataExport
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

	// 为当前下载任务创建独立的日志文件
	taskLogger, err := c.logFact.Create(targetUin)
	if err != nil {
		c.logger.Errorf("❌ 无法创建任务日志文件: %v", err)
		taskLogger = c.logger
	}

	spider := app.NewSpider(c.client, c.config, finalAlbums, taskLogger)

	fmt.Println(color.HiBlackString("\n━━━━━━━━━━━━━━━━━━━━━━ 正在下载 ━━━━━━━━━━━━━━━━━━━━━━"))
	results, err := spider.Download(ctx, targetUin, exclude)
	if err != nil {
		c.logger.Errorf("❌ 备份过程中发生异常中断: %v", err)
		return
	}
	fmt.Println(color.HiBlackString("━━━━━━━━━━━━━━━━━━━━━━ 下载完成 ━━━━━━━━━━━━━━━━━━━━━━"))

	green := color.New(color.FgGreen).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	summary := fmt.Sprintf("\n%s\n", bold(cyan("⭐ 备份任务报告 ⭐")))
	summary += fmt.Sprintf(" 🕒 %-10s %s\n", "结束时间:", time.Now().Format("2006/01/02 15:04:05"))
	summary += fmt.Sprintf(" 👤 %-10s %s\n", "目标账号:", cyan(targetUin))
	summary += fmt.Sprintf(" 📊 %-10s 共 %s 个项目\n", "数据概览:", bold(results.Total))
	summary += fmt.Sprintf(" ✅ %-10s %s (图片: %d, 视频: %d)\n", "成功保存:", green(results.Success), results.ImageCount, results.VideoCount)
	summary += fmt.Sprintf(" ➕ %-10s %s\n", "新增文件:", green(results.NewAdded))
	summary += fmt.Sprintf(" ⏭  %-10s %s\n", "跳过已存:", yellow(results.Skipped))
	summary += fmt.Sprintf(" ❌ %-10s %s\n", "失败数量:", red(results.Failed))
	summary += fmt.Sprintf("%s\n", bold(cyan("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")))

	c.logger.Info(summary)

	if len(results.FailedItems) > 0 {
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
}

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
