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
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/fatih/color"
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
	logger  *zap.SugaredLogger
	logFact *logger.Factory
}

func NewCLI(httpClient *http.Client, logFact *logger.Factory, logger *zap.SugaredLogger) *CLI {
	return &CLI{
		http:    httpClient,
		logFact: logFact,
		logger:  logger,
	}
}

func (c *CLI) Start() {
	c.showBanner()
	c.Menu()
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

func (c *CLI) Menu() {
	for {
		var option string
		prompt := &survey.Select{
			Message: color.New(color.FgCyan, color.Bold).Sprint("请选择您要执行的操作:"),
			Options: []string{
				"🏠 下载自己的相册",
				"👥 下载好友的相册",
				"🔍 查看对我开放的好友",
				"⚙️ 开启/关闭调试模式",
				"🚪 退出程序",
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

		if strings.Contains(option, "退出程序") {
			color.Cyan("\n✅ 感谢使用，再见！👋")
			os.Exit(0)
		}

		if c.client == nil {
			c.logger.Info("正在准备登录，请扫描弹出的二维码...")
			client, err := qzone.NewClient(c.http, c.logFact)
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
			c.handleSpider(c.client.QQ)
		case strings.Contains(option, "下载好友的相册"):
			var targetUin string
			survey.AskOne(&survey.Input{
				Message: color.New(color.FgCyan).Sprint("请输入目标 QQ 号:"),
			}, &targetUin, survey.WithValidator(survey.Required))
			c.handleSpider(targetUin)
		case strings.Contains(option, "查看对我开放的好友"):
			c.handleAccessList()
		case strings.Contains(option, "开启/关闭调试模式"):
			c.handleDebugToggle()
		}
	}
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

func (c *CLI) handleSpider(targetUin string) {
	taskCountStr := "10"
	survey.AskOne(&survey.Input{
		Message: color.New(color.FgCyan).Sprint("🚀 并行下载任务数 (1-50):"),
		Default: "10",
	}, &taskCountStr)
	tasks, _ := strconv.Atoi(taskCountStr)

	exclude := true
	survey.AskOne(&survey.Confirm{
		Message: color.New(color.FgCyan).Sprint("📥 是否开启增量下载 (跳过已存在文件)?"),
		Default: true,
	}, &exclude)

	// 1. 获取相册列表
	c.logger.Infof("📡 正在获取 [%s] 的相册列表...", color.YellowString(targetUin))
	allAlbums, err := c.client.GetAlbumList(targetUin)
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
		label := fmt.Sprintf("%s (%d)", name, count)
		albumOptions = append(albumOptions, label)
		albumMap[label] = album
	}

	selectedLabels := []string{}
	promptSelect := &survey.MultiSelect{
		Message:  color.New(color.FgCyan).Sprint("📂 请选择要下载的相册 (空格选中，回车确认):"),
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
		// 如果选了全选，或者什么都没选（默认全下）
		finalAlbums = nil // nil 表示全部下载
		c.logger.Info("✅ 已选择下载全部相册")
	} else {
		for _, label := range selectedLabels {
			if album, ok := albumMap[label]; ok {
				finalAlbums = append(finalAlbums, album.Get("name").String())
			}
		}
		c.logger.Infof("✅ 已选择 %d 个相册进行下载", len(finalAlbums))
	}

	// 为当前下载任务创建独立的日志文件
	taskLogger, err := c.logFact.Create(targetUin)
	if err != nil {
		c.logger.Errorf("❌ 无法创建任务日志文件: %v", err)
		taskLogger = c.logger // 退回到默认日志
	}

	spider := app.NewSpider(c.client, tasks, finalAlbums, taskLogger)

	color.HiBlack("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	results, err := spider.Download(targetUin, exclude)
	if err != nil {
		c.logger.Errorf("❌ 下载过程中发生错误: %v", err)
		return
	}

	green := color.New(color.FgGreen).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	bold := color.New(color.Bold).SprintFunc()

	summary := fmt.Sprintf("\n%s\n", bold(cyan("✨ 下载任务执行完毕 ✨")))
	summary += fmt.Sprintf(" 📅 %-10s %s\n", "结束时间:", time.Now().Format("2006/01/02 15:04:05"))
	summary += fmt.Sprintf(" 👤 %-10s %s\n", "目标账号:", cyan(targetUin))
	summary += fmt.Sprintf(" 📊 %-10s 共 %s 个项目\n", "数据概览:", bold(results.Total))
	summary += fmt.Sprintf(" 📥 %-10s %s (图片: %d, 视频: %d)\n", "成功保存:", green(results.Success), results.ImageCount, results.VideoCount)
	summary += fmt.Sprintf(" 🆕 %-10s %s\n", "新增文件:", green(results.NewAdded))
	summary += fmt.Sprintf(" ⏩ %-10s %s\n", "跳过已存:", yellow(results.Skipped))
	summary += fmt.Sprintf(" ❌ %-10s %s\n", "失败数量:", red(results.Failed))
	summary += fmt.Sprintf("%s\n", bold(cyan("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")))

	c.logger.Info(summary)
}

func (c *CLI) handleAccessList() {
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

	friends, err := c.client.GetFriendList()
	if err != nil {
		c.logger.Errorf("❌ 获取好友列表失败: %v", err)
		return
	}

	headerColor := color.New(color.FgHiCyan, color.Bold).SprintFunc()
	fmt.Printf("\n%s\n", headerColor("📋 权限查询结果 (共 "+strconv.Itoa(len(friends))+" 位好友)"))
	fmt.Printf("%-15s | %-20s | %s\n", headerColor("QQ号"), headerColor("昵称"), headerColor("相册状态"))
	fmt.Println(color.HiBlackString(strings.Repeat("━", 70)))

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
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// 引入 500ms - 1500ms 的随机延迟，规避自动化检测
			time.Sleep(time.Duration(500+util.RandInt(0, 1000)) * time.Millisecond)

			uin := v.Get("uin").String()
			name := v.Get("name").String()

			albums, err := c.client.GetAlbumList(uin)
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

	for s := range statusChan {
		statusStr := s.info
		if strings.Contains(s.info, "🔓") {
			statusStr = color.GreenString(s.info)
		} else if strings.Contains(s.info, "🔒") {
			statusStr = color.RedString(s.info)
		} else {
			statusStr = color.YellowString(s.info)
		}
		fmt.Printf("%-15s | %-20s | %s\n", color.CyanString(s.uin), s.name, statusStr)
	}
	fmt.Println(color.HiBlackString(strings.Repeat("━", 70)))
}
