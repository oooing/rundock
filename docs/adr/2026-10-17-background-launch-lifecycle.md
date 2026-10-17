# Windows 后台启动的通用托管

## 问题与原因

有些启动脚本通过 `cmd start /b` 或 PowerShell `Start-Process` 启动服务后正常退出。
原逻辑只等待入口进程；入口退出立即关闭 Windows Job，连带终止仍健康的后台服务。
另外，先执行入口再绑定 Job 有竞争窗口，极快的脚本可能在托管完成前创建子进程。

## 改动

- Windows 项目入口先以暂停状态创建，成功加入专属 Job 后才开始执行。
  无法建立托管时取消启动，避免放出无法管理的后台进程。
- 入口正常退出后，等待 Job 中全部后台进程结束；监测与日志继续工作。
- 从原生 Job 查询成员及进程创建时间，发现端口与运行状态均使用这份归属。
  不因日志中出现其他项目 URL 就接管该端口。
- 停止、重启按本次启动的 Job 清理，不依赖已退出、可能被复用的入口 PID。
- 入口报错保留失败退出码并清理本次启动的后台进程。
- 保留 `KILL_ON_JOB_CLOSE`：启动器异常退出时由 Windows 回收该 Job，避免孤儿进程。
- 本地 ConPTY v0.1.4 补丁仅增加暂停创建和恢复主线程，来源与许可证保存在
  `sidecar/third_party/conpty/`。未更改机器上的 Go 模块缓存。

普通后台子进程不需要项目专用脚本改造。通过系统服务、任务计划、提权代理或容器守护程序
启动的工作负载不等同于本次 Job 的子进程，仍需对应的管理接口，不能仅凭端口猜测接管。
本次改变 Windows 生产启动路径（ConPTY）；其他平台维持原来的行为。

## 实际验收（2026-10-17）

隔离数据库、随机端口和临时脚本验收通过：

1. `cmd start /b`：入口快速退出，后台稍后开放端口，仍正确发现服务并保持运行。
2. PowerShell `Start-Process`：入口退出后服务保留，停止回收全部本次后台进程。
3. 后台启动后重启：旧组退出，新运行与服务重新识别；随后停止成功。
4. 启动脚本输出无关服务 URL：无关端口不被登记，停止与重启不影响该服务。
5. 入口错误退出：保留退出码 7，后台进程能被回收。
6. 入口退出后取消：回收仍在运行的后台组。
7. 启动器进程直接异常退出、不调用 Close：后台进程被系统回收，无关监听继续运行。
8. 原始 LibreTV `Start-Local.ps1`：仅在测试副本替换项目/日志路径和隔离端口、禁用开浏览器，
   保持原后台启动逻辑。真实 v2.16.12 服务在 12120 就绪，入口退出后仍运行，停止清理成功。
   用户原来 8080 服务未改动、未重启。

对应证据（本机验收输出，不上传）：

- `outputs/acceptance/background-process-regression.txt`：proc、launcher、recovery、API 定向回归通过。
- `outputs/acceptance/background-restart.txt`：真实后台启动、重启与停止通过。
- `outputs/acceptance/background-libretv-live.txt`：原始 LibreTV 后台脚本实测通过。
- `outputs/acceptance/background-existing-services.json`：现有 3000、4318、8080、11784、11785、
  17654、17655、17656 的监听 PID 均与验收前一致；8080、11784、17656 页面 HTTP 200。

## 第一阶段的生效边界（后续安装结果见文末）

本轮修改源码并做隔离验收，没有重启在线开发后台或安装版，没有替换生产程序、发布 GitHub。
当前在线后台尚未加载此修复。现有 Job 不能安全转交新进程，需要通过已有安全停止/恢复流程
更新后台。LibreTV 的原有临时前台启动适配暂时保留，以兼容当前尚未更新的安装版。

## 系统依据

- [Windows Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)
- [Job 成员 PID 列表](https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_basic_process_id_list)

## 后续本地安装进度（2026-10-17）

用户随后授权更新开发版、安装版并停止恢复项目。本地 2.0.43 的 NSIS/MSI 已构建，
NSIS 覆盖安装退出码 0，安装后台 SHA-256 与新构建的 sidecar 一致，在线 health.version=2.0.43。
未创建 Git 标签、推送或发布 GitHub。

LibreTV 已还原原始 `Start-LibreTV.cmd`，与原备份 SHA-256 完全一致；原始 PowerShell
后台启动脚本未修改。入口已退出，新后台仍显示运行，8080 网页 HTTP 200。
详见 `outputs/acceptance/background-rollout/installed-libretv-original-first.json`。

