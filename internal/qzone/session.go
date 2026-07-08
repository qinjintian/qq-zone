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
 * @Description: [登录会话持久化管理，支持 Session 的保存、加载与有效性校验]
 */

package qzone

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	SessionPath = "storage/session.json"
)

type Session struct {
	QQ       string `json:"qq"`
	Nickname string `json:"nickname"`
	GTK      string `json:"g_tk"`
	Cookie   string `json:"cookie"`
}

// SaveSession saves the session to local storage
func SaveSession(s *Session) error {
	dir := filepath.Dir(SessionPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		_ = os.MkdirAll(dir, os.ModePerm)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(SessionPath, data, 0644)
}

// LoadSession loads the session from local storage
func LoadSession() (*Session, error) {
	data, err := os.ReadFile(SessionPath)
	if err != nil {
		return nil, err
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}

	return &s, nil
}

// ClearSession removes the session file
func ClearSession() error {
	return os.Remove(SessionPath)
}

// HasSession checks if the session file exists
func HasSession() bool {
	_, err := os.Stat(SessionPath)
	return err == nil
}
