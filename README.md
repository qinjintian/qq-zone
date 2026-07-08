# 🚀 QQ-Zone Album Backup Tool

<p align="center">
  <img src="https://coresg-normal.trae.ai/api/ide/v1/text_to_image?prompt=A%20sleek%20modern%20terminal%20interface%20showing%20a%20rocket%20launching%20with%20digital%20photos%20flying%20into%20a%20folder%2C%20cyberpunk%20aesthetic%2C%20high%20quality%2C%20blue%20and%20cyan%20tones&image_size=landscape_16_9" alt="Banner" width="100%">
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.23+-00ADD8?style=for-the-badge&logo=go" alt="Go Version">
  <img src="https://img.shields.io/badge/License-MIT-green?style=for-the-badge" alt="License">
  <img src="https://img.shields.io/badge/Architecture-Standard%20Go-blueviolet?style=for-the-badge" alt="Architecture">
  <img src="https://img.shields.io/badge/Author-qinjintian-orange?style=for-the-badge" alt="Author">
</p>

---

## 🌟 简介

**QQ-Zone Album Backup Tool** 是一款专为简化流程而生的 QQ 空间相册备份工具。

在这个数字时代，QQ 空间承载了我们无数的青春记忆。然而，手动备份成千上万张照片不仅繁琐，还容易丢失原图质量。本项目采用 **Go 语言** 深度重构，支持 **原图无损下载** 并 **完整保留 EXIF 元信息**。只需通过 **手机 QQ 扫码登录** 即可一键开启自动化备份流程，将您的珍贵回忆以最原始的状态永久封存在本地。

> **Why choose this?** 拒绝画质压缩！拒绝繁琐的 Cookie 复制！我们提供的是**丝滑**的备份体验。

---

## ✨ 核心特性

- 📸 **原图无损下载**：支持获取相册原始分辨率图片，**完整保留 EXIF 元信息**（拍摄时间、地点、设备等），回忆不失真。
- 🎬 **实况图 (Live Photo) 全兼容**：完美支持 **MVIMG** 格式，自动识别并同时备份图片与对应的视频组件，还原动态瞬间。
- 👥 **多账号丝滑管理**：支持 **多账号登录记录** 自动保存与切换，提供历史账号列表一键登录，无需重复扫码，效率倍增。
- 📱 **极简登录与持久化**：支持 **CLI 终端直接显示二维码** 扫码即登；集成 **Session 持久化**，一次登录，长期有效。
-  ⚡ **工业级下载引擎**：基于 `errgroup` 的多协程并发，配合 **HTTP Range 断点续传** 与 **文件完整性校验**，大文件下载稳如泰山。
- 🎨 **大厂级 CLI 审美**：集成 `mpb` 实现动态清理式多进度条，支持 **MiB/s 实时速率** 与 **自动单位换算**，界面紧凑优雅。
- 📁 **智能归档与整理**：支持按 **拍摄时间 (Timeline)** 自动将媒体文件按 `年/月` 归档，并可同步导出相册 **JSON 元数据**（描述、点赞等）。
- 🔍 **智能扫描**：一键扫描所有对自己开放权限的好友空间，使用 `tablewriter` 渲染结构化权限报告。
- 🛡️ **安全与稳健**：内置 `golang.org/x/time/rate` **令牌桶限流算法**，智能规避风控；支持全局 `Context` 优雅中断 (Ctrl+C)。
- 🛠️ **规范化架构**：基于 `uber-go/fx` 依赖注入框架，代码遵循 **Standard Go Project Layout**，生产环境就绪。

---

## 🚀 快速开始

### 方式一：直接运行 (推荐)
前往 [Releases](https://github.com/qinjintian/qq-zone/releases) 页面下载最新的 `qq-zone.exe`，双击运行即可。

### 方式二：源码编译
1. **克隆仓库**
   ```bash
   git clone https://github.com/qinjintian/qq-zone.git
   cd qq-zone
   ```
2. **编译项目**
   ```bash
   go build -o qq-zone.exe ./cmd/qq-zone
   ```
3. **启动程序**
   ```bash
   ./qq-zone.exe
   ```

## 📖 使用指南

![Usage Guide](docs/assets/demo.png)

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
| **核心语言** | Go 1.25+ (Modern Features) |
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
├── cmd/                # 程序入口
│   └── qq-zone/        # 主程序入口
├── docs/               # 项目文档及静态资源
│   └── assets/         # 演示图及素材
├── internal/           # 私有业务逻辑
│   ├── app/            # 核心爬虫引擎
│   ├── cli/            # 终端交互界面
│   ├── net/            # 网络请求封装
│   ├── pkg/            # 公共工具包 (Logger, Util)
│   └── qzone/          # QQ 空间协议实现
├── storage/            # 数据存储 (日志、相册、二维码)
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
