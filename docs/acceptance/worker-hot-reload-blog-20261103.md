# 博客热重载与正式后端报警验收

2026-11-03；Windows 本地开发构建，Chrome 扩展验证。未打包或更新已安装版本。

## 修复

- 复用已有进程快照识别 Miniflare 启动的 workerd：二进制协议、loopback 桥接、控制管道，以及 `--socket-addr=entry=…:0` 随机入口同时满足才归为辅助。固定端口的 workerd 代理、普通未知后端、独立 workerd 服务保持必要服务；显式就绪声明和端口证据优先。
- 辅助服务分类在健康检查中保留；已关闭的辅助端口从当前服务列表移除，发现与退出日志仍保留。必要服务端口关闭后保留记录并继续报警。
- `/api/health` 等健康路径返回 5xx 时，后续首页 200 不再覆盖该错误。原有失败复查与恢复机制保留。

## 环境

- RunDock Web：`http://127.0.0.1:18752/`，后端：`http://127.0.0.1:18751/`。
- 后端：`sidecar/.tmp/rundock-hot-reload-v2.exe`。
- 独立数据目录：`outputs/acceptance/hot-reload-blog-20261103/data/`。
- 项目卡片：`oooing 博客（热重载验收）`，ID `ecaf86e819aad8cb`。
- 真实博客源码来自 `D:\001oooing\WorkPitt\001AIcoding\053oooing-blog`，复制到验收目录的 `blog/`；使用原启动 CMD、Vite、Wrangler 和本地迁移。依赖目录关联原已安装依赖，Vite 缓存独立。源码改动和故障注入仅在副本内。
- 原博客另有外部 QA 实例在运行，未停止或修改。此前由旧 RunDock 验收托管的博客进程已通过停止接口退出。

## 真实验证

最终运行 ID：`20261103T103006-eff156479bf7`。

| 场景 | 观察 | 结果 |
| --- | --- | --- |
| 启动 | 17653 前端、8788 固定后端均为 required；随机 Worker 端口为 auxiliary | 通过 |
| 热重载 | 内部端口 3605 → 8681；3605 移出当前列表；连续采样均为 running | 通过 |
| 后端故障 | 仅在副本让 `/api/health` 返回 503，首页仍可返回 200 | 卡片 degraded，明确提示 8788 /api/health 503；辅助端口错误未进入项目故障清单 |
| 恢复 | 还原副本 Worker 文件，真实 Wrangler 热重载 | 8788 healthy，项目自动恢复 running |

首次故障验证暴露了首页 200 覆盖健康检查 503 的旧问题；最终构建修复后重新完成上表所有场景。故障已撤销，最终留下健康运行的验收实例。

## 自动回归与证据

- Go：进程归属/Miniflare 分类、服务发现、辅助端口热重载退出、必要后端关闭报警、健康检查重试和恢复、WXT/Vite、存储、相关 API 测试通过。
- `internal/recovery`、`internal/store`、`internal/probe` 完整测试通过；launcher 与 API 使用本次相关测试集。
- 卡片服务测试 7/7 通过；Go 构建与 `git diff --check` 通过。
- 实测脚本：`scripts/acceptance/worker-hot-reload-blog.py`，支持 start、baseline、reload、fail、recover。
- 机器记录：`outputs/acceptance/hot-reload-blog-20261103/report.json`。
- Chrome 截图：同目录 `01-hot-reload.png`、`02-backend-failure.png`、`03-recovered.png`。其中 01 记录首轮热重载，02/03 为最终修复构建。

自动识别当前针对有明确进程参数证据的 Miniflare 内部 Worker；没有证据的服务仍按必要服务处理，避免静默忽略普通后端故障。
