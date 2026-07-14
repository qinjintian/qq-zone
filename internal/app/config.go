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
 * @FileName: config.go
 * @Description: [项目配置对象定义，支持通过 fx 注入到各个服务中]
 */

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config 定义了应用程序的全局配置结构
// 这些配置会被持久化到本地，以便下次启动时恢复用户的偏好设置
type Config struct {
	TaskLimit              int  `json:"task_limit"`                // 并发下载任务数限制
	EnableDynamicTaskLimit bool `json:"enable_dynamic_task_limit"` // 是否开启智能动态并发
	EnableTimeline         bool `json:"enable_timeline"`           // 是否按 年/月 整理时间轴
	EnableMetadataExport   bool `json:"enable_metadata_export"`    // 是否导出相册元数据 (JSON)
	EnableDebug            bool `json:"enable_debug"`              // 是否开启调试模式
}

const configPath = "storage/config.json"

// NewDefaultConfig 返回一套默认的应用程序配置
// 用于在初次运行或配置文件丢失时提供合理的初始化参数
func NewDefaultConfig() *Config {
	cfg := &Config{
		TaskLimit:              10,    // 默认并发数为 10
		EnableDynamicTaskLimit: true,  // 默认开启智能动态并发
		EnableTimeline:         true,  // 默认开启时间轴整理
		EnableMetadataExport:   true,  // 默认开启元数据导出
		EnableDebug:            false, // 默认关闭调试模式
	}

	// 尝试从本地加载配置
	if data, err := os.ReadFile(configPath); err == nil {
		_ = json.Unmarshal(data, cfg)
	}

	return cfg
}

// Save 将当前内存中的配置对象持久化写入到本地 JSON 文件中
// 以便实现跨运行周期的状态记忆（如调试模式开关、默认并发数等）
func (c *Config) Save() error {
	dir := filepath.Dir(configPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		_ = os.MkdirAll(dir, 0755)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

// Clone 返回当前配置对象的浅拷贝，便于在单次任务中安全覆写部分选项而不影响全局状态。
func (c *Config) Clone() *Config {
	if c == nil {
		return NewDefaultConfig()
	}

	cloned := *c
	return &cloned
}
