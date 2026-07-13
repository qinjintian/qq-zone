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
 * @FileName: logger.go
 * @Description: [结构化日志系统工厂实现，支持按账号分流、API 报文审计及 ANSI 码过滤]
 */

package logger

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ansiRegex 预编译了用于匹配终端 ANSI 颜色转义字符的正则表达式
// 用于在写入日志文件前清洗掉控制台的颜色代码，保证纯文本日志的整洁
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// plainFileWriter 是一个自定义的 io.Writer 包装器
// 它的核心职责是拦截即将写入文件的日志流，并通过 ansiRegex 剔除掉所有 ANSI 颜色字符
type plainFileWriter struct {
	w io.Writer
}

// Write 实现了 io.Writer 接口，在此过程中利用正则去除了文本中的 ANSI 颜色代码
func (p *plainFileWriter) Write(b []byte) (int, error) {
	plain := ansiRegex.ReplaceAll(b, []byte(""))
	n, err := p.w.Write(plain)
	if err != nil {
		return n, err
	}
	return len(b), nil
}

// Factory 负责统筹和创建所有的日志记录器实例
// 支持内存缓冲与并发安全控制
type Factory struct {
	basePath string
	buffer   *bytes.Buffer
	mu       sync.Mutex
	debug    bool
}

// NewFactory 实例化一个日志工厂，指定日志落盘的基础目录
func NewFactory(basePath string) *Factory {
	return &Factory{
		basePath: basePath,
		buffer:   new(bytes.Buffer),
		debug:    false, // 默认关闭调试模式
	}
}

// SetDebug 动态开启或关闭 API 级别的调试模式
func (f *Factory) SetDebug(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.debug = enabled
}

// IsDebug 返回当前系统是否处于调试模式
func (f *Factory) IsDebug() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.debug
}

// Create 为指定的 QQ 号生成一个常规业务日志记录器
func (f *Factory) Create(uin string) (*zap.SugaredLogger, error) {
	return f.createLogger(uin, false)
}

// CreateAPILogger 为指定的 QQ 号生成专用的 API 调试日志记录器
// 如果全局调试模式未开启，它将返回一个极低开销的 Nop (无操作) 记录器
func (f *Factory) CreateAPILogger(uin string) (*zap.SugaredLogger, error) {
	f.mu.Lock()
	isDebug := f.debug
	f.mu.Unlock()

	if !isDebug {
		// 如果未开启调试模式，返回一个不执行任何操作的 Nop Logger
		return zap.NewNop().Sugar(), nil
	}
	return f.createLogger(uin, true)
}

// createLogger 封装了底层 zap Logger 的核心构建逻辑
// 支持区分常规日志和 API 日志，同时配置了控制台输出和基于 plainFileWriter 的纯文本文件落盘
func (f *Factory) createLogger(uin string, isAPI bool) (*zap.SugaredLogger, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	config := zap.NewProductionEncoderConfig()
	config.EncodeTime = zapcore.TimeEncoderOfLayout("2006/01/02 15:04:05")
	config.CallerKey = ""
	config.LevelKey = "L"
	config.TimeKey = "T"
	config.ConsoleSeparator = " "

	var logWriter zapcore.WriteSyncer

	if uin == "" {
		// 登录前，仅输出到控制台并存入内存缓冲
		logWriter = zapcore.NewMultiWriteSyncer(
			zapcore.AddSync(os.Stdout),
			zapcore.AddSync(&plainFileWriter{w: f.buffer}),
		)
	} else {
		// 登录后，创建账号专属日志文件
		dateStr := time.Now().Format("2006-01-02")
		prefix := ""
		if isAPI {
			prefix = "api_"
		}
		fileName := fmt.Sprintf("%s%s_%s.log", prefix, uin, dateStr)
		logPath := filepath.Join(f.basePath, fileName)
		_ = os.MkdirAll(f.basePath, os.ModePerm)

		file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}

		// 只有业务日志才合并系统缓冲日志，API 日志保持纯净
		if !isAPI && f.buffer.Len() > 0 {
			_, _ = file.Write(f.buffer.Bytes())
			f.buffer.Reset()
		}

		if isAPI {
			// API 日志默认不输出到控制台，防止刷屏，只存文件
			logWriter = zapcore.AddSync(&plainFileWriter{w: file})
		} else {
			logWriter = zapcore.NewMultiWriteSyncer(
				zapcore.AddSync(os.Stdout),
				zapcore.AddSync(&plainFileWriter{w: file}),
			)
		}
	}

	level := zap.InfoLevel
	if isAPI {
		level = zap.DebugLevel
	}

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(config),
		logWriter,
		level,
	)

	return zap.New(core).Sugar(), nil
}
