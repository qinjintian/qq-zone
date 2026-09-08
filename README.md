<div align="center">

# QQ 空间备份（相册原图 / 原视频 / 说说时间线）

**扫码登录，一键备份到本地。** 相册下原图和原视频；说说连同评论、点赞、浏览数一起保存，并生成可双击打开的网页。

[![Release](https://img.shields.io/github/v/release/qinjintian/qq-zone?color=blue&include_prereleases&style=flat-square)](https://github.com/qinjintian/qq-zone/releases)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat-square&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat-square)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey?style=flat-square)](https://github.com/qinjintian/qq-zone/releases/latest)

</div>

<p align="center">
  <img src="docs/images/demo.png" alt="终端扫码登录后备份相册或说说" width="920">
</p>

> **💡 不会写代码也能用**
>
> **不要点右上角绿色 `Code` 按钮下载源码。** 请到 Release 下载已经打包好的程序，双击即可运行：
>
> 👉 [**下载最新版**](https://github.com/qinjintian/qq-zone/releases/latest)
>
> | 系统 | 下载文件 |
> | --- | --- |
> | Windows | `qq-zone-win.exe` |
> | macOS（Apple 芯片） | `qq-zone-macos-m-series` |
> | macOS（Intel） | `qq-zone-macos-intel` |
> | Linux | `qq-zone-linux` |
>
> 不确定是哪款 Mac：左上角苹果菜单 →「关于本机」，芯片写着 Apple 就选 M 系列。Windows 若提示「已保护你的电脑」，点「更多信息」→「仍要运行」即可。

---

## 🌟 简介

QQ 空间里堆着很多人的照片、视频和说说。手动保存又慢，相册还经常变成压缩图，说说更是只能在网页里翻。本工具用 **Go** 重写：相册支持 **原图 / 原视频 / 实况图** 并 **保留拍摄信息（EXIF）**；说说会连同配图、视频、评论、点赞和浏览数一起拉下来，并生成一份 **可双击打开的本地时间线网页**。自己的空间和好友对你开放的空间都可以备份，手机 QQ **扫码登录** 即可。

## ✨ 核心特性

- 📸 **原图 + EXIF，不是压缩预览**：按相册原始分辨率下载，拍摄信息跟着走，备份下来才是自己的底片。
- 💬 **说说备份成本地网页**：正文、配图、视频、语音、转发、评论、点赞人和浏览次数一并保存；生成 `index.html`，双击就能按时间线翻看，不用对着 JSON 发愁。
- 🎬 **能播就能下，地址失效自动换源**：相册视频、实况图一并收下；点下载失败时会改走播放地址再试，尽量把文件救回来。
- 📱 **扫码即用**：终端直接出二维码，手机 QQ 一扫就登录；账号会记住，换号也不用重新折腾。
- 👥 **自己的、好友的，勾了就下**：已有文件自动跳过，隔几个月再跑一遍只补新的。
- 🔁 **失败能重试，中断能续上**：半截文件接着下，下坏了重来；历史任务里还能把失败项单独再跑一次。
- ⏱️ **导入手机后，时间线还是当年**：相册文件时间写成拍摄时间；也可按年/月归档，并导出相册元数据。
- 🔍 **先看清谁对我开放**：列出能访问的好友空间和公开相册，再决定备份谁。
- 🛡️ **限流会等着，Ctrl+C 停得住**：被减速时自动等待，进度条看得到速度；中途退出尽量不把文件写坏。

### 说说查看页还能做什么

备份完成后生成 `index.html`，**双击用浏览器打开即可，不必再开本程序**。

- 按年份跳转，搜索正文 / 评论 / 地点，筛选有图或有视频
- 点开配图，播放视频和语音，展开全部评论
- 评论者和点赞人优先显示你通讯录里的好友备注
- 可切换深色模式；空间表情小图需要联网，照片和正文不用

---

## 🚀 快速开始

### 方式一：直接运行（推荐）

下载文件见上方表格，或打开 [Releases](https://github.com/qinjintian/qq-zone/releases/latest) 选对应系统。到这里就可以运行了，下面是给要自己编译的人看的。

### 方式二：源码编译

1. **克隆仓库**
   ```bash
   git clone https://github.com/qinjintian/qq-zone.git
   cd qq-zone
   ```
2. **编译项目**
   - **全平台交叉编译（推荐）**：
     ```powershell
     ./scripts/build.ps1    # Windows
     ```
     ```bash
     chmod +x scripts/build.sh   # 首次需要，赋予脚本可执行权限
     ./scripts/build.sh          # Linux / macOS
     ```
     编译产物会生成到本地 `bin/` 目录。
   - **仅编译当前平台**：
     ```bash
     go build -o bin/qq-zone ./cmd/qq-zone
     ```
3. **启动程序**
   ```bash
   ./bin/qq-zone-win.exe      # Windows (amd64)
   ./bin/qq-zone-linux        # Linux (amd64)
   ./bin/qq-zone-macos-intel  # macOS (Intel)
   ./bin/qq-zone-macos-m-series # macOS (Apple Silicon)
   ```

## 📖 使用指南

1. **启动程序**：双击 Release 里的可执行文件，或运行自己编译出的程序。先进入主菜单，选到需要登录的功能时再提示你登录。
2. **登录**：第一次会在终端画出二维码（同时生成 `qrcode.png`），用手机 QQ 扫一下即可。以前登录过会先列出历史账号，选一个就能继续，不用反复扫码。
3. **选功能**：方向键 `↑` `↓` 移动，`Enter` 确认。
   - **下载自己的相册**：备份当前账号的照片和视频。
   - **备份自己的说说**：下载说说、配图、视频和评论，并生成本地时间线网页。
   - **下载好友的相册**：输入好友 QQ，备份其公开或对你开放的相册。
   - **备份好友的说说**：备份好友空间里对你可见的说说。
   - **查看说说备份**：用浏览器打开已经生成的 `index.html`，不必重新登录。
   - **重试上次失败项**：从历史任务里挑一次记录，只补还没成功的文件。
   - **查看对我开放的好友**：看哪些好友空间你能进、有哪些公开相册。
   - **切换账号 / 重新登录**：换一个已保存的号，或再扫一次码。
4. **配一下任务**：并发填 `auto` 即可；建议打开增量下载（跳过已有文件）。相册还可按年/月整理、导出元数据。说说可以选是否额外保存原始接口 JSON，以及结束后是否打开查看页。
5. **勾选相册**：空格勾选，也可点「全选」；直接回车则备份全部。`Enter` 开始下载。说说没有相册勾选，确认选项后即开始拉取。
6. **看结果**：下完会出任务报告。
   - 照片和视频在 `storage/qzone/<QQ号>/album/`
   - 说说查看页在 `storage/qzone/<QQ号>/shuoshuo/index.html`，可直接双击打开
   - 任务记录在 `storage/tasks/`
   中途想停按一次 `Ctrl+C`，等当前文件收尾即可。

文件都写在程序所在目录的 `storage/` 下：

```text
storage/
├── config.json                  # 并发、增量等偏好
├── tasks/                       # 任务记录，失败重试会用到
└── qzone/<QQ号>/
    ├── album/<相册名>/          # 照片、视频；可选年/月子目录
    └── shuoshuo/
        ├── index.html           # 双击打开查看页
        ├── assets/              # 查看页样式和脚本
        ├── avatars/             # 头像
        ├── media/               # 说说配图、视频、语音
        ├── data/                # backup.json 和按年拆开的页面数据
        └── raw/                 # 可选：接口原始 JSON
```

## ❓ 常见问题

**下下来的是原图，还是空间里看到的压缩图？**  
按相册原始分辨率下载，并尽量保留拍摄信息（EXIF）。不是页面上的预览图。

**一定要黄钻才能下原图吗？**  
以 QQ 空间实际开放的权限为准。空间对你可见的原图/视频，工具就会去下；没有权限的相册会被跳过。

**视频或实况图失败怎么办？**  
下载地址失效时会自动换一条再试。仍失败的文件会记进任务记录，可在菜单里选「重试上次失败项」。

**好友相册或说说为什么是空的？**  
对方没对你开放，或内容是私密的。可先用「查看对我开放的好友」确认哪些空间能进。访客看不见的说说会被跳过。

**中途按 Ctrl+C 会把文件下坏吗？**  
按一次后会等当前文件收尾再退出，尽量不留下半截文件。未完成的可以下次增量续传或失败重试。

**说说备份下来怎么看？**  
备份完成后会生成 `storage/qzone/<QQ号>/shuoshuo/index.html`。直接双击用浏览器打开即可，支持按年份跳转、搜索正文和评论、点开配图。不需要再开本程序，也不需要联网（空间表情小图除外）。菜单里也可以选「查看说说备份」。

**浏览数、点赞怎么和空间网页对上？**  
说说列表本身不含赞和浏览，工具会另外去拉，再写进查看页。旧备份若缺这两项，再跑一次**增量备份**即可补上（已下载的配图仍会跳过）。点赞人特别多时，名单只保留比较前面的一批（大约 60 人），总人数仍显示「共 N 人觉得很赞」。

**更早的说说为什么没有？**  
空间有时会把很久以前的说说封存，或对访客不可见。工具会把已经能拉到的部分存下来，并在查看页顶部提示。开通相关权限后可再跑一次增量备份。

**登录会不会把账号密码交给别人？**  
不会。在你自己电脑上扫码，和网页版 QQ 空间同一套登录。登录信息和备份都只写在本机 `storage/` 里，不会上传到别处。

**这是腾讯官方工具吗？**  
不是。这是个人开源项目，未与腾讯关联。请只备份你有权访问的内容。遇到问题欢迎到 Issues 反馈。

**文件保存在哪？**  
相册：`storage/qzone/<QQ号>/album/`。说说：`storage/qzone/<QQ号>/shuoshuo/`（其中 `index.html` 是查看页）。任务记录在 `storage/tasks/`。

---

## 🛠️ 技术栈

日常使用可以跳过。想改代码时：登录和空间协议在 `internal/qzone`，相册/说说备份在 `internal/app`，下载在 `internal/net`，终端菜单在 `internal/cli`。

| 类别 | 选用方案 |
| :--- | :--- |
| **核心语言** | Go 1.25+（以仓库 `go.mod` 为准） |
| **依赖注入** | [uber-go/fx](https://github.com/uber-go/fx) |
| **进度渲染** | [vbauerster/mpb/v8](https://github.com/vbauerster/mpb) |
| **HTTP 客户端** | [go-resty/resty/v2](https://github.com/go-resty/resty) |
| **交互式菜单** | [survey/v2](https://github.com/AlecAivazis/survey) |
| **频率限制** | `golang.org/x/time/rate` |
| **表格渲染** | [olekukonko/tablewriter](https://github.com/olekukonko/tablewriter) |
| **日志系统** | [uber-go/zap](https://github.com/uber-go/zap) |
| **并发控制** | `golang.org/x/sync/errgroup` |
| **JSON 解析** | [gjson](https://github.com/tidwall/gjson) |

---

## 📁 项目结构

```text
.
├── .github/            # GitHub Actions 自动化构建与发布流水线
├── bin/                # 编译产物（不入库，可执行文件请前往 Releases 下载）
├── cmd/                # 程序入口
│   └── qq-zone/        # 主程序入口
├── docs/               # 项目文档与演示资料
├── internal/           # 私有业务逻辑
│   ├── app/            # 核心引擎（相册下载 + 说说备份）
│   │   └── viewer/     # 说说离线时间线页面（HTML/CSS/JS）
│   ├── cli/            # 终端交互界面
│   ├── net/            # 网络请求封装
│   ├── pkg/            # 公共工具包 (Logger, Util)
│   └── qzone/          # QQ 空间协议实现
├── scripts/            # 跨平台编译脚本（支持 Windows 下构建全平台二进制程序）
├── storage/            # 数据存储（日志、相册、说说、任务记录、配置）
└── go.mod              # 依赖管理
```

发版时推送 `v*` 标签会走 GitHub Actions，编译 Windows / Linux / macOS 四个文件并挂到 Release。

---

## 🤝 贡献与反馈

如果这个项目对你有帮助，欢迎点一个 **⭐ Star**。

- **作者**: qinjintian
- **邮箱**: [514092640@qq.com](mailto:514092640@qq.com)
- **问题反馈**: 请提交 [Issues](https://github.com/qinjintian/qq-zone/issues)

---

<p align="center">
  <b>Made with ❤️ by qinjintian</b>
</p>
