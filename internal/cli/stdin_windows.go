//go:build windows

/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-07
 * @FileName: stdin_windows.go
 */

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// drainStdin 清空控制台输入缓冲。备份时按的回车会积在这里，不丢掉就会被下一道菜单直接确认。
func drainStdin() {
	h := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return
	}
	_ = windows.FlushConsoleInputBuffer(h)
}
