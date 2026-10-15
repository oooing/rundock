# 发布状态第一轮收敛

本轮只改 RunDock 的任务记录、状态展示和按钮恢复流程。验收停在发布前检查，不创建真实提交、Tag、Release 或部署。

## 状态与动作

| 情况 | 页面反馈 | 下一步 |
| --- | --- | --- |
| 打开窗口 | 读取项目信息，尚未开始检查 | 等待初始化 |
| 点击发布 | 保存准备记录，然后检查 | 按钮锁定，可取消检查 |
| 检查需处理 | 尚未提交，展示具体文件或检查项 | 处理问题，再确认发布 |
| 检查中取消 | 已取消，尚未提交 | 可以重新检查 |
| 提交响应丢失 | 结果待确认，不解锁重复发布 | 用候选 ID 查找同一任务 |
| 本机提交完成、云端未完成 | 等待云端结果或云端执行中 | 持续读取原任务 |
| 部分平台失败 | 分别显示成功、失败、等待及失败步骤 | 打开失败任务处理后重试 |
| 连接断开 | 结果待确认，保留已确认事实 | 自动恢复读取，不创建新任务 |
| 老版本由新版本替代 | 明确显示已替代 | 不把老版本标为成功 |
| 重新打开窗口 | 恢复未发布的准备记录 | 恢复选择，重新核对当前文件 |

`release_preparations` 保存选择、版本、更新说明和候选 ID。采用递增修订号防止迟到响应覆盖新状态；不保存源码、签名密钥或检查日志摘录。已创建的发布任务是最终依据，准备记录不构成自动发布授权。后台拒绝提交会独立记录拒绝结果，即使客户端没有收到响应也能恢复。

云端监控按提交 SHA、版本、工作流路径及本次任务标记归属到具体平台。单个平台失败不再把其他平台全部标为等待。旧数据没有平台快照时，只有整批确认成功才可推断全部成功。服务器同步仍独立展示，发布成功不代表服务器同步成功。

## 验收证据

- `scripts/tests/release-preparation.test.mjs`：有序写入、迟到响应、存储失败、提交响应丢失、明确拒绝与恢复。
- `sidecar/internal/store/release_preparations_test.go`：重启持久化、修订冲突、项目隔离。
- `sidecar/internal/api/handlers_release_preparation_test.go`：来源限制、无效请求、已失效候选恢复。
- `sidecar/internal/publisher/cloud_target_status_test.go`：PC 成功、Android 失败、Web 运行及缺失任务并存，重跑只更新对应目标。
- `scripts/acceptance/release-round1.ps1`：实际 RunDock、ingLocalPlay 配置和文件，隔离数据库；脚本禁止正式发布接口。仅沿用用户已经允许且已人工核对的测试密码例外。

2026-10-15 实测：RunDock 发布前检查通过；ingLocalPlay PC、Web、Android 发布前检查通过，无阻断性缺少文件。测试密码确认用时约 3.3 秒，保留原扫描结果，再运行此前尚未执行的平台检查。两个项目的 HEAD、文件指纹、真实发布历史在验收期间保持一致；隔离后台重启后恢复准备记录，并要求重新检查。

报告：`outputs/acceptance/release-round1-1d6c1316bee94898a6fc68e4a6335dfb/report.json`。

开发版 17656 已通过自身的安全自重启接口加载修改，没有启动安装版。重启前后真实发布历史一致，新准备记录接口和历史平台名称/版本组读取正常，证据见 `outputs/acceptance/release-round1-live-reload.json`。

验证范围：TypeScript 类型检查、47 项前端逻辑回归、后台准备记录/云端监控/冻结平台身份专项通过；store、api 完整套件通过。publisher 完整套件触及 10 分钟总上限，不能宣称完整套件全绿。其中报告等待超时的 `TestReleaseCanStayLocalWithoutPushingBranchOrTag` 与结束时正在执行的 `TestLocalReleaseWorksWithoutUsableRemote` 单独复验全部通过（约 62 秒），见 `outputs/acceptance/release-round1-go-local-recheck.log`。

本轮没有执行真实安装包构建、上传、发布或部署。因此不能据此宣称这几个外部步骤已端到端验收。浏览器自动操作受当前安全策略限制；未把页面视觉验收算作完成。

## 后续完整回归与超时调查

原失败有两个独立的时间预算：Go 默认每个测试包 10 分钟，以及 `waitRelease` 固定等待 15 秒。当前平台通过 `go test -list` 发现 publisher 有 212 个顶层用例，其中集成用例创建真实 Git 仓库、运行命令、复制候选内容、验签并写 SQLite，不能按纯内存单元测试估算时间。旧日志到总上限时仍在运行新的用例，不是同一个用例卡了十分钟。

对原来的两个超时相关用例启用 Git Trace2：5 个实际场景共启动 924 次顶层 Git 命令，Git 内部执行合计约 73.06 秒，单条最长约 1.21 秒；全部用例完成约 205.13 秒。测量与其他回归并行，耗时不是独占机器的性能基准。证据：`outputs/acceptance/release-round1-timing-summary.json`、`release-round1-timed-local.jsonl`。这次没有发现单条 Git 命令长期阻塞。