### 最终完成结果（2026-10-17）

用户明确授权一次性 Windows 任务启动并立即删除。使用当前用户的交互会话启动安装版，
消除了继承打包应用身份导致的开发数据目录保护拦截；没有关闭或绕过该保护。
初次恢复及最终安装后的两次临时任务都已删除，并核对任务列表确认没有残留。
任务收据保存在 `one-time-task-first-receipt.json`、`one-time-task-receipt.json`。

实际验收另外发现并修复了两项问题：

1. 外层 RunDock Job 会包含内层开发版托管的项目。保留 Job 的生命周期保护，服务发现则
   使用共享进程快照、PID 创建时间和已知项目路径进一步收窄，避免把看剧鸭、LibreTV 的
   端口算到 RunDock 上。只读恢复识别区分直接托管根进程与继承的 Job 成员；直接托管关系
   仍优先于共享工具路径。新增项目/快照缺失时延后登记，不借用日志 URL 扩大归属。
2. ConPTY 上游原始管道句柄在输出读取与关闭并发时触发无效句柄异常。改用 `os.File`
   协调并发 I/O 和关闭。后台退出、取消、故障退出、启动器崩溃回收、并发停止测试通过，
   管道生命周期相关测试额外重复三轮通过。

最终源码已重新构建 NSIS、MSI，NSIS 安装退出码 0。安装后的 sidecar 与本次包内构建一致：
`7E974686199A6737BC78CD06B4B56415A71D3CEC2FD31F62156026C24CCCC5AA`。
安装后台 health.version 为 **2.0.43**；开发后台由原 `dev.bat` 从最终源码重新编译。
开发库原 5 个项目、安装库原 10 个项目 ID 全部一致，没有替换为新数据库。

- 开发版 `17656` 和看剧鸭 `11784` 已按原配置恢复，网页 HTTP 200。
- 安装版、开发版均实际执行未经修改的 LibreTV `Start-LibreTV.cmd`：入口退出后仍运行，
  8080 服务健康，开发版停止后端口释放，临时项目已删除。
- 安装版 LibreTV 再次执行真实重启：生成新运行记录，旧 Node 进程退出，新服务 HTTP 200，
  页面地址可用。此轮 LibreTV 验收期间看剧鸭运行记录、PID 均未变化。
- 按用户要求保留 VirtualPhotography **17618**，没有恢复旧 3000 实例。
  本轮开始至结束其 PID **28248**、创建时间均未变化，网页 HTTP 200。
- 安装版 RunDock 卡片最终仅登记 **17655、17656**，不含看剧鸭或 LibreTV 端口。
- 未创建 Git 标签、推送提交或发布 GitHub。

最终证据统一位于 `outputs/acceptance/background-rollout/`：

- `final-state.json`：项目清单、端口、网页、进程身份和任务清理结果。
- `install-final.json`、`build-final.log`：最终安装文件哈希和构建结果。
- `dev-libretv-final.json`、`dev-libretv-final-cleanup.json`：开发版实测与临时项清理。
- `installed-libretv-before-restart.json`、`installed-libretv-final.json`：安装版重启前后证据。
- `final-regression.txt`、`nested-ownership-tests.txt`、`recovery-full.txt`、
  `pipe-lifecycle-repeat.txt`、`live-ownership.txt`：定向回归及实际进程只读验收。

早期恢复及最终核对曾遇到进程快照查询超时，保护拒绝启动或显示“状态待确认”，未跳过核验。
新增日志明确记录了进程查询的 context deadline exceeded。核对发现一次性计划任务默认
Priority=7，使安装版、开发后台继承 BelowNormal。已仅将 RunDock 安装进程、开发后台及
开发启动器改回 Normal，任务脚本以后显式 Priority=4；没有修改项目进程优先级。
调整后安装版、开发版主动核验分别约 2.08、1.92 秒完成，并继续观察周期刷新。
这是本次桌面任务启动环境的修正，不承诺 Windows 查询在任何负载下都不超时。
相关证据为 `normal-priority.json`、`normal-dev-launcher-priority.json`、
`normal-priority-check.json`、`normal-priority-stability.json`。

原数据库与旧安装文件的离线回退备份仍保留于
`outputs/acceptance/background-rollout/rollback/`（约 4.48 GiB），不属于正在占用的构建临时目录。
