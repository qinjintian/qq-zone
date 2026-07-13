<p align="center">
  <h1 align="center">🚀 QQ-Zone Album Backup Tool</h1>
  <p align="center"><b>一款极简、好用的 QQ 空间相册备份工具，守护您的数字回忆。</b></p>
  <p align="center">
    <a href="https://github.com/qinjintian/qq-zone/releases">
      <img src="https://img.shields.io/github/v/release/qinjintian/qq-zone?color=blue&include_prereleases&style=flat-square" alt="release">
    </a>
    <a href="https://golang.org/">
      <img src="https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat-square&logo=go" alt="Go Version">
    </a>
    <a href="https://github.com/qinjintian/qq-zone/blob/main/LICENSE">
      <img src="https://img.shields.io/badge/License-MIT-green?style=flat-square" alt="License">
    </a>
    <a href="https://github.com/qinjintian/qq-zone">
      <img src="https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey?style=flat-square" alt="platform">
    </a>
    <a href="https://github.com/qinjintian">
      <img src="https://img.shields.io/badge/Author-qinjintian-orange?style=flat-square" alt="Author">
    </a>
  </p>
</p>

---

## 🌟 简介

**QQ-Zone Album Backup Tool** 是一款专为简化流程而生的 QQ 空间相册备份工具。

在这个数字时代，QQ 空间承载了我们无数的青春记忆。然而，手动备份成千上万张照片不仅繁琐，还容易丢失原图质量。本项目采用 **Go 语言** 深度重构，支持 **原图无损下载** 并 **完整保留 EXIF 元信息**。只需通过 **手机 QQ 扫码登录** 即可一键开启自动化备份流程，将您的珍贵回忆以最原始的状态永久封存在本地。

> **Why choose this?** 拒绝画质压缩！拒绝繁琐的 Cookie 复制！我们提供的是**丝滑**的备份体验。

