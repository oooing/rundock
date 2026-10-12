# 发布连接与代理恢复

## 已确认的问题

本项目 `.git/config` 的 GitHub 专用代理为 `127.0.0.1:7920`，覆盖用户全局代理 `127.0.0.1:7890`。旧端口已经没有服务监听，但发布仍使用旧设置。已移除此项目的旧代理覆盖，通过普通 `git ls-remote origin HEAD` 验证仓库可读。未修改全局设置。

## 修复范围

1. 开始上传型发布之前检查实际推送地址；连接失败时不改版本、不提交、不启动安装包构建。本机打包不要求远端连接。
2. 每次 Git 网络命令、GitHub 文件上传和核验重新读取可用的代理配置。Windows 手动系统代理、环境变量、Git 全局设置提供实际候选地址，不猜端口。
3. 仅失效且不带凭据的本机代理可以自动恢复。替代地址必须通过只读目的地检查后才能执行原命令。仅对子进程生效，不改用户代理配置。
4. 尊重显式关闭代理、外部代理、带凭据代理、SSH 配置和绕过列表。已连接代理的认证、证书或权限错误停止执行，不切换路线。
5. 写入命令不因网络结果不明而立即重发。继续沿用发布器的远端核查、产物摘要核验和按文件恢复，重启后保留记录和安装包。
6. 仓库读取、GitHub API、附件上传与云端状态查询共用恢复规则。GitHub CLI 仍使用系统凭证存储，环境中的令牌不会传入子进程。

## 验收边界

- 临时回环代理：关闭旧端口、更换新端口、全无可用代理、认证/证书错误停止、取消和超时。
- 真实临时 Git 仓库与 HTTP 代理：验证 URL 专用设置可以被一次性替换，远端只收到 GET，项目配置没有被修改。
- 真实发布执行器：无法连接时不生成安装包、不修改版本、不产生提交。
- 交付故障场景：上传中断后恢复、重启后恢复、重复提交、上传或发布响应丢失。GitHub 在这些故障测试中模拟，Git、SQLite、构建命令和签名包是真实的。
- 当前真实仓库和 GitHub API 只做读取验证，不推送代码，不创建新 Release。

## 复现命令

在 `code/sidecar` 使用本机 Go：

```powershell
$env:GOROOT='C:\Users\oooin\go'
$env:GOPATH='C:\Users\oooin\gopath'
& "$env:GOROOT\bin\go.exe" test ./internal/netproxy ./internal/publisher ./internal/delivery
$env:RUNDOCK_DELIVERY_E2E='1'
& "$env:GOROOT\bin\go.exe" test ./internal/publisher -run 'TestReleaseDeliveryE2E/(network-resume|restart-head-advanced|duplicate-submit|lost-upload-response|lost-publish-response)$' -v -count=1
```

可选的真实连接验收：将 `RUNDOCK_PROXY_LIVE_REPO` 设置为 RunDock 的 `code` 绝对目录，再运行 `go test ./internal/publisher ./internal/delivery -run TestLive -v -count=1`。只执行仓库和 API 读取。

## 使用与限制

`7890` 和 `7920` 都是本机代理软件的端口，不是 RunDock 页面端口。代理软件改端口时，应更新它的系统代理设置；RunDock 每次连接重新读取这些设置。没有任何候选地址可用时，必须先恢复网络或开启代理，再从原发布记录继续上传。

界面上已有的失败记录保留当时错误，不因修复配置而自动变成发布成功。代理恢复不等于 GitHub 发布完成。

不保证互联网、代理服务和 GitHub 永不故障；保证已识别的旧本机代理不被盲目重复使用，并在无法确认时停止写入。PAC 自动脚本和多推送地址不会被自动重写；保留其原有连接策略。TLS 验证不关闭。

## 本次验收结果

- 代理关闭、换端口、显式连接策略、取消、HTTP 权限拒绝、真实 Git 只读恢复以及发布前断网阻断测试通过。
- 发布模块完整回归分三组执行：171 项通过、7 项按条件跳过、0 项失败。首次整组运行超过默认 10 分钟，未将该次超时计为通过。
- 五项真实执行器故障验收通过：上传中断恢复、重启后恢复、重复提交、上传响应丢失、正式发布响应丢失。每项构建 1 次、正式发布 1 次；GitHub 服务模拟。
- 当前真实仓库和 GitHub API 的只读连接通过；未执行真实上传或创建 Release。
- 编译、静态检查及发布/交付/安全重启接口回归通过。开发版 `17656` 返回 HTTP 200，运行后台二进制 SHA-256 与验收构建相同。

报告位于 `outputs/acceptance/release-network/`：`delivery-e2e.json`、三个 `publisher-shard-*.jsonl`、`runtime-before.json` 和 `runtime-after.json`。

## 开发数据恢复与启动保护

原开发启动器收到后台自重启请求后退出。第一次从 Codex 子进程启动开发脚本时，同一个 AppData 路径被 Windows 文件隔离映射到 `OpenAI.Codex_2p2nqsd0c76g0/LocalCache/Roaming`，读到了较早的开发数据。原数据库没有丢失或被覆盖。

用户明确授权桌面只读核对和恢复后，通过已运行的 Explorer 窗口启动探针，文件句柄路径证实桌面进程读取的是原始 `AppData/Roaming/launcher-sidecar-dev/launcher.db`。已做 SQLite 一致性备份，完整性检查为 `ok`，没有覆盖两份数据库或补造发布记录。

已正常退出错误视图的开发实例（托管运行项目数为 0），从桌面 Explorer 启动当前 `scripts/dev.ps1 -NoBrowser`。恢复后的开发版 `17656` 返回 HTTP 200，原 5 个项目和 RunDock 的 5 条发布记录与重启前快照相符。今天失败的 `20261012T052924-853e44c0169f`、代码提交、4 条产物登记及原封存清单均保留；封存的 EXE、MSI 和校验文件大小及 SHA-256 全部核验通过。没有执行真实重试上传。

`scripts/dev.ps1` 新增保护：使用默认开发 AppData 时，先检查进程包身份及数据库句柄的实际路径。子进程即使没有包身份，也可能继承文件隔离，因此同时检测 `Packages/.../LocalCache` 重定向。发现隔离时在启动后台前停止，提示从 Explorer 启动 `scripts/dev.bat`；测试需要时可显式指定独立的非 AppData `-DataDir`。当前现场核验：Codex 子进程识别为隔离，桌面原数据识别为正常。

数据与产物核对报告：`original-data-view.json`、`original-data-backup.json`、`desktop-process-view.json`、`packaged-data-guard.json`、`original-artifacts-check.json` 和 `runtime-after.json`。备份保存在已忽略的本地验收目录，不上传。
