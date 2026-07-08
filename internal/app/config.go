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

// Config defines the application configuration
type Config struct {
	TaskLimit            int  // 并发下载任务数限制
	EnableTimeline       bool // 是否按 年/月 整理时间轴
	EnableMetadataExport bool // 是否导出相册元数据 (JSON)
}

// NewDefaultConfig returns a default configuration
func NewDefaultConfig() *Config {
	return &Config{
		TaskLimit:            10,   // 默认并发数为 10
		EnableTimeline:       true, // 默认开启时间轴整理
		EnableMetadataExport: true, // 默认开启元数据导出
	}
}
