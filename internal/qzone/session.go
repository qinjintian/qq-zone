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

const (
	OldSessionPath = "storage/session.json"
	SessionsPath   = "storage/sessions.json"
)

type Session struct {
	QQ       string    `json:"qq"`
	Nickname string    `json:"nickname"`
	GTK      string    `json:"g_tk"`
	Cookie   string    `json:"cookie"`
	LastUsed time.Time `json:"last_used"`
}

// LoadSessions loads all sessions from local storage
func LoadSessions() (map[string]*Session, error) {
	sessions := make(map[string]*Session)

	// 1. 尝试迁移旧的单账号 session.json
	if _, err := os.Stat(OldSessionPath); err == nil {
		data, err := os.ReadFile(OldSessionPath)
		if err == nil {
			var s Session
			if err := json.Unmarshal(data, &s); err == nil {
				s.LastUsed = time.Now()
				sessions[s.QQ] = &s
				// 迁移后保存到新路径并尝试删除旧文件
				_ = saveSessionsToFile(sessions)
				_ = os.Remove(OldSessionPath)
			}
		}
	}

	// 2. 加载多账号 sessions.json
	data, err := os.ReadFile(SessionsPath)
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

// SaveSession saves or updates a session
func SaveSession(s *Session) error {
	sessions, _ := LoadSessions()
	if sessions == nil {
		sessions = make(map[string]*Session)
	}

	s.LastUsed = time.Now()
	sessions[s.QQ] = s

	return saveSessionsToFile(sessions)
}

// GetLastSession returns the most recently used session
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

// RemoveSession removes a specific account's session
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

// HasSession checks if any session exists
func HasSession() bool {
	sessions, _ := LoadSessions()
	return len(sessions) > 0
}

func saveSessionsToFile(sessions map[string]*Session) error {
	dir := filepath.Dir(SessionsPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		_ = os.MkdirAll(dir, os.ModePerm)
	}

	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(SessionsPath, data, 0644)
}
