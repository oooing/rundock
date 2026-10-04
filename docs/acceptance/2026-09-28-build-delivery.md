# 构建与交付验收 · 2026-09-28

历史验收摘要：本地构建、持久保存、交付恢复及云端调用完成隔离验证；不表示真实 GitHub 发布或生产部署已通过。

| 验收 | 结果 | 边界 |
| --- | --- | --- |
| 本地构建到 Release 交付 | 20 场景通过 | 真实 Git、SQLite、命令进程；GitHub 为故障模拟服务 |
| 显式云端调用 | 4 场景通过 | 正常调用、响应丢失、重启恢复、明确拒绝后重试 |
| HTTP 与同步状态 | 4 项通过 | 隔离 API、HTTPS 版本端点和数据库 |
| 发布界面 | 5 项通过 | 实际 Vue、独立浏览器；HTTP 响应模拟 |
| 前后端构建 | 通过 | 后端回归分段执行，不宣称一次完整全套运行通过 |

原始报告和日志只在本地 `outputs/release-delivery/` 保存。专项外部项目接入材料已归到仓库外，不是 RunDock 的依赖。
后续 2026-10-03 的旧交付整组回归曾超时，只单独复验一个多目标完整场景；不能把历史 20 场景当作当前全部重验结果。

## 重复验证

在 `code` 根目录使用已有 Node、Go 和 Playwright；无需外部项目或签名材料：

```powershell
npm.cmd run build
$env:RUNDOCK_PLAYWRIGHT_MODULE = '<已有 Playwright 模块路径>'
node scripts/acceptance/release-delivery-ui.mjs

New-Item -ItemType Directory -Force outputs/release-delivery | Out-Null
$env:RUNDOCK_DELIVERY_EVIDENCE = (Resolve-Path outputs/release-delivery).Path
$env:RUNDOCK_DELIVERY_E2E = '1'
Push-Location sidecar
try {
    go test ./internal/publisher ./internal/api -run '^(TestReleaseDeliveryE2E|TestCloudDispatchE2E|TestDeliveryAPIE2E)$' -count=1 -timeout=15m -v
} finally { Pop-Location }
```

命令执行隔离端到端场景，不访问真实 GitHub 发布账号。完整场景只在开发结束时运行。
配置、产物和恢复方法见[通用接入说明](../onboarding/build-delivery.md)。
