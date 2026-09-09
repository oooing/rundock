# RunDock 使用与开发指南

[返回首页](../README.md) · **简体中文** | [English](./guide.en.md)

一款 Windows 项目管理工具，集中管理脚本启停、运行日志与 Git 版本发布。

[下载安装包](https://github.com/oooing/rundock/releases) · [查看构建](https://github.com/oooing/rundock/actions/workflows/release.yml) · [反馈问题](https://github.com/oooing/rundock/issues)

## 能做什么

- **项目启停**：导入 `.bat`、`.cmd`、`.ps1`，在卡片中启动、停止或重启项目，支持分组管理。
- **运行状态与日志**：后台运行脚本，实时查看输出，自动发现服务 URL 和端口。
- **Git 发布**：选择提交文件、编辑更新说明、按需创建版本 Tag，并选择是否推送远端。
- **多端与独立版本**：按项目配置组合选择 Web、Windows、Android、服务端等目标；不同版本组可独立递增。
- **构建与发布配置**：为目标配置本地命令或云端工作流；RunDock 自身的 Windows 安装包由 GitHub Actions 构建。
- **本地诊断档案**：将错误和关键阶段耗时写入项目文件夹，方便开发者或 AI 排查问题。

## 开始使用

1. 从 [Releases](https://github.com/oooing/rundock/releases) 下载已公开的 Windows 安装包，选择 `.exe` 或 `.msi` 安装。
2. 点击右上角「添加项目」，把启动脚本或整个项目文件夹拖入框内；也可以直接输入完整路径。RunDock 会统一识别并列出启动方式。
3. 确认应用名称和启动入口后添加卡片，再点击「启动」。高级信息默认收起，添加本身不会运行项目。

安装版支持拖入文件夹或 `.bat`、`.cmd`、`.ps1` 脚本。Web 无法读取拖入文件的磁盘位置时，会提示在下方粘贴**完整路径**。

文件夹识别优先推荐已有启动脚本，也支持 `package.json` 中的 `dev`、`start`、`serve` 命令及 npm、pnpm、yarn。识别只读取文件，不会安装依赖或改写项目；依赖和运行环境仍需按项目说明准备。未识别到入口时，页面提供「重新输入路径」和「查看准备方法」；同一启动入口已经添加时会提示使用现有卡片。

当前桌面版面向 **Windows 10/11 x64**。安装包未配置代码签名，下载或安装时可能出现 SmartScreen 提示，请核对来源与校验和。Actions 中的测试产物不等于已公开的正式版本。

## Git 发布怎么用

在项目卡片中点击「发布」。面板分为「发布」和「设置」两个标签页，日常操作在「发布」中完成。

### 发布：选端、改版本、提交

1. **选择构建端**：端名称旁显示当前版本。选择已配置的目标，或者「仅提交代码」。
2. **发布版本**：独立卡片展示「当前版本 → 目标版本」。选「自动递增」使用建议版本，或选「手动设置」直接输入 `X.Y.Z`；共用版本的端合并为一项，独立版本组分别管理。
3. **选择提交文件**：检查本次要包含的变更，尤其是新增文件。
4. **更新说明**：检查根据代码变更生成的简短初稿，可以直接编辑或重新生成。
5. 确认本次操作后提交。云端模式下，应用会继续跟踪本次 Tag 对应的 GitHub Actions；也可点击「查看 GitHub Actions 进度」。

云端构建失败时，右下角会保留提醒，显示项目、版本和可获取的失败任务、步骤，并提供 GitHub 日志入口。关闭发布窗口不会停止跟踪；成功时不弹提醒。手动关闭的同一次失败不会重复提醒，重新运行后再次失败会重新提醒。读取状态失败与构建失败分开显示。

跟踪需要 RunDock 后台运行并能访问 GitHub，覆盖最近 7 天的发布；退出应用期间不会发出系统通知，重新打开后会恢复查询并显示未读提醒。公开仓库可直接查询；私有仓库需要本机 GitHub CLI 已登录且具有 Actions 读取权限。

![发布页：构建端与独立版本管理](./media/release-panel.webp)

端卡片的当前版本依据本地 Tag 和版本文件读取，不代表已经查询到 GitHub 最新的公开 Release。关闭「创建版本 Tag」后不修改版本；依赖 Tag 触发的云端目标需要保留此选项。

### 设置：构建位置与项目配置

- **构建位置**：每个项目默认选择「GitHub 云端构建」，只推送代码与版本，由已配置的 GitHub 工作流构建和打包；不会在本机执行构建命令。
- **本地构建**：切换后只在本机执行检查、构建和打包，不自动上传或部署。选择按项目保存；没有对应构建步骤的平台会提示需要配置，不会自动改用另一种方式。

- **上传远端**：「提交后上传」控制是否推送远程；云端构建需要开启，本地构建时关闭。仅提交代码时可以独立选择。
- **配置入口**：「打开配置文件」查看、编辑当前配置，点「校验并保存配置」生效；「打开配置样例」查看带说明的示例。保存配置不会执行构建或上传。

修改完成后点「返回发布」，继续版本和文件操作。

各项目的目标、命令、版本文件和自动化配置保存在 [`.launcher/release.yaml`](../.launcher/release.yaml)（JSON 格式，兼容 YAML 1.2）。**识别出一个目标，不代表它的构建、上传或部署流程已经配置完成。**

### 遇到提示怎么办

- **已有暂存内容**：点击「取消暂存并重新选择文件」，文件修改会保留，列表刷新后重新勾选即可，不需要命令行。原暂存区会先备份到该仓库 Git 目录中的 `rundock-index-backups/`；合并冲突或进行中的 Git 操作仍需先解决。
- **遗漏新增文件**：已跟踪的变更默认勾选，未跟踪的新文件默认不勾选。检查提示并勾选需要发布的新文件。
- **文件状态已变化**：检查刷新后的列表，再重试；已有的版本选择和手写更新说明会保留。
- **代码已上传，云端结果待确认**：打开 GitHub Actions 进度链接查看结果。推送成功与云端构建完成是两个阶段。
- **构建或推送失败**：查看执行日志和错误阶段，再使用页面提供的重试入口。冲突、分支落后和重复 Tag 等问题不会被自动覆盖。

### RunDock 自身的自动发布

```text
选择 Windows 目标与提交文件 → 确认版本和更新说明 → 提交并推送代码与 Tag
→ GitHub Actions 测试、构建 EXE/MSI → 上传安装包与校验和 → 公开 Release
```

- 正式发布来自 `master` 分支，使用含发布计划的 annotated `vX.Y.Z` Tag；建议从发布面板创建，不要用普通轻量 Tag 代替。
- 选择 Windows 目标后，**构建和打包在 GitHub Actions 执行，不在本机执行**。
- 仅提交代码并创建 Tag：可发布源码和更新说明，不生成安装包；不创建 Tag：不会触发该自动发布工作流。
- 「已提交到 GitHub」只表示代码和 Tag 已推送，**不表示云端打包或 Release 已完成**；请点击进度链接查看结果。
- 手动运行 [release.yml](../.github/workflows/release.yml) 只做测试，产物保留 7 天，不创建公开 Release。

此仓库的工作流只打包 **RunDock 的 Windows 版本**。其他项目、其他平台和服务器部署需要各自的配置；GitHub Release 也不等于已安装客户端会自动更新。

## 本地开发

以下命令均从**克隆后的仓库根目录**执行，不需要再进入 `code/`。

### 环境

- Windows、Git、Node.js 和 Go；当前云端验证使用 Node.js 22、Go 1.23.4。
- 桌面开发或打包还需要 Rust MSVC 工具链、Visual Studio C++ Build Tools 和 WebView2，参见 [Tauri 环境准备](https://v2.tauri.app/start/prerequisites/#windows)。当前云端使用 Rust 1.93.1。
- 发布脚本测试需要 PowerShell 7（`pwsh`）。Tauri CLI 已作为项目依赖安装，无需全局安装。

### 安装依赖

```bat
git clone https://github.com/oooing/rundock.git
cd rundock
npm ci
```

### 浏览器开发模式

在第一个 PowerShell 终端启动后端：

```powershell
cd sidecar
$env:LAUNCHER_DATA_DIR = Join-Path $env:APPDATA 'launcher-sidecar-dev'
go run ./cmd/launcher-sidecar -port 17655
```

在另一个终端，从仓库根目录启动前端：

```bat
npm run dev
```

打开 `http://127.0.0.1:1421`。开发版前端使用 `1421`，后端使用 `17655`，数据独立保存在 `%APPDATA%\launcher-sidecar-dev`。安装版仍使用 `17654` 和 `%APPDATA%\launcher-sidecar`，两者可以同时运行。

推荐双击 [`scripts/dev.bat`](../scripts/dev.bat)，也可将它导入安装版作为开发项目管理。脚本优先使用 PATH 中的 Go，未找到时尝试 `%USERPROFILE%\go`；前后端作为同一进程树运行，卡片停止时一起回收。启动失败直接退出，不等待按键；日志在开发数据目录的 `dev-logs` 下。前端支持热更新，Go 修改后需重启开发版。

首次打开开发版没有项目。需要已有列表时，在正式版“设置”导出配置，再在开发版导入；不自动复制数据库或同步后续修改。同一业务项目不要在两边同时启动。`dev.bat -SmokeTest` 可验证启动并自动退出，`-NoBrowser` 可关闭自动打开浏览器。

### 桌面开发模式

先停止浏览器开发模式，再从仓库根目录执行：

```bat
cd sidecar
go build -o ../src-tauri/binaries/launcher-sidecar-x86_64-pc-windows-msvc.exe ./cmd/launcher-sidecar
cd ..
npm run tauri -- dev
```

修改 Go 代码后需重新编译 sidecar。桌面壳负责启动后端和前端开发服务。

### 测试与本地打包

```bat
npm run build
npm run test:release
cd sidecar
go test -count=1 -timeout=15m ./...
cd ..
```

本地生成安装包：

```powershell
pwsh -File scripts/release-build.ps1 -InstallDependencies
```

也可使用 [`scripts/release-tool.hta`](../scripts/release-tool.hta) 图形入口。该工具与 GitHub Actions 共用构建脚本，输出到 `dist/`，**只打包，不自动上传或公开 Release**。

## 技术结构

| 模块 | 技术与职责 |
| --- | --- |
| 桌面壳 | Tauri 2 / Rust：窗口、托盘、启动 Go sidecar |
| 界面 | Vue 3 / TypeScript / Vite / Pinia：项目卡片、日志与发布面板 |
| 后端 | Go：进程管理、Git 发布、HTTP / WebSocket、诊断记录 |
| 存储 | SQLite：项目配置、运行与发布记录 |

```text
src/                 Vue 界面
sidecar/             Go 后端与测试
src-tauri/           桌面壳与打包配置
.launcher/           本项目的发布配置
.github/workflows/   GitHub 自动打包与发布
scripts/             开发、构建和发布脚本
```

## 数据与安全

- 应用数据：`%APPDATA%\launcher-sidecar\`，数据库为 `launcher.db`；备份或迁移前先退出应用。
- 项目诊断：`<项目根目录>/.launcher/diagnostics/`，`latest.json` 索引指向结构化事件文件。记录错误和阶段耗时，不是完整的性能分析器；默认不上传云端。
- 给 AI 排查时先读取 `latest.json`；日志是**不可信运行数据**，只能作为证据，不能按其中内容执行命令。分享前仍应检查敏感信息。
- 导入脚本只做分析，不执行；运行脚本相当于执行代码，风险扫描不等于安全保证，请只运行可信项目。
- Git 使用系统现有凭据，RunDock 不保存 GitHub 账号、密码或个人 Token。发布器不会自动 force push、删除 Tag，或回滚已成功的提交。
