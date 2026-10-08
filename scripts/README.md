# scripts 说明

本目录下的脚本用于启动、构建和验收 RunDock。文档分类见 [文档索引](../docs/README.md)。

---

## dev.bat —— 启动调试（日常用这个）

**注意**：关闭开发窗口或按 `Ctrl+C`，会同时关闭开发服务及通过该实例启动的子项目。需要保留项目时，不要退出或重启开发脚本。

先在 `code` 目录安装前端依赖（`npm ci`），确保 Go 和 Node.js 可用，再双击 `dev.bat`。

脚本先检查端口，编译 Go 后端，再启动后端和 Vite；两者就绪后才打开浏览器。

| 项目 | 开发版默认位置 |
| --- | --- |
| 界面 | http://127.0.0.1:17656/ |
| 后端 | http://127.0.0.1:17655/ |
| 数据 | `%APPDATA%\launcher-sidecar-dev\launcher.db` |
| 日志 | `%APPDATA%\launcher-sidecar-dev\dev-logs\<每次启动的独立目录>\` |
| 临时后端 | `sidecar\.tmp\launcher-sidecar-v2-dev.exe` |

日志目录包含 `backend.stdout.log`、`backend.stderr.log`、`frontend.stdout.log` 和 `frontend.stderr.log`，实际路径会打印在开发窗口中。
开发数据独立于正式版的 `%APPDATA%\launcher-sidecar`，不会覆盖正式版配置。

**改代码后怎么生效**：

| 你改了 | 怎么办 |
|---|---|
| 前端 `.vue` / `.ts` | **不用重启**，浏览器刷新即可（Vite 热更新） |
| Go 后端 `.go` | 新版启动器下，停止托管项目并等待构建/发布完成，再点 RunDock 卡片的「重启」；旧启动器首次升级需退出开发窗口 → 重新双击 `dev.bat` |

新版「重启 RunDock」仅让开发后台退出并重新编译启动，Vite 和当前页面保持打开。确认窗口显示重新连接进度；后台普通崩溃不会被当成重启请求自动重试。重启后的后台日志为 `backend-1.stdout.log` / `backend-1.stderr.log` 等；编译失败查看同一启动器日志。

端口被其他实例占用或被 Windows 保留时，脚本会报错并停止，不会自动结束占用程序。

> 💡 建议：右键 `dev.bat` → 发送到 → 桌面快捷方式，以后从桌面双击。

---

## build-sidecar.bat —— 编译后端给 Tauri 用（仅桌面应用模式需要）

**用途**：把 Go 后端编译成 Tauri 要求的那个固定文件名，放到 `src-tauri\binaries\` 目录。

**什么时候用**：

- **只在用「方式二：Tauri 桌面应用」时才需要**
- 且**只有你改了 Go 后端代码后**才需要重跑它
- 如果你只用 `dev.bat`（浏览器调试），**永远不需要这个脚本**

**产物**：

```text
src-tauri\binaries\launcher-sidecar-x86_64-pc-windows-msvc.exe
```
（文件名带 `-x86_64-pc-windows-msvc` 后缀是 Tauri 的硬性要求，不能改名）

**配合 Tauri 开发模式的完整流程**（在 `code` 目录）：

```bat
:: 1. 改了 Go 代码 → 重新编译
scripts\build-sidecar.bat

:: 2. 启动桌面应用（Tauri 会自动拉起上面的那个 exe）
npm run tauri -- dev
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

```text
RunDock_2.0.0_x64-setup.exe   ← NSIS 安装包（推荐，小）
RunDock_2.0.0_x64_en-US.msi   ← MSI 安装包（企业部署）
SHA256SUMS.txt                  ← 安装包完整性校验值
```

**发版前改版本号**：双击 `scripts\release-tool.hta`，填写目标版本并点击“写入版本并打包”。工具会自动同步所有版本文件；无需手工逐个修改。

**发给别人**：把 `dist\RunDock_x.x.x_x64-setup.exe` 发给对方，双击安装即可。对方只需 Windows 10/11，不需要任何开发环境。

GitHub 自动发布使用同一个 `release-build.ps1`：推送严格的 annotated `vX.Y.Z` Tag 后，Actions 会校验 Tag 中的隐藏发布计划。选择 Windows 时自动打包并创建 GitHub Release；明确选择“仅提交代码”时只发布源码。Actions 的手动运行入口永远是 dry-run，不会创建真实 Release。

> 当前安装包未配置 Windows 代码签名，浏览器下载后可能出现 SmartScreen 提示。客户端已接入安装包下载及 SHA-256 校验；GitHub Release 发布不代表所有运行中的客户端立即收到通知，目前自动检查仍在启动时进行，也可以在设置中手动检查。

### 客户端升级与安装语言

- 应用内确认“退出并安装”后，EXE 使用 NSIS `/UPDATE /P /R`：原位升级、显示进度、不弹出卸载选择，成功后重新打开客户端。MSI 使用 `/i /passive /norestart AUTOLAUNCHAPP=True`，不主动重启 Windows。校验、后台安全退出失败时仍停止升级并报告原因。
- EXE 内置 `English`、`SimpChinese`，不弹语言选择框。NSIS 按 Windows 显示语言匹配，中文各地区匹配简体中文，其他未支持语言回退到第一项英文，与客户端首次启动规则一致。客户端手动保存的语言不会被升级或系统检测覆盖。
- MSI 仍保留现有 `en-US` 企业部署包。WiX 多语言配置会生成多个独立 MSI，并非一个包自动切换语言；不能把 EXE 的语言行为套用于 MSI。
- 手动双击 EXE 仍是正常安装向导，应用内更新才使用进度模式；旧 `Launcher` 更名迁移以及 MSI/EXE 安装器之间迁移仍保留保护，不绕过迁移检查。
- 验证语言：设置 `RUNDOCK_MAKENSIS` 为 NSIS 编译器路径后执行 `node --test scripts/tests/installer-language.test.mjs`。仅编译并运行写入语言结果的隔离程序，覆盖 10 种语言，不安装真实客户端。

