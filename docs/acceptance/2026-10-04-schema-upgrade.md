# 旧库升级顺序 · 2026-10-04

## 结论与范围

v2.0.22 的云端失败来自旧库缺字段时提前创建索引，不是 NSIS/MSI 本身。
本次修复 store 初始化依赖，不改版本号、外部项目、运行中服务或用户数据库。
升级端到端验收已通过，隔离候选已生成 EXE/MSI；尚未提交、上传或重新发布。
完整构建命令的 Go 回归在本机累计达到 15 分钟上限，不能记为一次通过。
补跑剩余用例后，发布模块合计 153 项通过、6 项按原有条件跳过；其他 Go 包通过。

## 证据与原因

- E-001：GitHub 任务 [37173700708](https://github.com/oooing/rundock/actions/runs/37173700708/job/111351894460)，
  `TestUpgradeReleaseOptionsUsesSafeDefaults` 报 `009_local_build_artifacts.sql: no such column: execution_plan_json`。
- E-002：旧候选的真实进程读取隔离旧库也立即退出，复现报告位于本地
  `outputs/acceptance/schema-upgrade-1791086337074-3a285346/report.json`；不是单纯测试断言问题。
- F-001：`CREATE TABLE IF NOT EXISTS` 不补旧表字段，009 引用字段早于 ensureSchema。
  依据 E-001/E-002，修复为建表、补列、派生索引的顺序；唯一约束保留。
- P-001：启动 → store.Open → SQL 建表 → ensureSchema → ensureDerivedIndexes → HTTP 可用。
  依据 E-002 及修复后报告 E-003，旧记录保持原值，新功能可用。
- E-003：Node 24 与 Node 22 均通过 9 项真实进程/HTTP/SQLite 验收，包含全新库、旧库、
  重复启动、发布历史与偏好保留、本地构建、下载哈希、数据库唯一约束和重启幂等。
- E-004：原云端失败用例在本机回归中通过（0.07 秒）；完整 Go 命令仍因发布模块
  累计用时超过 900 秒退出。超时前该模块 150 项通过；剩余 3 项单独补跑全部通过
  （49.488 秒）。没有通过增加超时或跳过失败来修改发布门槛。
- E-005：前端类型检查及生产构建、锁定 Rust 编译、22 项发布脚本测试均通过；
  Tauri 实际生成 NSIS EXE 和 MSI，合成安装/卸载场景的 3 项钩子验收通过。
  安装包仅为本地候选，未安装到当前正式版，不能替代完整构建门槛或云端验收。

## 工件与未验证边界

精简结果与哈希见 [验收记录](evidence/2026-10-04-schema-upgrade.json)。
Node 22 完整报告位于 `outputs/acceptance/schema-upgrade-1791086582038-c2d9c556/report.json`，
Node 24 完整报告位于 `outputs/acceptance/schema-upgrade-1791086478094-702321ab/report.json`。
本地候选目录为 `.tmp/schema-ci-20261004-7f531c2a/`；
`outputs/build-retry.log` 保留完整命令的超时记录，`outputs/publisher-tail.log` 保留补跑记录。
6 项发布模块跳过项包括 opt-in 云端/项目验收和需要 symlink 条件的场景，本次未宣称覆盖。

候选基于 `b7d683826d7890d146cb406660d1bfb465af2f39` 加本次未提交修复，版本为 2.0.22；
并非 GitHub 上原 v2.0.22 Tag 的原始内容。工作区已有 2.0.20 版本文件改动未调整。
Windows 本机 Git 调用耗时明显：优先查找 Git 后完整回归仍超时，不能断言 PATH 是唯一原因。
云端工作流尚未使用修复重新运行，原失败版本与 Tag 均未更改。
正式端 17654 与测试后端 17655 健康检查正常，原有进程保持运行。

## 可重复验收

前提：Windows、Go、Node.js 22.13+（node:sqlite），在 `code` 根目录运行。
无需用户项目、账号或网络发布；脚本使用独立数据目录和系统分配的 loopback 端口。

```powershell
Push-Location sidecar
try {
    go build -trimpath -o .tmp/schema-upgrade.exe ./cmd/launcher-sidecar
    if ($LASTEXITCODE -ne 0) { throw 'sidecar compilation failed' }
} finally { Pop-Location }
$env:RUNDOCK_SIDECAR = (Resolve-Path sidecar/.tmp/schema-upgrade.exe).Path
node scripts/acceptance/schema-upgrade.mjs
if ($LASTEXITCODE -ne 0) { throw 'schema upgrade acceptance failed' }
```

每次生成独立 `outputs/acceptance/schema-upgrade-*/report.json`，记录二进制 SHA-256、
验收项与产物哈希，并保留合成数据库、日志。`--scenario=legacy` 可单独复现旧库路径。
真实打包脚本自动执行同一验收，Actions 单独保存合成验收工件，不加入公开安装包资产。

设计原因、失败清单和回滚边界见 [ADR](../adr/2026-10-04-schema-upgrade-order.md)。
完整生成物留在本地 `outputs/`/`.tmp/`，不提交仓库。
