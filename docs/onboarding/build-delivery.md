# 构建与交付接入指南

RunDock 负责冻结任务、执行项目命令、保存产物、发布和恢复。项目负责自己的环境检查、打包方法、版本与签名验证。

## 项目归属

RunDock 只保留通用发布器、自身的 Windows 构建配置和验收脚本。
每个受管理项目的工作流、构建命令、签名脚本和迁移资料应放在该项目自己的仓库，不复制进 RunDock。
此前 LocalPlay 的专用接入包已保存在仓库外的本地归档，不是运行依赖或通用安装步骤。

RunDock 自身生成 Windows EXE、MSI 和 SHA256SUMS，校验内嵌版本、x64、哈希及完整集合。
历史安装包沿用未签名策略；将来要求 Authenticode 时须明确证书身份并同步构建、验证规则。

## 首次启用顺序

1. 在受管理项目自己的仓库配置构建命令、产物和工作流，并将需要的工作流同步到其默认分支。
2. 安装或运行包含本次发布器改动的 RunDock。旧版发布器不识别新的 dispatch 配置。
3. 在本机 GitHub CLI 登录配置中的账号，凭证保存到系统密钥环。发布器检查当前账号、仓库身份及写入权限。
4. 打开项目发布界面，选择本地构建目标，再选择“仅保存在本机”或“GitHub Release”。正式交付需要同步代码和 Tag。
5. 查看自动建议的版本和本次冻结范围，确认发布。安装包全部验证通过后才进入草稿上传和公开阶段。

本地修改不代表远端工作流已更新，也不代表安装版已替换。远端仍使用旧工作流时，
本地交付预检会阻断并说明应先同步。不要通过删除工作流检查来绕过这一步。

## 项目配置

`.launcher/release.yaml` 继续使用 schemaVersion 1；文件为 JSON（YAML 1.2 的子集）。在现有本地目标上增加：

```json
{
  "timeouts": { "check": 900, "build": 7200, "package": 600 },
  "artifactRules": [
    { "pattern": "release/app-${VERSION}.zip", "min": 1, "max": 1 },
    { "pattern": "release/app-${VERSION}.zip.sha256", "min": 1, "max": 1 }
  ],
  "verification": [
    { "name": "版本、身份和内容校验", "command": "node scripts/verify-package.mjs", "timeoutSeconds": 120 }
  ],
  "delivery": {
    "provider": "github",
    "repository": "owner/repository",
    "account": "account-name",
    "workflowPolicy": "dispatch-only",
    "makeLatest": false
  }
}
```

路径相对于目标工作目录。`${VERSION}`、`${TAG}`、`${COMMIT_SHA}` 来自冻结计划。必需文件名应明确包含版本，
避免宽泛通配符收集旧包。`artifacts` 用于登记生成文件，`artifactRules` 决定真正交付的完整集合。
验证命令必须实际核验包内版本、平台和项目签名策略，不能只检查文件是否存在。
环境依赖及版本要求应放在项目已有 check 命令中；每步超时为 1–86400 秒，未指定时为 600 秒。

本地交付目标不能再配置自定义 publish/deploy 命令。每个版本组形成独立批次，同一组全部目标采用一致交付设置。
首版不在一个任务中混合内置交付与云端/自定义发布，不协调同 Tag 的本地和云端产物。

云端目标继续使用 runner.type=git-push，但 publish 设置为 `workflow-dispatch:文件.yml`，automation.trigger 设置为 dispatch，
并提供 account。工作流接受 release_tag、release_commit、release_run_id、target_id，先验证 Tag/提交/目标，再构建。
没有 release_tag 的手动构建保留原行为；正式任务使用 `rundock:<任务ID>:<目标ID>` 作为 run-name。

## 签名与账号

账号配置只保存身份名称，token 不进入仓库或 API 响应；使用前核对本机实际登录账号。
GitHub Secrets 只能通过 API 查看名称和时间，无法取回私钥值。云端能签名，不意味着本机已经拥有签名备份。

Android 等项目的签名由该项目自己的构建和验证脚本负责；原始密钥与恢复口令不得进入源码、日志或产物。
正式包须验证既有签名身份，不能用临时生成或 debug 证书替代。
RunDock 的 Authenticode 可选验证要求显式配置证书指纹，当前不强行要求其历史上未使用的证书。

## 失败后继续

完整构建通过验证后，文件复制到数据库旁的 `releases/<任务ID>/<版本组>/`，按 SHA-256 保存并记录不可变清单。
上传断网时打开同一任务继续，系统核对远端后只上传缺少的文件；重启后也不需要原候选目录或当前 HEAD。
保留数据库及对应 releases 目录，迁移到另一台机器时两者一起备份。不要删除未完成批次。

同名同内容识别为已完成；异内容、未知草稿、Tag 异提交、缺必需文件、封存文件被修改均停止。
未封存的构建中断必须重新预检并构建。取消会停止本机构建进程树及当前上传，保留产物与草稿；
取消请求不撤销已公开的 Release，也不会取消已交接的 GitHub Actions 任务。

请求响应丢失时，GitHub 操作可能已经成功。工作流调用结果不确定且暂时查不到任务时，重试仅继续查询，
不会为了方便而重复启动构建。明确 4xx 拒绝可在修复后重试。

## 服务器同步

可选 `delivery.sync` 包含无凭证的 HTTPS `url` 和 `jsonPointer`，例如 `/data/version`。
只有返回的版本字符串精确等于本次版本才显示已核验；HTTP 200 但版本旧仍显示等待同步。
未配置时显示未配置验证。同步失败不会把已经成功的 GitHub 发布改为失败，可单独重新检查。
此模块只观察版本，不代替服务器部署脚本；是否配置端点以各项目实际配置为准。

## 回滚

回滚应用前备份数据库和 releases 目录，未完成交付任务先停止。新增表不把旧产物伪装为已封存。
回滚到不支持 dispatch 的旧发布器时，需同时恢复原项目配置与工作流触发策略，避免云端发布入口失配。
远端已经公开的 Release 不自动撤回或覆盖。
