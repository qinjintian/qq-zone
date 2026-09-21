// 群相册备份的终端交互：先选群，再选相册

package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/fatih/color"
	"github.com/qinjintian/qq-zone/internal/app"
	"github.com/qinjintian/qq-zone/internal/qzone"
	"github.com/tidwall/gjson"
)

const (
	groupListAll     = "📱 查看我加入的全部群"
	groupManualInput = "⌨️ 手动输入群号"
	groupGoBack      = "↩ 返回"
)

const (
	groupActBack = iota
	groupActAuth
	groupActStay
	groupActGroup
)

const (
	groupFlowQuit = iota
	groupFlowPicker
	groupFlowDone
)

// handleGroupAlbum 先进群名单，再下相册。授权是名单上的一个动作，不拦在门口提问。
func (c *CLI) handleGroupAlbum(ctx context.Context) {
	remote, listed := c.loadRemoteGroups(ctx)
	if ctx.Err() != nil {
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}

		groups := qzone.VisibleGroups(remote, c.localKnownGroups(), listed)
		group, act := c.pickGroup(groups, listed)
		switch act {
		case groupActBack:
			return
		case groupActStay:
			continue
		case groupActAuth:
			got, ok := c.authorizeGroupList(ctx)
			if !ok {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			remote, listed = got, true
			continue
		}

		switch c.backupOneGroup(ctx, group) {
		case groupFlowQuit:
			return
		case groupFlowPicker:
			continue
		}

		var again bool
		if err := askOne(&survey.Confirm{
			Message: color.New(color.FgCyan).Sprint("继续备份其他群?"),
			Default: false,
		}, &again); err != nil || !again {
			return
		}
	}
}

func (c *CLI) loadRemoteGroups(ctx context.Context) ([]qzone.Group, bool) {
	if !c.client.HasQunAuth() {
		return nil, false
	}

	c.logger.Info("📡 正在拉取你加入的群…")
	groups, err := c.fetchGroupList(ctx)
	if err == nil {
		return groups, true
	}
	if ctx.Err() != nil {
		return nil, false
	}
	if qzone.IsQunAuthError(err) {
		c.logger.Info("完整群列表的授权过期了，空间登录还在。下面先列出本机用过的群。")
		c.client.ClearQunAuth()
		return nil, false
	}
	c.logger.Warnf("⚠️  完整群列表暂时拉不下来: %v", err)
	c.logger.Info("可以先选手头用过的群，或手输群号。")
	return nil, false
}

func (c *CLI) fetchGroupList(ctx context.Context) ([]qzone.Group, error) {
	var (
		groups  []qzone.Group
		listErr error
	)
	err := app.WithWaitSpinner(ctx, "正在拉取群列表", func() error {
		groups, listErr = c.client.GetGroupList(ctx)
		return listErr
	})
	if err != nil {
		return groups, err
	}
	return groups, nil
}

func (c *CLI) authorizeGroupList(ctx context.Context) ([]qzone.Group, bool) {
	fmt.Println()
	c.logger.Infof("请用当前账号 %s 扫码，列出你加入的群。这不是重新登录空间，扫过会记住。", color.YellowString(c.client.QQ))
	if err := c.client.AuthorizeQun(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, false
		}
		c.logger.Warnf("⚠️  没有完成授权: %v", err)
		c.logger.Info("可以继续选手头用过的群，或手输群号。")
		return nil, false
	}
	c.logger.Info("✅ 已授权查看加入的群，下次不用再扫。")
	groups, err := c.fetchGroupList(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false
		}
		c.logger.Warnf("⚠️  授权成功但列表暂时拉不下来: %v", err)
		return nil, false
	}
	return groups, true
}

func (c *CLI) backupOneGroup(ctx context.Context, group qzone.Group) int {
	if group.DisplayName() == group.ID {
		c.logger.Infof("✅ 已选择群 %s", color.YellowString(group.ID))
	} else {
		c.logger.Infof("✅ 已选择群: %s (%s)", color.CyanString(group.DisplayName()), color.YellowString(group.ID))
	}

	c.logger.Infof("📡 正在拉取群 [%s] 的相册列表…", color.YellowString(group.DisplayName()))
	var allAlbums []gjson.Result
	err := app.WithWaitSpinner(ctx, "正在拉取群相册列表", func() error {
		var fetchErr error
		allAlbums, fetchErr = c.client.GetGroupAlbumList(ctx, group.ID)
		return fetchErr
	})
	if err != nil {
		c.logger.Errorf("❌ 获取群相册失败: %v", err)
		c.logger.Info("请确认你仍在该群内，并且群相册对你可见。")
		return c.askStayInGroupPicker("换一个群试试?")
	}
	if len(allAlbums) == 0 {
		c.logger.Warnf("⚠️  群 [%s] 没有相册，或当前账号无权查看", group.DisplayName())
		return c.askStayInGroupPicker("换一个群试试?")
	}
	_ = qzone.RememberGroup(c.client.QQ, group)

	exclude, ok := c.askAlbumTaskOptions()
	if !ok {
		return groupFlowPicker
	}

	finalAlbums, ok := c.pickAlbums(allAlbums)
	if !ok {
		return groupFlowPicker
	}

	taskLogger := c.createTaskLogger(c.client.QQ)
	record := app.NewTaskRecord(app.TaskModeGroupAlbum, c.client.QQ, c.client.QQ, finalAlbums, c.config, exclude)
	record.GroupID = group.ID
	record.GroupName = group.DisplayName()
	c.saveTaskRecord(record, nil, nil, app.TaskStatusPending)

	spider := app.NewGroupSpider(c.client, c.config, finalAlbums, group.ID, group.DisplayName(), taskLogger)

	fmt.Println(color.HiBlackString("\n━━━━━━━━━━━━━━━━━━━━━━ 正在下载群相册 ━━━━━━━━━━━━━━━━━━━━━━"))
	results, runErr := spider.Download(ctx, c.client.QQ, exclude)
	fmt.Println(color.HiBlackString("━━━━━━━━━━━━━━━━━━━━━━ 下载完成 ━━━━━━━━━━━━━━━━━━━━━━"))

	status := c.determineTaskStatus(ctx, results, runErr)
	c.saveTaskRecord(record, results, runErr, status)

	if runErr != nil {
		c.logger.Errorf("❌ 群相册备份过程中发生异常中断: %v", runErr)
	}

	display := fmt.Sprintf("%s (%s)", group.DisplayName(), group.ID)
	if group.DisplayName() == group.ID {
		display = group.ID
	}
	c.renderTaskSummary("⭐ 群相册备份报告 ⭐", display, results, record, true)
	c.logger.Infof("📂 文件保存在 storage/qzone/%s/qun/%s/", c.client.QQ, group.ID)
	return groupFlowDone
}

