# 完整更新：延后安装与项目恢复

## 用户流程

- 下载和校验完成后，主操作为“下次启动安装”，次操作为“立即安装”。选择延后会持久化安排，不停止项目；可取消安排。
- 立即安装先读取当前运行清单，明确提示将停止的项目名称与保存工作提醒；点击“停止项目并安装”才开始。
- 确认后若项目清单、启动配置或进程批次发生变化，拒绝操作并要求重新确认。
- 停止项目与旧后台，安装并校验磁盘版本，新窗口等待旧窗口退出后启动；新后台版本也匹配才显示更新完成。
- 只恢复停止清单中的项目，保留临时端口。原本停止的项目不启动。项目配置或启动文件改变时不盲目执行，列出原因和重试入口。
- 完整退出时重新保存当时的运行清单；下次完整启动先安装、再启动后台、恢复清单。关闭到托盘或退出窗口但保留后台，都不触发延后安装。

这里的“恢复”是按原配置重新启动项目，不恢复项目内未保存的编辑内容，也不是无中断更新。界面在确认前明确提醒保存工作。

## 可靠性约束

- 更新记录 `desktop-update.json` 写临时文件、同步落盘，再原子替换；每个成功恢复的项目都从剩余清单移除。
- 后台以启停锁和发布互斥锁保护更新，不在构建、发布或启停过程中切换。
- 更新只发正常停止信号；项目不退出就取消，不升级为强杀。已正常停止的其他项目尝试恢复，并保留失败原因。
- 安装失败、安装中断、系统要求重启、版本不匹配均保留明确结果；不把失败自动重试成开机安装循环。
- 旧后台缺少完整更新接口时拒绝静默停止，要求先完整退出并启动新后台。
- 恢复接口仅本机原生调用；浏览器 Origin/Sec-Fetch-Site 请求拒绝。恢复前核对项目配置和启动脚本指纹。
- Windows 进程识别同时核对父子关系与创建时间，避免 PID 复用把其他程序误认成项目子进程；等待退出时继续核对原进程身份。停止检查不再为每次进程树查询启动 PowerShell。

## 验收范围与复现

当前代码已通过 Vue/TypeScript 构建、9 项更新状态单元测试、14 项 Rust 原生测试（另 1 项 GitHub 下载测试跳过）、Go 更新/停止回归测试与 27 项发布脚本断言。真实进程验收使用独立数据目录、随机端口、两个运行中的 HTTP 项目以及一个停止项目，不读写用户生产数据库。

- `scripts/acceptance/update-transaction.mjs`：真实 Go 后台 + 生产 Rust 事务模块的 CLI 宿主 + 真实 NSIS 安装钩子。校验停止清单、过期确认、延后安排、正常停止、文件覆盖、新后台版本、项目 PID/runId 更新、健康响应、停止项目保持停止、幂等恢复、延后退出快照、部分恢复失败及重试、安装中断、拒绝退出。
- `scripts/acceptance/update-choice-ui.mjs`：渲染真实 Vue 弹窗与状态层，仅替换原生 IPC；验证主次按钮、准确项目清单、显式确认、安装期间不能关闭、部分恢复提示与重试。
- `scripts/tests/app-update.test.mjs`：更新状态流转。
- `cargo test --manifest-path src-tauri/Cargo.toml --lib --offline --locked`：原生安装参数、校验、持久化、退出握手；GitHub 下载测试默认跳过。
- `go test ./internal/api ./internal/launcher -run 'TestDesktopUpdate|TestDesktopShutdown|TestStop|TestReusedParentPID' -count=1`（在 sidecar 目录）：互斥、过期确认、来源限制、停止与 PID 复用回归。

验收记录：`outputs/acceptance/update-transaction-real.json`、`update-transaction-real.log`、`update-choice-ui.log`、`update-choice-confirm.png`、`update-choice-failure.png`、`update-transaction-store.log`、`update-transaction-rust.log`、`update-transaction-go.log`。

本轮隔离测试目录及专属增量编译缓存已清理，记录中的测试路径仅用于追溯。验收脚本以后也会在结束时清理本次测试目录，保留小型日志和结果。复现前需先构建 Rust example，以及 `.tmp/update-backend-old.exe` / `update-backend-new.exe`（分别通过 `-ldflags '-X github.com/launcher-sidecar/internal/api.BuildVersion=2.0.41'` 和 `2.0.42` 注入后台版本）；界面脚本支持 `RUNDOCK_PLAYWRIGHT_MODULE` 指向已有 Playwright。

本轮尚未把新流程覆盖安装到用户当前生产目录；不能把隔离安装验收称为当前生产版已生效。NSIS 实测覆盖文件交付，真实桌面内的完整新流程以及 MSI 实装仍需随下一次本地生产包验收；MSI 参数校验已覆盖。没有创建 Git 标签、推送或发布到 GitHub。
