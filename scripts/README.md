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
| Go 后端 `.go` | 确认允许关闭该实例的子项目后，退出开发窗口 → 重新双击 `dev.bat` |

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
| **启动命令** | 双击 `dev.bat` | `scripts\build-sidecar.bat` 然后 `npm run tauri -- dev` |
| **界面** | 浏览器 http://127.0.0.1:17656/ | 原生桌面窗口 |
| **改 Go 代码** | 重启 `dev.bat`（自动重编译） | 重跑 `build-sidecar.bat` + 重启 `tauri dev` |
| **改前端代码** | 浏览器刷新即可 | 自动刷新 |
| **适合场景** | **日常开发调试（推荐）** | 最终联调 / 演示 / 打包 |

---

## 常见问题

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
