# RunDock 2.1.7 本机安装验收

2026-11-03。将中文脚本识别、重复入口提示、多访问入口及临时端口误报修复一起打包，更新本机安装版并验证。

## 打包与安装

- 原安装版本：2.1.6；本次版本：2.1.7。package.json、package-lock.json、Tauri 配置、Cargo.toml、Cargo.lock 版本一致。
- 使用当前工作区构建，未提交或发布远程版本。生成 Windows x64 NSIS 和 MSI。
- NSIS 安装器退出码 0；安装目录 `C:\Users\oooin\AppData\Local\RunDock`。
- 已启动安装目录的 `launcher-platform.exe`；文件版本、桌面界面、安装版后台 `http://127.0.0.1:17654/api/health` 均为 2.1.7。此次验收没有使用开发版后台 18751。
- NSIS SHA-256：`f7519e8d7fa71aa32b195a2d4789847180bc8db85b638c7e5e48a765dc67b0fd`。
- MSI SHA-256：`9b7a2b80ab35208fe16e79453dc246aaa547ddcd3be0d13b135193680cef811d`。

## 数据保留

安装前使用 SQLite 备份接口备份原数据库，并保留两个原安装可执行文件。备份位于 `outputs/acceptance/installed-blog-20261103/launcher-before.db` 和 `binaries-before/`。

安装后逐一核对原 12 个项目的 ID、名称和启动脚本，全部保留。新增一张“oooing 博客（安装版验收）”卡片，共 13 个项目。没有覆盖原项目。

## 安装版验证

真实博客源码来源：`D:\001oooing\WorkPitt\001AIcoding\053oooing-blog`。故障测试运行 `outputs/acceptance/hot-reload-blog-20261103/blog` 内的源码副本，使用真实 CMD、Vite、Wrangler 和独立本地数据。未改动原博客源码和原实例。

安装版项目 ID：`b2e881acd2bfb614`；运行 ID：`20261103T105146-f79af402fbc9`。

| 场景 | 结果 |
| --- | --- |
| 原博客目录识别 | 原生安装版添加窗口推荐“启动博客.cmd”；“启动博客Demo.cmd”标记与它内容相同 |
| 已添加项目重复入口 | 副本 Demo 脚本提示与已添加验收项目内容相同；未勾选独立添加时确认按钮禁用；取消后没有产生重复卡片 |
| 关联入口 | “管理博客.cmd”返回已添加博客项目的 relatedApps |
| 多入口 | 同一卡片显示“博客首页”和“管理后台”，分别指向 17653 的 `/` 与 `/admin`；两地址 HTTP 200 |
| 热重载 | 内部端口 8559 → 11003；旧端口从当前列表移除；采样状态始终 running |
| 正式后端故障 | 副本 `/api/health` 注入 503 后，必要后端 8788 为 unhealthy，项目转为 degraded；前端仍 healthy |
| 恢复 | 还原副本文件，与故障前备份字节一致；8788 健康检查 HTTP 200，项目自动恢复 running |

故障已撤销，安装版窗口留在健康运行的博客卡片页面。原其他项目的已有状态不在此次修复范围内。

## 构建检查与限制

本次相关 importer、accesslink、launcher、probe、recovery、store、API 回归及类型检查通过。完整 Go 测试执行到 `TestParentAcceptedCandidateMustBindCurrentRequest/source` 长时间等待，约 373 秒时终止 publisher 测试进程，因此完整测试记录为失败/未完成，不能声称全套通过。未对该发布模块测试作修改。

随后用 `scripts/release-build.ps1 -TagName v2.1.7 -SkipTests` 完成新 Go sidecar 构建、健康冒烟、前端类型检查和构建、Rust 构建以及 NSIS/MSI 打包。以上安装版真实博客验证均在新包安装后执行。

## 证据

均位于 `outputs/acceptance/installed-blog-20261103/`：

- `build.log`：首次完整测试，包括中断记录。
- `package.log`：最终完整打包日志。
- `packages/`：两个安装器、SHA256SUMS.txt、release-assets.json。
- `installation.json`：安装版本、退出码和安装器哈希。
- `apps-before.json`：安装前项目列表。
- `features-and-preservation.json`：安装版 API 返回、原项目保留检查、入口 HTTP 验证。
- `blog-report.json`：热重载、正式后端故障、恢复采样。
- `chinese-discovery.png`、`duplicate-warning.png`、`installed-healthy-card.png`：实际安装版原生窗口截图。

可复用脚本为 `scripts/acceptance/worker-hot-reload-blog.py`，本次通过 `RUNDOCK_ACCEPTANCE_BASE`、`RUNDOCK_ACCEPTANCE_REPORT`、`RUNDOCK_ACCEPTANCE_INSTALLED` 指定安装版验收。故障注入仅修改上述验收副本。