修正仅作用于测试：集成等待有界延长至 60 秒，输出每次阶段变化和超时最后状态；测试 Git 命令增加 30 秒截止时间和管道退出上限；无变动的夹具配置不再触发第二次完整扫描。产品命令超时、签名、版本、文件和远端核对规则没有放宽。新增 `scripts/acceptance/backend-regression.ps1`，完整模式自动发现当前平台的测试，publisher 每批最多 30 个顶层用例、两个故障注入套件独立分批，串行执行、每批 30 分钟上限；保留逐项 JSON 计时、失败/跳过清单和非零失败退出码，不用无限延长整包时间掩盖问题。批次发现、完整执行和失败传播另用 63 项模拟清单验证，模拟结果不计入真实后台测试数。

补测发现并修复实际界面问题：历史或准备记录读取失败时不能当作空列表。现在保持表单未就绪和提交锁定，显示原因及“重新加载”，防止结果未知时重新发起发布。前端旧测试仍读取重构前的单体组件，已迁移到当前控制器，保留行为断言，并对批量处理、代码保存、选择恢复等按现有产品约定验证。

2026-10-15 第二次真实项目验收：RunDock 的 126 个所选文件及 Windows 检查通过；ingLocalPlay 的 39 个所选文件及 PC、Web、Android 检查通过，无阻断性缺少文件。测试密码复核约 4.25 秒，之后只跑未完成的项目检查。两项目源码指纹、真实发布历史不变；隔离后台重启恢复通过；临时验收进程退出、候选副本取消回收，约 19 MB 验收后台程序已删除。报告：`outputs/acceptance/release-round1-5b7d92b4285e498795a4999263b8995d/report.json`。

真实项目配置与工作流只读核对也通过：RunDock 保留 1 个本地、1 个云端目标；ingLocalPlay 保留各 4 个目标。报告：`outputs/acceptance/backend-1e56d1593b784c7ea7e1a06f2168b60c/projects-e2e.json`。

真实 GitHub 只读检查：两仓库各自的账户/仓库权限、当前发布工作流与远端版本一致性、API 和 Git 连接均通过。调用层拒绝写请求；没有触发 Actions、Tag 或 Release。日志：`outputs/acceptance/release-round1-github-readonly-rundock.log`、`release-round1-github-readonly-localplay.log`。

对 ingLocalPlay 原来误报的真实备份再次验证：4 个实际源码文件通过，4 个备份文件默认只留本机；主动扫描备份发现的 50 个不完整引用仅为参考提示，不阻断发布。证据：`outputs/acceptance/release-round1-real-backup-scope.jsonl`。这不代表任意真实源码缺失都可忽略，项目构建/检查命令失败仍会阻断。

## 最终验收对照（2026-10-15）

汇总：`outputs/acceptance/release-round1-final-summary.json`。原始全量命令的失败退出码仍为 1，publisher 运行 30 分钟后超时；不能把这次命令描述为“一次全绿”。其余 18 个有测试的后台包通过。对照当前平台发现的 212 个 publisher 顶层用例，合并原始执行、故障场景复验、剩余用例补跑和只读 Git 连接检查后，207 个通过、5 个按条件跳过，没有未执行且未说明的用例，也没有尚未解决的失败。5 个跳过项是实际完整安装包构建、私有旧案例、专项性能探针，以及 2 个需要当前 Windows 不具备的符号链接权限的用例。其他后台包仍有 2 个同类符号链接用例、1 个需外部 WXT 页面地址的探针未执行；它们不计为通过。

首轮 4 个失败的顶层套件（含子用例）均已通过复验；本地交付 20 个场景和云端提交 4 个场景全部通过，包括断网恢复不重复构建、丢失响应后恢复、重复提交、缺签名、文件冲突、取消、超时和重启。复验记录分别在 `backend-4993273dae1a44eeb06d4e3bcdd89744`（约 750.7 秒）和 `backend-803869388ab54a6cbbafd408a4e3efa3`（约 575.3 秒）。上述目录均位于 `outputs/acceptance/`。服务器同步/部署状态另有 `deployment-api-e2e.json`、`server-pull-api-e2e.json`：构建完成仍等待服务器版本核对、失败可见、响应丢失与重启恢复通过。

前端 165 项发布逻辑测试通过；新增的历史/准备记录读取失败锁定、重新加载恢复行为专项通过；TypeScript 检查及 `git diff --check` 通过。执行器的分批发现和失败传播经过模拟清单验证，实际 store 套件也经新执行器通过；没有再重复运行一轮几十分钟的全量套件。

本轮未进行两个项目的真实完整安装包构建、GitHub 写入、正式发布或生产部署，因此外部交付不算真实端到端验收通过。浏览器自动操作曾被安全策略明确拒绝，本轮没有绕过，也未把按钮点击和视觉检查计为通过。已完成的是源码逻辑回归、真实项目发布前检查和只读远端核对，以及模拟外部服务下的交付故障验收。

后续可复现命令（在 `code` 目录运行）：

```powershell
./scripts/acceptance/backend-regression.ps1 -Delivery -LocalPlayRoot 'D:/001oooing/WorkPitt/001AIcoding/023ingLocalPlay'
```
