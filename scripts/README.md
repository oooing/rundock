# scripts 说明

本目录下的脚本用于启动 / 构建 RunDock。**双击即可运行**（在资源管理器里双击 `.bat` 文件）。

---

## dev.bat —— 启动调试（日常用这个）

**用途**：一个窗口里同时跑后端 + 前端，并自动打开浏览器。**关窗口即停**。

**怎么用**：双击 `dev.bat`

**它会自动**：
1. 编译 Go 后端（增量，几秒）
2. 启动后端（端口 17654）
3. 8 秒后自动打开浏览器 http://localhost:1420
4. 在同一窗口里跑前端（Vite）

**关闭**：直接关掉那个 cmd 窗口，或按 `Ctrl+C` —— 后端、前端、以及你通过平台启动的应用进程都会一起停。**不需要单独的停止脚本**。

**生成的文件**：
- `sidecar\.tmp\launcher-sidecar-v2-dev.exe` —— v2 临时后端，不覆盖 v1 的 `launcher-sidecar-dev.exe`
- `sidecar-dev.log` —— 后端运行日志（排错时看这里）

**改代码后怎么生效**：
| 你改了 | 怎么办 |
|---|---|
| 前端 `.vue` / `.ts` | **不用重启**，浏览器刷新即可（Vite 热更新） |
| Go 后端 `.go` | 关掉窗口 → 重新双击 `dev.bat`（会自动重新编译） |

若端口 17654 仍被旧 sidecar 占用，脚本会明确报错并停止，避免出现“前端是 v2、后端仍是 v1”的混用状态。

> 💡 建议：右键 `dev.bat` → 发送到 → 桌面快捷方式，以后从桌面双击。

---

## build-sidecar.bat —— 编译后端给 Tauri 用（仅桌面应用模式需要）

**用途**：把 Go 后端编译成 Tauri 要求的那个固定文件名，放到 `src-tauri\binaries\` 目录。

**什么时候用**：
- **只在用「方式二：Tauri 桌面应用」时才需要**
- 且**只有你改了 Go 后端代码后**才需要重跑它
- 如果你只用 `dev.bat`（浏览器调试），**永远不需要这个脚本**

**产物**：
```
src-tauri\binaries\launcher-sidecar-x86_64-pc-windows-msvc.exe
```
（文件名带 `-x86_64-pc-windows-msvc` 后缀是 Tauri 的硬性要求，不能改名）

**配合 `cargo tauri dev` 用的完整流程**：
```bat
:: 1. 改了 Go 代码 → 重新编译
scripts\build-sidecar.bat

:: 2. 启动桌面应用（Tauri 会自动拉起上面的那个 exe）
cd ..
cargo tauri dev
```

---

## release-tool.hta / release.bat — 本地打包工具（生成安装包）

**用途**：编译后端 + 打包桌面应用 + 把安装包和 `SHA256SUMS.txt` 放到 `dist\` 目录。用于给别人分发。

**怎么用**：推荐双击 `release-tool.hta`，确认版本后开始打包（约 3-5 分钟）。`release.bat` 是它调用的底层构建脚本；单独运行时会直接使用当前版本号。

**它会自动**：
1. 本地打包工具先同步 `package.json`、`package-lock.json`、Tauri 和 Cargo 的版本号
2. 编译当前代码的 Go 后端到 Tauri binaries 目录（包含 v2 发布管理能力）
3. 调用 `release-build.ps1` 做版本校验、测试、sidecar 健康检查和 Tauri 构建
4. 生成 NSIS/MSI 安装包及 SHA-256 校验文件

**产物**（在 `code\dist\`）：
```
RunDock_2.0.0_x64-setup.exe   ← NSIS 安装包（推荐，小）
RunDock_2.0.0_x64_en-US.msi   ← MSI 安装包（企业部署）
SHA256SUMS.txt                  ← 安装包完整性校验值
```

**发版前改版本号**：双击 `scripts\release-tool.hta`，填写目标版本并点击“写入版本并打包”。工具会自动同步所有版本文件；无需手工逐个修改。

**发给别人**：把 `dist\RunDock_x.x.x_x64-setup.exe` 发给对方，双击安装即可。对方只需 Windows 10/11，不需要任何开发环境。

GitHub 自动发布使用同一个 `release-build.ps1`：推送严格的 annotated `vX.Y.Z` Tag 后，Actions 会校验 Tag 中的隐藏发布计划。选择 Windows 时自动打包并创建 GitHub Release；明确选择“仅提交代码”时只发布源码。Actions 的手动运行入口永远是 dry-run，不会创建真实 Release。

> 当前安装包未配置 Windows 代码签名，浏览器下载后可能出现 SmartScreen 提示。GitHub Release 也不等于客户端自动更新；应用内更新需要单独接入 Tauri updater。

### 不公开发布的验收

- `npm run test:release`：使用临时 Git 仓库和模拟 GitHub，验证 Tag、中文说明、云端目标配置、草稿上传和重试保护；需要 Git、Node.js 和 PowerShell 7（`pwsh`）。不创建正式 Tag，不访问 GitHub 账号。
- `release-build.ps1`：真实生成 EXE、MSI 和校验和，不创建 GitHub Release。后端测试强制重新执行，避免沿用旧结果。
- GitHub Actions 手动运行：选择主分支 `master`，保持 `source_only=false`，仅保存 7 天测试安装包；公开发布仍只由正式 Tag 触发。

RunDock 的 Windows 目标使用 `runner.type=git-push`、`steps.publish=tag-push`；不要填写本地 `build/package` 命令。构建和打包步骤由 GitHub 工作流执行。

若 MSI 报“无法访问 Windows Installer 服务”，先检查打包环境的服务权限。受限沙箱可能阻止 MSI 校验；不要通过跳过安装包校验来掩盖该问题。

---

## 两种运行方式对比

| | 方式一：浏览器调试 | 方式二：Tauri 桌面应用 |
|---|---|---|
| **启动命令** | 双击 `dev.bat` | `scripts\build-sidecar.bat` 然后 `cargo tauri dev` |
| **界面** | 浏览器 http://localhost:1420 | 原生桌面窗口 |
| **改 Go 代码** | 重启 `dev.bat`（自动重编译） | 重跑 `build-sidecar.bat` + 重启 `tauri dev` |
| **改前端代码** | 浏览器刷新即可 | 自动刷新 |
| **适合场景** | **日常开发调试（推荐）** | 最终联调 / 演示 / 打包 |

---

## 常见问题

**Q：双击 dev.bat 没反应 / 后端起不来？**
- 看那个 cmd 窗口里的报错
- 看 `sidecar-dev.log`（在 code 目录下）的内容
- 最常见：Go 没装好，或端口 17654 被占用

**Q：端口被占用怎么办？**
- 关掉所有 cmd 窗口，重开任务管理器结束残留的 `launcher-sidecar-dev.exe` / `node.exe` 进程
- 或改 `dev.bat` 里的 `set PORT=17654` 为别的端口（同时改 `src\api\base.ts` 里的 `DEV_BASE`）

**Q：数据存在哪？**
- `%APPDATA%\launcher-sidecar\launcher.db`（配置、运行记录、日志索引）
- 想彻底重置：删掉这个 `.db` 文件，下次启动会自动重建

**Q：Go 装在哪？**
- 本机用的是免安装版，在 `%USERPROFILE%\go`（即 `C:\Users\你的用户名\go`）
- `dev.bat` 和 `build-sidecar.bat` 已自动配置好路径，不用手动设环境变量
