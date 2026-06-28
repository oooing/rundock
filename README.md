# Windows AI 启动平台（Launcher）

把任意 `start.bat` / `.cmd` / `.ps1` 拖进窗口，平台会**自动识别项目、隐藏窗口运行、实时采集日志、自动发现 URL 与端口、统一启停**。

> 核心定位：懂 Windows 本地项目脚本语义的、可解释的、无窗口应用启动控制台。

## 技术栈

| 层 | 技术 | 说明 |
|---|---|---|
| 桌面壳 | **Tauri 2**（Rust） | 窗口、托盘、spawn sidecar、最小权限 |
| 核心后端 | **Go sidecar** | 全部业务逻辑 + SQLite + HTTP/WebSocket |
| 前端 | **Vue 3 + Vite + TS + Pinia** | 卡片式控制面板 |
| 存储 | **SQLite**（modernc.org/sqlite，纯 Go 免 CGO） | 配置、运行记录、日志索引 |

架构：Tauri Rust 壳启动时 spawn Go sidecar（固定端口 17654），前端通过 `http://127.0.0.1:17654` REST + `/ws` WebSocket 直连 sidecar。所有进程托管状态、日志、端口发现都在 Go 侧，避免 Rust↔Go 双写。

## 目录结构

```
code/
├── sidecar/                 # Go 核心后端
│   ├── cmd/launcher-sidecar/main.go   # 入口
│   └── internal/
│       ├── store/    # SQLite 持久化 + migrations
│       ├── config/   # 数据目录定位
│       ├── app/      # 状态机 + 运行时表
│       ├── proc/     # 进程管理 + Windows Job Object
│       ├── adapter/  # batch/ps1/npm/yarn/pnpm 适配器
│       ├── importer/ # 拖入解析 + 项目根推断
│       ├── probe/    # 端口快照 + URL 抽取 + HTTP 健康检查
│       ├── logbus/   # 日志采集 + 事件流 + WS 广播
│       ├── security/ # 风险扫描 + 哈希白名单
│       └── launcher/ # 编排层（启停重启闭环）
├── src-tauri/              # Tauri Rust 壳
│   ├── src/{main,lib}.rs   # spawn sidecar + 端口注入前端
│   └── tauri.conf.json     # externalBin + 窗口 + 能力
├── src/                    # Vue 3 前端
│   ├── views/ Dashboard
│   ├── components/ AppCard / ConfirmCard / LogDrawer / GroupSidebar / SettingsModal
│   ├── stores/ apps / groups / connection
│   └── api/ http + ws 客户端
└── scripts/ build-sidecar.bat / dev.bat
```

## 核心能力

- **拖拽导入**：拖入脚本 → 只读分析项目根/类型/哈希/风险 → 确认卡 → 生成 App
- **隐藏窗口启动**：`cmd /d /s /c call` + `CREATE_NO_WINDOW`，无黑窗弹出
- **进程树回收**：每个 App run 独立 **Windows Job Object**（`KILL_ON_JOB_CLOSE`），停止分级（Ctrl-Break → grace → `taskkill /t /f`）
- **URL/端口自动发现**：日志正则（`Local: http://localhost:5173` 等）+ 端口快照对比 + HTTP 健康检查三段式
- **实时日志**：stdout/stderr 全量落库 + WebSocket 推流 + 历史搜索
- **状态机**：starting → running / degraded / failed / stopped
- **安全基线**：风险模式高亮（删目录/格式化/注册表/编码命令等）+ 哈希白名单 + 首次确认
- **分组 / 标签 / 配置导入导出**

## 开发

### 环境要求
- Node.js ≥ 18
- Go ≥ 1.23（或用官方免安装版解压到任意目录）
- Rust + Tauri CLI（`cargo install tauri-cli --version "^2"`）—— 打包桌面应用时需要
- Windows 10/11（核心 Job Object / netstat 能力为 Windows 实现）

### 方式 A：纯前端 + sidecar（最快调试，浏览器即可）

```bat
:: 1. 装前端依赖（首次）
cd code
npm install

:: 2. 起 sidecar（固定 17654）+ Vite
scripts\dev.bat
:: 浏览器打开 http://localhost:1420
```

### 方式 B：Tauri 桌面壳（端到端）

```bat
:: 1. 编译 sidecar 到 Tauri binaries 目录（带 target-triple 后缀）
scripts\build-sidecar.bat

:: 2. 安装 Tauri CLI（首次）
cargo install tauri-cli --version "^2"

:: 3. 桌面开发模式（壳自动起 sidecar + 前端）
cd code
cargo tauri dev
```

### 测试

```bat
cd code\sidecar
go test ./...          :: 后端单测：URL 抽取 / netstat 解析 / 风险扫描
```

## 数据位置

- 数据目录：`%APPDATA%\launcher-sidecar\`
- SQLite：`launcher.db`
- 端口发现：`sidecar.port`（壳据此连接 sidecar）

## 安全说明

平台始终把"运行脚本"视为**执行任意代码**：

1. 导入只读分析，绝不执行。
2. 首次启动前必须用户确认（确认卡列出入口/工作目录/命令/环境变量/风险高亮）。
3. 脚本内容（SHA256）+ 路径未变才免再确认；变化则重新要求确认。
4. 应用本体默认标准用户权限运行，不提权。
