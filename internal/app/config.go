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

// Config defines the application configuration
type Config struct {
	TaskLimit            int  `json:"task_limit"`             // 并发下载任务数限制
	EnableTimeline       bool `json:"enable_timeline"`        // 是否按 年/月 整理时间轴
	EnableMetadataExport bool `json:"enable_metadata_export"` // 是否导出相册元数据 (JSON)
	EnableDebug          bool `json:"enable_debug"`           // 是否开启调试模式
}

const configPath = "storage/config.json"

// NewDefaultConfig returns a default configuration
func NewDefaultConfig() *Config {
	cfg := &Config{
		TaskLimit:            10,   // 默认并发数为 10
		EnableTimeline:       true, // 默认开启时间轴整理
		EnableMetadataExport: true, // 默认开启元数据导出
		EnableDebug:          false, // 默认关闭调试模式
	}

	// 尝试从本地加载配置
	if data, err := os.ReadFile(configPath); err == nil {
		_ = json.Unmarshal(data, cfg)
	}

	return cfg
}

// Save persists the configuration to local storage
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
