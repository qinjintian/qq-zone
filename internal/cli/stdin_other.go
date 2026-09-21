//go:build !windows

// 非 Windows 下非阻塞丢掉 stdin 里积着的按键

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
