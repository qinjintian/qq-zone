//go:build windows

// Windows 下丢掉控制台里积着的按键

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
