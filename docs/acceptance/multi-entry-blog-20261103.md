# 中文启动脚本与多访问入口：本地 Web 验收

日期：2026-11-03。范围：RunDock 本地源码与 Web 开发实例；未打包、安装或发布桌面版。

## 行为

- 文件夹识别支持“启动、运行、开启、预览、打开、查看、管理”等中文前缀；启动入口优先于预览/Demo，中文安装、发布、停止等操作不作为启动推荐。忽略备份目录。
- 同目录、同脚本类型、相同内容（兼容 BOM 与 CRLF/LF 差异）的入口显示重复提示。已经添加的重复脚本默认不可直接再添加，勾选“仍作为独立项目添加”后才放行。不会删除文件或自动合并项目。
- 一个项目可以保存多个命名的本机 HTTP(S) 访问地址。添加页可修改建议地址；卡片上可打开、编辑、增加或移除。访问地址不增加进程、服务计数或启停操作。
- 简单 CMD 包装脚本调用同一个本地 Node 文件时，提供关联提示和“将访问入口合并到已有项目”。合并只增加去重后的地址，保留原项目名称、启动命令与运行状态。
- 地址建议来自选中脚本、其明确调用的本地 JS/MJS/CJS 文件，以及同目录中调用同一文件的包装脚本；排除 API/内部健康检查路径。不解释任意 shell 表达式，不承诺复杂脚本自动推断，用户可手工填写。
- 数据保存在 SQLite 的 `apps.access_links_json`；旧数据库自动补列，已有项目默认空列表。点击地址沿用现有网页检查与浏览器打开流程。

## 真实博客验收

项目：`D:\001oooing\WorkPitt\001AIcoding\053oooing-blog`。

Chrome 实测通过：

1. 文件夹导入推荐 `启动博客.cmd`，`启动博客Demo.cmd` 提示与前者内容相同。
2. 自动提供 `http://127.0.0.1:17653/` 和 `http://127.0.0.1:17653/admin`。
3. 确认后只创建一张卡片，导入本身没有启动或停止服务。
4. 再导入 Demo 时提示已存在同内容脚本；显式勾选后才允许继续，取消没有生成额外卡片。
5. 修改首页名称、移除后台地址，保存并刷新后仍保留。
6. 导入 `管理博客.cmd`，把后台地址合并回原卡片；仍为一张卡片，启动入口仍是 `启动博客.cmd`。
7. 非本机地址被拒绝，取消编辑不影响已保存地址。
8. 在该卡片点击启动，真实博客本地服务就绪。首页和后台分别经过实际打开请求及 Chrome 页面渲染验证；标题为“oooing｜开发、AI 与写作”和“写作台 — oooing”。
9. RunDock 浏览器未捕获到 JavaScript 运行错误。

测试使用已安装的 Chrome（Playwright `channel='chrome'`）；当前会话未能连接 Chrome 插件，因此未声称通过该插件验收。

## 检查与证据

- `npm run build` 通过；保留已有的主包大小提示。
- `i18n.test.mjs`、`card-services.test.mjs`、`apps-store.test.mjs`：21/21 通过。
- Go `internal/importer`、`internal/store`、`internal/accesslink` 全包通过，包含真实博客只读测试与保存、清空、重开数据库测试。
- Go API 网页入口/导入相关选择性回归通过；未运行整个发布系统的回归。
- `git diff --check` 通过。
- 自动化脚本：`scripts/acceptance/multi-entry-blog.py`。
- 报告及截图：`outputs/acceptance/multi-entry-blog-20261103/chrome-report.json`、`01-import.png`、`02-duplicate.png`、`03-card.png`、`04-home.png`、`05-admin.png`、`06-final.png`。

## 本地实例

- Web：`http://127.0.0.1:18752/`。
- 后端：`http://127.0.0.1:18751/`。
- 独立数据：`outputs/acceptance/multi-entry-blog-20261103/data`，未使用安装版或日常开发数据库。
- 后端构建：`sidecar/.tmp/rundock-multi-entry.exe`。
- Web 进程使用 `VITE_LAUNCHER_BASE=http://127.0.0.1:18751`。

重跑验收需新建空的独立数据库；脚本会拒绝复用已有项目数据。默认只测导入和编辑，传 `--start-blog` 才会启动真实博客并检查页面。博客启动脚本会按项目本身流程生成本地构建产物和初始化本地数据；没有修改博客启动脚本、发布线上内容或操作远端服务。

开始任务前已有的 package/lock/Tauri 版本改动保持原样。
