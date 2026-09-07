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
 * @FileName: embed.go
 * @Description: [内嵌说说离线查看页的 HTML/CSS/JS 模板]
 */

package viewer

import "embed"

// Files 是离线查看页模板，备份结束时拷到 storage/qzone/<QQ>/shuoshuo/。
//
//go:embed index.html assets/viewer.css assets/viewer.js
var Files embed.FS
