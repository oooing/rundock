# RunDock 文档

使用说明、架构决策和精简验收结论集中在这里；临时测试产物不提交仓库。

## 按任务阅读

| 我想做什么 | 文档 |
| --- | --- |
| 使用 RunDock | [中文使用指南](guide.zh-CN.md) · [English guide](guide.en.md) |
| 在本机生成安装包 | [本地构建](onboarding/local-build.md) |
| 接入项目的构建与交付 | [构建与交付接入指南](onboarding/build-delivery.md) |
| 启动开发环境、查日志 | [开发脚本说明](../scripts/README.md) |
| 理解设计取舍 | [架构决策](adr/) |
| 查看实施计划 | [研发计划](plans/) |
| 查看早期产品研究 | [Windows 启动平台历史研究](research/windows-ai-launcher.md) |
| 查看已经验证的结果与限制 | [验收记录](acceptance/) |
| 维护 README 配图 | [品牌与界面素材](media/README.md) |

## 文件放哪里

- `docs/`：长期指南、架构决策、计划，以及包含复现方法的精简验收记录。
- `scripts/acceptance/`：可重复运行的端到端验收脚本；Go 的 `*_test.go` 保留在所属模块旁，不挪到文档目录。
- `outputs/`：运行日志、截图、录像、临时数据库、构建包等本地证据，不提交。验收记录说明生成方法，不依赖把整个证据目录上传。
- `docs/media/`：文档实际引用的配图；临时测试截图仍放 `outputs/`。

LocalPlay 专用接入包是另一个项目的集成资料，不属于 RunDock 产品源码；本地保留在仓库外的 `../local-materials/localplay/`（相对 `code` 目录），不随 RunDock 提交。通用接入能力及说明仍保留在本仓库。

提交前检查文件范围：保留源码、可复现的测试和必要文档；排除日志、数据库、安装包和凭证。验收通过不等于已经安装、上线或公开发布。

## 提交前核对

在 `code` 根目录运行：

```powershell
git status --short
git check-ignore -v outputs/acceptance/example/report.json outputs/example.apk
git diff --check
```

`outputs/` 应被忽略，文档和测试源码不应被忽略。按任务选择文件，不要直接全选所有未提交改动。
忽略规则不影响已跟踪文件：历史 `sidecar/launcher-sidecar-dev.exe` 和 Tauri sidecar 二进制仍已跟踪，本轮未改变它们的跟踪或构建方式；是否移除需另行核对桌面打包流程。