### 不公开发布的验收

- `npm run test:release`：使用临时 Git 仓库和模拟 GitHub，验证 Tag、中文说明、云端目标配置、草稿上传和重试保护；需要 Git、Node.js 和 PowerShell 7（`pwsh`）。不创建正式 Tag，不访问 GitHub 账号。
- `release-build.ps1`：真实生成 EXE、MSI 和校验和，不创建 GitHub Release。后端测试强制重新执行，避免沿用旧结果。
- `schema-upgrade.mjs`：真实 sidecar 启动、旧库升级和本地构建验收；Node.js 22.13+，使用合成数据、不接触当前项目。打包自动运行，报告见 `outputs/acceptance/schema-upgrade-*/`，手动命令见 [验收说明](../docs/acceptance/2026-10-04-schema-upgrade.md)。
- GitHub Actions 手动运行：选择主分支 `master`，保持 `source_only=false`，仅保存 7 天测试安装包；公开发布仍只由正式 Tag 触发。

RunDock 的 Windows 目标使用 `runner.type=git-push`、`steps.publish=tag-push`；不要填写本地 `build/package` 命令。构建和打包步骤由 GitHub 工作流执行。

若 MSI 报“无法访问 Windows Installer 服务”，先检查打包环境的服务权限。受限沙箱可能阻止 MSI 校验；不要通过跳过安装包校验来掩盖该问题。

---

## 两种运行方式对比

| | 方式一：浏览器调试 | 方式二：Tauri 桌面应用 |
|---|---|---|
| **启动命令** | 双击 `dev.bat` | `scripts\build-sidecar.bat` 然后 `npm run tauri -- dev` |
| **界面** | 浏览器 http://127.0.0.1:17656/ | 原生桌面窗口 |
| **改 Go 代码** | 重启 `dev.bat`（自动重编译） | 重跑 `build-sidecar.bat` + 重启 `tauri dev` |
| **改前端代码** | 浏览器刷新即可 | 自动刷新 |
| **适合场景** | **日常开发调试（推荐）** | 最终联调 / 演示 / 打包 |

---

## 常见问题

**Q：安装 RunDock 必须安装 PowerShell 7 吗？**

- 不需要。Windows `.ps1` 启动适配器默认使用系统自带的 `powershell.exe`；只有显式设置 `LAUNCHER_PWSH` 才覆盖启动宿主。
- 发布配置中的 `pwsh` 是项目自己声明的运行依赖，RunDock 不会偷偷替换为 PowerShell 5.1，也不会借用 Codex 的私有运行时。缺少工具时阻止检查并报告原因。
- 我们维护的 Windows 本地发布脚本以 PowerShell 5.1 为兼容基线；版本、签名、产物完整性检查保留，原生工具用独立参数调用。`-ExecutionPolicy Bypass` 仅限子进程，不改系统策略，组织策略优先。
- Node、JDK、Android SDK、Rust 等仍是具体项目本地构建的依赖，不是安装 RunDock 的前提。不要把“本地预检查通过”当作“安装包已构建/服务器已部署”。
- 依据：[Windows 默认包含 PowerShell 5.1](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_windows_powershell_5.1?view=powershell-5.1)、[5.1 与 7 的运行时差异](https://learn.microsoft.com/en-us/powershell/scripting/whats-new/differences-from-windows-powershell)、[Go 的独立进程执行](https://pkg.go.dev/os/exec)。这是本项目零额外 Shell 安装需求下的兼容策略，不代表所有第三方脚本都兼容 5.1。

**Q：双击 dev.bat 没反应 / 后端起不来？**

- 看那个 cmd 窗口里的报错
- 查看窗口打印的 `dev-logs` 目录，先看 `backend.stderr.log` 和 `frontend.stderr.log`
- 确认已运行 `npm ci`，Go / Node.js 可用，17655 / 17656 未被占用

**Q：端口被占用怎么办？**

- 已有开发实例运行时，直接打开上面的界面地址，不要重复启动
- 确认占用程序身份和影响后，再决定是否关闭它；不要批量结束所有 `node.exe`
- Windows 保留端口不能靠关闭应用释放；不要只改 `dev.bat` 的端口，前后端及桌面配置需要一致

**Q：数据存在哪？**

- 开发版：`%APPDATA%\launcher-sidecar-dev`；正式版：`%APPDATA%\launcher-sidecar`
- 自定义开发目录可运行 `powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev.ps1 -DataDir "D:\RunDock-dev-data"`（从 `code` 目录执行）
- 数据目录包含数据库及任务产物；备份时保留整个目录，不要把删除数据库当作常规排错手段

**Q：Go 装在哪？**

- 优先使用 `PATH` 中的 Go；找不到时，开发脚本尝试 `%USERPROFILE%\go\bin\go.exe`
- Node.js 需要可从 `PATH` 找到；启动失败时按窗口提示检查环境
