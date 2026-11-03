# RunDock 2.1.8 后台启动验收

2026-11-03，本机安装版。

## 问题与修改

用户关闭博客弹出的控制台后，安装版报告 8788 connection_refused，前端 17653 仍在运行。这次“部分异常”符合实际后端停止状态。

主启动脚本原本通过隐藏 ConPTY 运行。博客 `start-preview.mjs` 用 `detached: true, windowsHide: true` 启动 Wrangler；Wrangler 再启动 workerd 时没有传递隐藏参数，脱离控制台的后代仍可能产生可见终端。

新增每次运行专用的 Node preload，通过继承的 NODE_OPTIONS 补充到启动链。仅对识别为 Windows 控制台子系统的可执行文件补 windowsHide；保留 detached、unref、stdio、参数和环境。通过 PE 子系统区分 GUI 程序，避免隐藏浏览器、编辑器或 Electron 窗口。保留用户原有 NODE_OPTIONS，不修改系统环境和项目脚本；预加载文件随整个运行结束清理。

常规 CMD/PowerShell/Python 入口继续使用已有隐藏 ConPTY；本次额外覆盖 Node 多层后台启动。脚本清空 NODE_OPTIONS、显式启动独立终端或进程自行创建控制台的情况，不保证无窗口，需要按项目适配。没有使用桌面全局窗口扫描或强制关闭控制台。

## 构建与安装

- 版本从 2.1.7 更新为 2.1.8，生成 NSIS/MSI。
- 新包重新构建 Go sidecar、执行健康冒烟、前端类型检查与构建、Rust 构建，均完成。
- 使用 NSIS 自带覆盖安装，退出码 0。安装位置 `C:\Users\oooin\AppData\Local\RunDock`。
- 磁盘主程序、实际桌面界面和 `http://127.0.0.1:17654/api/health` 均为 2.1.8；后台实例 `2026-11-03T12:25:45.4115747Z`。
- 含强制结束进程的组合更新命令被自动安全检查拒绝，没有执行。后续使用安装器覆盖更新和 UI“停止并退出”正常退出旧版，再启动新版。临时恢复关闭询问后，已还原用户 `closeBehavior=minimize`。
- 当前数据库和 2.1.7 两个可执行文件已备份到验收输出目录；原 13 张卡片的 ID、名称、脚本、命令、参数、环境、端口提示和访问入口逐一比对保持一致。

NSIS SHA-256：`71d5985187b9559abd95de5e360b4ddd9e5b2f4554cd032c9b47fdcc2323585b`。

## 测试

- `go test ./internal/proc -v -count=1 -timeout 90s`：通过。真实 Windows 夹具模拟入口 Node → detached 后台 Node → 未声明隐藏的控制台孙进程，验证不显示控制台、入口退出后存活、用户 preload 保留、Stop 回收。
- `node --test sidecar/internal/proc/node_background.test.cjs`：3/3 通过，覆盖控制台隐藏、后台参数不变、中文及空格 PATH、GUI 和未知文件不改动。
- launcher 后台启动、重启、停止相关回归通过，覆盖 `start /b` 和 PowerShell 后台启动，独立服务未受影响。
- 本次未重新运行全部 Go 测试；前次完整测试中 publisher 长时间等待的情况未在此次处理，不宣称全套通过。

## 安装版博客实测

沿用真实博客源码验收副本 `outputs/acceptance/hot-reload-blog-20261103/blog`，没有修改原博客源码或原有独立 QA 实例。

项目 ID `b2e881acd2bfb614`。恢复启动运行 ID `20261103T122555-327c698a627d`；原生面板点击“重启”后的运行 ID `20261103T122704-03c012582aff`。

| 验证 | 结果 |
| --- | --- |
| 新版恢复启动 | 17653 前端、8788 后端健康，项目 running |
| 热重载 | 临时端口 7713 → 13451，旧端口移除，采样保持 running |
| 面板重启 | 旧 workerd 进程退出，新运行健康；没有重复项目 |
| 窗口观察 | 启动、热重载和重启后窗口清单均无新 CMD、workerd 或系统终端窗口；保留正常 Chrome 页面 |
| 访问入口 | 首页 `/`、后台 `/admin`、8788 `/api/health` 均 HTTP 200 |
| 原外部实例 | 17765 `/api/health` HTTP 200，未停止 |
| 最终状态 | 2.1.8 桌面留在健康博客卡片，关闭主窗口仍按原设置收起托盘 |

窗口记录是阶段性观察；真实进程回归另行验证控制台可见性，并不将阶段截图等同于逐帧录像。

证据目录：`outputs/acceptance/background-blog-20261103/`。包含 `package.log`、`proc-tests.log`、`node-tests.log`、`launcher-tests.log`、`installation.json`、`blog-report.json`、`restart-and-preservation.json`、`windows-*.json`、`installed-background.png`、安装包及备份。无远程发布。
