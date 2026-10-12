# Windows 安装保留运行中的项目

## 已确认的问题

原 `install_app_update` 先请求 `/api/desktop/shutdown`，后台执行 `StopAll` 后退出；NSIS 安装前也运行关闭脚本，终止安装目录内的主程序及后台。后台持有项目的 Windows Job 句柄，后台退出会连带结束项目。因此原行为确实会中断项目。

## 修改后的行为

- 应用内更新不请求停止后台、不退出 RunDock，不自动重启软件。等待安装器返回，分别显示安装成功、取消或失败；Windows 返回 3010 时说明需要重启电脑，仍不自动重启。
- EXE、MSI 安装使用同一个 Go 辅助程序：把安装目录中的两个旧 EXE 移至唯一备用名称，再写入新文件。原进程、Job、端口、日志会话及项目继续运行。新代码在用户停止项目、退出后台后，下次启动生效；保留项目的退出方式不会切换旧后台。
- 安装辅助程序只处理两个固定程序文件。恢复记录限制名称及目录，每次安装有独立归属，第二个安装器不能回滚第一个安装器的文件。失败时恢复文件；没有释放的旧副本保留，后续安装清理已释放的副本。
- NSIS 移除安装阶段的进程检查/终止宏；保留明确卸载行为和旧 Launcher / MSI 迁移确认。正常同格式升级、同版本重装走原有原位安装路径，不改变降级或跨安装格式迁移的确认政策。
- MSI 在文件占用检测和旧产品移除前准备文件，并禁用 Restart Manager 自动关闭应用。链接使用现有 Tauri WiX fragment 配置，升级标识不变。依据：[Microsoft Restart Manager 配置](https://learn.microsoft.com/en-us/windows/win32/msi/msirestartmanagercontrol)、[WiX 自定义动作](https://docs.firegiant.com/wix3/xsd/wix/customaction/)。
- `release-build.ps1` 和 `build-sidecar.bat` 编译辅助程序；发行构建结束恢复辅助输入，辅助 EXE 不提交为源码。

## 验收与边界

- EXE：真实 NSIS 临时安装包、真实 Go 主进程/后台、真实 Windows Job 托管 HTTP 子进程；安装、重复安装后 PID 和访问正常。其他安装目录及配置不变。卸载和文件锁失败行为通过。
- MSI：唯一升级标识、仅当前用户的临时测试产品，真实 1.0.0 → 2.0.0 升级；原主程序、后台及 Job 子进程保持运行，新文件 SHA-256 与 2.0.0 输入一致。注入 3.0.0 安装失败后文件恢复至 2.0.0，项目继续运行。
- 辅助程序：锁定第二个文件后的回滚、安装互斥与恢复归属、提交后清理、拒绝越界恢复记录通过。
- Tauri 当前打包器实际编译 EXE/MSI 模板通过。更新器 Rust 测试、前端更新状态/文案测试、Vue 类型检查通过。未调用 GitHub 下载测试。
- 没有安装或替换用户当前正式 RunDock，也没有停止用户项目；开发版仍使用 17656。以上验收不等同于正式 Windows Program Files 权限、UAC 与企业安装环境全覆盖。
- 已安装旧版的更新器逻辑不会自动改变。需要重新生成并使用包含此修改的新版安装包；旧版应用内更新仍可能先停止项目，应直接运行新版安装包完成首次切换。

测试入口：`scripts/tests/installer-shutdown.test.ps1`、`installer-live-msi.test.ps1`、`installer-bundle.test.mjs`、`app-update.test.mjs`；辅助程序在 `sidecar/cmd/rundock-install-helper/`。
