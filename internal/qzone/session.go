/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-07-07
 * @FileName: session.go
 * @Description: [登录会话持久化管理，支持多账号 Session 的保存、加载与切换]
 */

package qzone

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

var (
	// SessionPath 定义了多账号会话信息的持久化文件路径
	SessionPath = "storage/sessions.json"
)

// Session 记录了单个账号登录状态的核心凭证与信息
type Session struct {
	QQ       string    `json:"qq"`        // 账号的唯一标识
	Nickname string    `json:"nickname"`  // 账号的展示昵称
	GTK      string    `json:"g_tk"`      // 根据 p_skey 算出的防跨站 CSRF 凭证
	Cookie   string    `json:"cookie"`    // 请求 API 必须携带的持久化 Cookie 串
	LastUsed time.Time `json:"last_used"` // 最后一次使用的时间
}

// LoadSessions 从本地 session.json，返回当前存储的所有历史账号信息
func LoadSessions() (map[string]*Session, error) {
	sessions := make(map[string]*Session)

	// 尝试加载新的多账号 sessions.json
	data, err := os.ReadFile(SessionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return sessions, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, err
	}

	return sessions, nil
}

// SaveSession 将当前最新提取到的登录凭证安全地写入到本地存储文件中
// 支持自动合并已有会话，并刷新 LastUsed 时间戳
func SaveSession(s *Session) error {
	sessions, _ := LoadSessions()
	if sessions == nil {
		sessions = make(map[string]*Session)
	}

	s.LastUsed = time.Now()
	sessions[s.QQ] = s

	return saveSessionsToFile(sessions)
}

// GetLastSession 从本地存储中提取出最新使用（或最后更新）的活跃会话
func GetLastSession() (*Session, error) {
	sessions, err := LoadSessions()
	if err != nil || len(sessions) == 0 {
		return nil, err
	}

	var list []*Session
	for _, s := range sessions {
		list = append(list, s)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].LastUsed.After(list[j].LastUsed)
	})

	return list[0], nil
}

// RemoveSession 根据提供的 QQ 号，从本地持久化存储中移除指定的登录状态
func RemoveSession(qq string) error {
	sessions, err := LoadSessions()
	if err != nil {
		return err
	}

	if _, ok := sessions[qq]; ok {
		delete(sessions, qq)
		return saveSessionsToFile(sessions)
	}

	return nil
}

// ClearSession 清理当前所有会话 (保留方法名以兼容旧逻辑，实际逻辑为清理最近一次)
func ClearSession() error {
	s, err := GetLastSession()
	if err != nil || s == nil {
		return nil
	}
	return RemoveSession(s.QQ)
}

// SetActiveSession 标记指定的 QQ 号为当前激活状态(最新使用)
func SetActiveSession(qq string) error {
	sessions, err := LoadSessions()
	if err != nil {
		return err
	}

	if s, ok := sessions[qq]; ok {
		s.LastUsed = time.Now()
		return saveSessionsToFile(sessions)
	}

	return nil
}

// HasSession 检查本地是否存在任何已保存的历史登录凭证
func HasSession() bool {
	sessions, _ := LoadSessions()
	return len(sessions) > 0
}

// saveSessionsToFile 将多账号状态全量写入本地文件
func saveSessionsToFile(sessions map[string]*Session) error {
	// 确保目录存在
	dir := filepath.Dir(SessionPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		_ = os.MkdirAll(dir, os.ModePerm)
	}

	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(SessionPath, data, 0644)
}
