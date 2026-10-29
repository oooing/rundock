package publisher

import (
	"context"
	"errors"
	"strings"
)

// Classify only failed commands. Never copy arbitrary command output into the
// primary error: it may contain credentials, even when the last line is useful.
func targetCommandFailure(targetName, step, output string, err error) (string, string) {
	prefix := targetName + " 的“" + stepLabel(step) + "”"
	if errors.Is(err, context.Canceled) {
		return "target_step_cancelled", prefix + "已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "target_step_timeout", prefix + "超时，请查看执行记录并调整该步骤的时限后重试"
	}
	text := strings.ToLower(output)
	if strings.Contains(text, "[android_native_source_missing]") {
		return "build_native_source_missing", prefix + "缺少原生模块构建文件。请将模块的 Gradle 配置、Manifest 和混淆规则纳入发布，返回检查后重新生成。"
	}
	if strings.Contains(text, "[android_native_module_missing]") {
		return "build_native_module_missing", prefix + "未通过原生模块校验。请检查原生模块构建文件是否纳入发布，以及混淆保留规则是否生效；返回检查后重新生成。"
	}
	if strings.Contains(text, "[rundock_required_asset_missing]") {
		return "build_required_asset_missing", prefix + "图标或启动图缺失或无效。请补齐并纳入发布，移除对应忽略规则后重试。"
	}
	if strings.Contains(text, "[rundock_signature_tool_missing]") {
		return "build_verification_tool_missing", prefix + "缺少 Windows 签名检查工具。请使用 Windows PowerShell，并修复其 Security 模块后重新构建。"
	}
	if strings.Contains(text, "get-filehash") && strings.Contains(text, "commandnotfoundexception") {
		return "build_verification_tool_missing", prefix + "无法计算文件校验码：PowerShell 的 Get-FileHash 命令不可用。请更新项目的打包脚本后重新构建；重复上传无法解决。"
	}
	for _, marker := range []string{"os error 112", "no space left on device", "磁盘空间不足", "there is not enough space on the disk", "error_disk_full", "code enospc", "enospc:"} {
		if strings.Contains(text, marker) {
			return "build_disk_full", prefix + "因磁盘空间不足而停止。请清理构建目录或系统临时目录所在磁盘，再重新构建；更换项目目录不会改变系统临时目录。"
		}
	}
	return "target_step_failed", prefix + "命令执行失败，请展开详细记录查看失败原因"
}
