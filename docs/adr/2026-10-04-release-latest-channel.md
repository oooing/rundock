# GitHub 首页 Latest 与平台同步边界

2026-10-04，用户要求发布后默认更新首页 Latest。首次多平台本地交付均设置了 makeLatest=false，导致 GitHub 首页仍显示旧 Web 版本，即使新 Release 及安装包已正式公开。

## 决策

- 主产品发布配置显式开启 makeLatest；多平台共仓只有一个首页 Latest，由该项目主产品目标负责。
- LocalPlay 的主产品为 Web，web-server-local 开启 makeLatest；PC、Android、扩展保留独立版本 Tag，不争抢首页标记。
- 已公开 web-server/v2.0.43 仅修改 Latest 标记，不覆盖 Tag、文件、签名或更新说明，不重新构建。
- 继续复用交付器的“草稿 → 完整文件校验 → 公开时设置 Latest”流程；失败的构建或草稿不应成为 Latest。
- 不改变历史冻结批次的 makeLatest 决策；配置改动只用于之后的新任务。

## 自动同步不等于首页标记

后续状态：LocalPlay 已安装独立 NAS 更新器，原生 ZIP 可以经该更新器触发部署；不再由旧 Watchtower 执行。详见 [双模式服务器更新](2026-10-04-dual-server-update.md)。下面记录本决策发生时的边界。

LocalPlay PC/Android 后端同步器按 desktop/v、android/v 的平台版本选包，独立于 GitHub 首页 Latest。已部署 Web 由镜像发布及 Watchtower 管理；上传原生 Web ZIP 或改 Latest 不会发布镜像、启动 Docker或更新 NAS 服务。

## 验证与回滚

本次为配置及 Release 元数据调整，无新增应用代码或功能测试。核对当前 Latest 的 Release ID、四个平台的产物哈希保持不变，以及后端读取到 Web 配置为 true、其他平台为 false。回执保存在 outputs/acceptance/localplay-release-20261004。

回滚时将主目标 makeLatest 恢复 false，并明确将哪个已有正式 Release 设为 Latest；不撤回或重传已公开资产。当前仓库实际 makeLatest 省略仍采用已有 false 行为，此次没有把缺省字段解释变化扩展到历史计划。
