/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-07
 * @FileName: open.go
 * @Description: [用系统默认浏览器打开本地文件，供说说查看页双击之外的菜单入口使用]
 */

package util

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

// OpenInBrowser 用系统默认方式打开本地文件（通常是 HTML）。
func OpenInBrowser(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", abs).Start()
	case "darwin":
		return exec.Command("open", abs).Start()
	default:
		return exec.Command("xdg-open", abs).Start()
	}
}