> **💡 小白用户请看这里**
> 
> 如果你不懂代码、不会配置 Go 语言环境，**请不要点击右上角的绿色 `Code` 按钮下载源码！**
> 
> 请直接点击下方链接，进入发布页面下载已经打包好的现成程序，双击即可运行：
> 
> 👉 [**点击前往 Release 页面下载最新版可执行程序**](https://github.com/qinjintian/qq-zone/releases/latest)
>
> *(Windows 用户请下载 `qq-zone-win.exe`，Mac 用户请下载带有 `macos` 字样的文件)*

---

## ✨ 核心特性

- **💻 极致体验**：无头浏览器零依赖，扫码即下，告别繁琐配置。流式实时进度展示，带来丝滑的 CLI 视觉体验。
- **⏱️ 时光重塑**：**【王炸功能】** 完美修复本地相册时间线！下载完成自动注入原始拍摄时间（OS Time 覆写），导入手机相册时间线绝出错乱。
- 📸 **原图无损下载**：支持获取相册原始分辨率图片，**完整保留 EXIF 元信息**（拍摄时间、地点、设备等），回忆不失真。
- 🎬 **实况图 (Live Photo) 全兼容**：完美支持 **MVIMG** 格式，自动识别并同时备份图片与对应的视频组件，还原动态瞬间。
- 👥 **多账号丝滑管理**：支持 **多账号登录记录** 自动保存与切换，提供历史账号列表一键登录，无需重复扫码，效率倍增。
- 📱 **极简登录与持久化**：支持 **CLI 终端直接显示二维码** 扫码即登；集成 **Session 持久化**，一次登录，长期有效。
- ⚡ **智能并发与风控防御**：内置 Worker Pool 动态扩缩容引擎，自动感知网络状态踩油门。遭遇腾讯风控时自动触发指数退避（Exponential Backoff）冷却重试，拒绝封禁。
- 🛡️ **优雅停机 (Graceful Shutdown)**：安全第一！支持 `Ctrl+C` 安全退出，拦截中断信号保护下载流，绝不产生损坏的残缺文件。
- 🎨 **大厂级 CLI 审美**：集成 `mpb` 实现动态清理式多进度条，支持 **MiB/s 实时速率** 与 **自动单位换算**，界面紧凑优雅。
- 📁 **智能归档与整理**：支持按 **拍摄时间 (Timeline)** 自动将媒体文件按 `年/月` 归档，并可同步导出相册 **JSON 元数据**（描述、点赞等）。
- 🔍 **智能扫描**：一键扫描所有对自己开放权限的好友空间，使用 `tablewriter` 渲染结构化权限报告。
- 🛡️ **安全与稳健**：内置 `golang.org/x/time/rate` **令牌桶限流算法**，智能规避风控；支持全局 `Context` 优雅中断 (Ctrl+C)。
- 🛠️ **规范化架构**：基于 `uber-go/fx` 依赖注入框架，代码遵循 **Standard Go Project Layout**，生产环境就绪。

---

## 🚀 快速开始

### 方式一：直接运行 (推荐)
- **从 Release 下载**：前往 [Releases](https://github.com/qinjintian/qq-zone/releases) 页面下载最新的 `qq-zone.exe`。
- **从仓库 bin 目录获取**：本仓库 `bin/` 目录下已预置各平台编译好的可执行程序，您可以直接下载对应版本：
  - Windows: `bin/qq-zone-win.exe`
  - Linux: `bin/qq-zone-linux`
  - macOS: `bin/qq-zone-macos-intel` 或 `bin/qq-zone-macos-m-series`

### 方式二：源码编译
1. **克隆仓库**
   ```bash
   git clone https://github.com/qinjintian/qq-zone.git
   cd qq-zone
   ```
2. **编译项目**
   - **全平台编译 (推荐)**：
     ```powershell
     ./scripts/build.ps1
     ```
     编译产物将存放在 `bin/` 目录下。
   - **仅编译当前平台**：
     ```bash
     go build -o bin/qq-zone.exe ./cmd/qq-zone
     ```
3. **启动程序**
   ```bash
   ./bin/qq-zone-win.exe      # Windows (amd64)
   ./bin/qq-zone-linux        # Linux (amd64)
   ./bin/qq-zone-macos-intel  # macOS (Intel)
   ./bin/qq-zone-macos-m-series # macOS (Apple Silicon)
   ```

## 📖 使用指南

![Usage Guide](docs/images/demo.png)

1. **启动程序**：运行编译好的可执行文件。
2. **扫码登录**：程序启动后会直接在 **CLI 终端显示二维码**，同时在项目根目录下生成 `qrcode.png`。请使用手机 QQ 扫码并确认登录。
3. **功能选择**：登录成功后，通过键盘方向键 `↑` `↓` 选择功能，按 `Enter` 确认。
   - **下载自己相册**：备份当前登录账号的所有媒体文件。
   - **下载好友相册**：输入好友 QQ 号，备份其公开相册。
   - **查看对我开放的好友**：一键扫描好友空间权限。
4. **任务配置**：根据提示设置并发数（建议 1-10）及是否开启增量下载。
5. **相册勾选**：在交互式列表中使用 `Space` (空格) 勾选想要备份的相册，按 `Enter` 开始下载。

---

## 🛠️ 技术栈

| 类别 | 选用方案 |
| :--- | :--- |
| **核心语言** | Go 1.23+ (Modern Features) |
| **依赖注入** | [uber-go/fx](https://github.com/uber-go/fx) |
| **进度渲染** | [vbauerster/mpb/v8](https://github.com/vbauerster/mpb) (Industrial Bars) |
| **HTTP 客户端** | [go-resty/resty/v2](https://github.com/go-resty/resty) |
| **交互式菜单** | [survey/v2](https://github.com/AlecAivazis/survey) |
| **频率限制** | `golang.org/x/time/rate` (Token Bucket) |
| **表格渲染** | [olekukonko/tablewriter](https://github.com/olekukonko/tablewriter) |
| **日志系统** | [uber-go/zap](https://github.com/uber-go/zap) |
| **并发控制** | `golang.org/x/sync/errgroup` |
| **JSON 解析** | [gjson](https://github.com/tidwall/gjson) |

---

## 📁 项目结构

遵循 **Standard Go Project Layout** 规范：

```text
.
├── .github/            # GitHub Actions 自动化构建与发布流水线
├── bin/                # 编译产物 (由于规范不入库，可执行文件请前往 Releases 下载)
├── assets/             # 存放静态资源 (app.ico, app.manifest 等)
├── cmd/                # 程序入口
│   └── qq-zone/        # 主程序入口
├── docs/               # 项目文档与演示资料
├── internal/           # 私有业务逻辑
│   ├── app/            # 核心爬虫引擎
│   ├── cli/            # 终端交互界面
│   ├── net/            # 网络请求封装
│   ├── pkg/            # 公共工具包 (Logger, Util)
│   └── qzone/          # QQ 空间协议实现
├── scripts/            # 跨平台编译脚本 (支持 Windows 下构建全平台二进制程序)
├── storage/            # 数据存储 (日志、相册)
└── go.mod              # 依赖管理
```

---

## 🤝 贡献与反馈

如果您觉得这个项目对您有帮助，请给一个 **⭐ Star**！这是对作者最大的鼓励。

- **作者**: qinjintian
- **邮箱**: [514092640@qq.com](mailto:514092640@qq.com)
- **问题反馈**: 请提交 [Issues](https://github.com/qinjintian/qq-zone/issues)

---

<p align="center">
  <b>Made with ❤️ by qinjintian</b>
</p>
