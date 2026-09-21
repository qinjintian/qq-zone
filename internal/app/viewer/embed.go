// 内嵌说说和留言板离线查看页的 HTML/CSS/JS 模板

package viewer

import "embed"

// Files 是离线查看页模板，备份结束时拷到说说或留言板目录的 assets/ 下。
//
//go:embed index.html assets/viewer.css assets/viewer.js
var Files embed.FS