func (c *CLI) askStayInGroupPicker(message string) int {
	var stay bool
	if err := askOne(&survey.Confirm{
		Message: color.New(color.FgCyan).Sprint(message),
		Default: true,
	}, &stay); err != nil || !stay {
		return groupFlowQuit
	}
	return groupFlowPicker
}

// pickGroup 先给群名单，操作项放在后面。未授权时「查看全部群」是动作，不是进门考题。
func (c *CLI) pickGroup(groups []qzone.Group, listedAll bool) (qzone.Group, int) {
	options := make([]string, 0, len(groups)+3)
	groupMap := make(map[string]qzone.Group, len(groups))
	for _, g := range groups {
		label := groupChoiceLabel(g)
		options = append(options, label)
		groupMap[label] = g
	}
	if !listedAll {
		options = append(options, groupListAll)
	}
	options = append(options, groupManualInput, groupGoBack)

	pageSize := 14
	if len(options) < pageSize {
		pageSize = len(options)
	}

	var selected string
	prompt := &survey.Select{
		Message:  color.New(color.FgCyan).Sprint(groupPickerMessage(len(groups), listedAll)),
		Options:  options,
		PageSize: pageSize,
		Description: func(value string, index int) string {
			if g, ok := groupMap[value]; ok {
				if role := g.RoleLabel(); role != "" {
					return role
				}
				return "已加入"
			}
			switch value {
			case groupListAll:
				return "再扫一次即可，不是重新登录空间；扫过会记住"
			case groupManualInput:
				return "列表没有时，直接输入群号"
			default:
				return ""
			}
		},
	}

	if err := askOne(prompt, &selected, survey.WithIcons(func(icons *survey.IconSet) {
		icons.Question.Text = "❓"
		icons.SelectFocus.Text = "▶"
	})); err != nil || selected == groupGoBack {
		return qzone.Group{}, groupActBack
	}

	if selected == groupListAll {
		return qzone.Group{}, groupActAuth
	}
	if selected == groupManualInput {
		g, ok := c.inputGroupID()
		if !ok {
			return qzone.Group{}, groupActStay
		}
		return g, groupActGroup
	}

	g, ok := groupMap[selected]
	if !ok {
		return qzone.Group{}, groupActBack
	}
	return g, groupActGroup
}

func groupPickerMessage(n int, listedAll bool) string {
	switch {
	case listedAll && n > 0:
		return fmt.Sprintf("🏫 请选择要备份相册的群（%d 个，可输入群名或群号过滤）:", n)
	case listedAll:
		return "🏫 没有拉到加入的群，可以手输群号:"
	case n > 0:
		return "🏫 请选择要备份相册的群（可授权查看全部加入的群）:"
	default:
		return "🏫 还没有群名单，可以授权查看全部，或手输群号:"
	}
}

func groupChoiceLabel(g qzone.Group) string {
	name := strings.TrimSpace(g.Name)
	if name == "" || name == g.ID || name == "群"+g.ID {
		return g.ID
	}
	return fmt.Sprintf("%s  (%s)", name, g.ID)
}

func (c *CLI) inputGroupID() (qzone.Group, bool) {
	var raw string
	if err := askOne(&survey.Input{
		Message: color.New(color.FgCyan).Sprint("请输入群号:"),
		Help:    "只填数字，例如 123456789",
	}, &raw, survey.WithValidator(func(ans interface{}) error {
		s, _ := ans.(string)
		if !qzone.IsValidGroupID(s) {
			return fmt.Errorf("请输入 4-15 位数字群号")
		}
		return nil
	})); err != nil {
		return qzone.Group{}, false
	}
	id := strings.TrimSpace(raw)
	return qzone.Group{ID: id, Role: "recent"}, true
}

func (c *CLI) localKnownGroups() []qzone.Group {
	if c == nil || c.client == nil {
		return nil
	}
	extra := qzone.LoadKnownGroups(c.client.QQ)
	records, err := app.LoadTaskRecords()
	if err != nil {
		return extra
	}
	for _, rec := range records {
		if rec == nil || rec.OperatorUin != c.client.QQ {
			continue
		}
		id := strings.TrimSpace(rec.GroupID)
		if !qzone.IsValidGroupID(id) {
			continue
		}
		extra = append(extra, qzone.Group{ID: id, Name: rec.GroupName, Role: "recent"})
	}
	return qzone.MergeGroups(nil, extra)
}
