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
 * @FileName: main.go
 * @Description: [QQ 空间备份工具启动入口，基于 Uber-fx 实现依赖注入与生命周期管理]
 */

package main

import (
	"context"
	"path/filepath"

	"github.com/qinjintian/qq-zone/internal/app"
	"github.com/qinjintian/qq-zone/internal/cli"
	"github.com/qinjintian/qq-zone/internal/net/http"
	"github.com/qinjintian/qq-zone/internal/pkg/logger"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

func main() {
	app := fx.New(
		// 注入所有核心服务依赖
		fx.Provide(
			app.NewDefaultConfig,
			http.NewClient,
			func() *logger.Factory {
				return logger.NewFactory(filepath.Join("storage", "logs"))
			},
			func(f *logger.Factory) (*zap.SugaredLogger, error) {
				return f.Create("")
			},
			cli.NewCLI,
		),
		// 启动 CLI 交互界面
		fx.Invoke(func(lifecycle fx.Lifecycle, c *cli.CLI, l *zap.SugaredLogger) {
			lifecycle.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					// 因为 CLI 的交互会阻塞主线程，所以我们将其放入独立的 Goroutine 中运行
					// 这样配合 fx 生命周期管理，可以实现平滑的启动和优雅停机
					go c.Start()
					return nil
				},
				OnStop: func(ctx context.Context) error {
					l.Info("正在停止程序...")
					return nil
				},
			})
		}),
		// 禁用 fx 框架自带的启动日志，以保持 CLI 终端界面的极简和清爽
		fx.NopLogger,
	)

	app.Run()
}
