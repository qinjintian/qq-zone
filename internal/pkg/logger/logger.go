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

// ansiRegex is used to strip ANSI color codes from strings
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// plainFileWriter wraps an io.Writer and strips ANSI escape codes
type plainFileWriter struct {
	w io.Writer
}

func (p *plainFileWriter) Write(b []byte) (int, error) {
	plain := ansiRegex.ReplaceAll(b, []byte(""))
	n, err := p.w.Write(plain)
	if err != nil {
		return n, err
	}
	return len(b), nil
}

type Factory struct {
	basePath string
	buffer   *bytes.Buffer
	mu       sync.Mutex
	debug    bool
}

func NewFactory(basePath string) *Factory {
	return &Factory{
		basePath: basePath,
		buffer:   new(bytes.Buffer),
		debug:    false, // 默认关闭调试模式
	}
}

func (f *Factory) SetDebug(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.debug = enabled
}

func (f *Factory) IsDebug() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.debug
}

func (f *Factory) Create(uin string) (*zap.SugaredLogger, error) {
	return f.createLogger(uin, false)
}

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
