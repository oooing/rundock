# 自动发布发现的范围

自动识别是候选建议，项目内 `.launcher/release.yaml` 优先。特殊构建使用显式配置，不增加框架专用适配器。

- Git 项目只扫描已跟踪和未忽略的未跟踪文件；使用 Git 自身规则，支持多层 `.gitignore`、例外与 `.git/info/exclude`。已跟踪文件不会被忽略规则排除。
- 不递归进入其他仓库、子模块目录或文件链接，不读取通过目录链接跳到仓库外的候选。已删除文件不再识别。
- Git 读取失败、范围过大或超时会提示；不回退为扫描被忽略目录。没有 Git 仓库的普通目录沿用固定目录排除、深度和数量限制。
- 显式配置可引用自动发现之外的目录，但实际发布前仍校验路径、版本文件和 Git 可提交性。
- 仅归属禁用目标的版本文件不会阻断其他启用目标；重新启用后恢复校验。执行计划只验证本次已选目标涉及的版本文件。

大量文件应由项目打包后交付。BrickMuse 的 2,771 个静态文件打成一个 tar.gz，再由 RunDock 登记哈希；没有扩大现有 2,000 项产物限制。

测试：`go test ./internal/releaseconfig`；发布器的 `TestDisabledTargetVersionFilesDoNotBlockActiveTarget` 验证禁用目标边界。可选真实项目构建测试 `TestLocalProjectBuildIntegration` 只在设置 `RUNDOCK_BUILD_TEST_ROOT` 后运行，读取该项目的唯一显式本地构建目标，使用临时数据库，不调用提交或部署流程。
