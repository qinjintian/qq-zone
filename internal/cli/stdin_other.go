//go:build !windows

/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-07
 * @FileName: stdin_other.go
 */

package cli

import (
	"os"
	"syscall"

	"golang.org/x/term"
)

// drainStdin 非阻塞读掉已经到达的按键；不是终端（管道）时不动 stdin。
func drainStdin() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		return
	}
	defer func() { _ = syscall.SetNonblock(fd, false) }()

	buf := make([]byte, 4096)
	for {
		n, err := syscall.Read(fd, buf)
		if n <= 0 || err != nil {
			return
		}
	}
}
