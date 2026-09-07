package publisher

import "strings"

// Empty is retained for internal callers and historical execution plans. The
// public create API resolves an omitted mode from the project (default GitHub).
func validateBuildMode(mode string, plan *executionPlan, push bool) error {
	if mode == "" {
		return nil
	}
	if mode != "github" && mode != "local" {
		return &Error{Code: "invalid_build_mode", Message: "请选择 GitHub 云端构建或本地构建"}
	}
	if len(plan.Targets) == 0 {
		return nil
	} // plain Git submission
	if mode == "local" && push {
		return &Error{Code: "build_mode_mismatch", Message: "本地构建不会上传代码或触发云端构建，请关闭“提交后上传”"}
	}
	for _, target := range plan.Targets {
		cloud := strings.EqualFold(strings.TrimSpace(target.Runner.Type), "git-push")
		if mode == "github" && (!cloud || target.Selection.Build || target.Selection.Package || target.Selection.Deploy || !target.Selection.Publish) {
			return &Error{Code: "build_mode_mismatch", Message: target.Name + " 未配置可用的 GitHub 云端构建，请配置云端流程或切换为本地构建"}
		}
		if mode == "local" && (cloud || target.Selection.Publish || target.Selection.Deploy) {
			return &Error{Code: "build_mode_mismatch", Message: target.Name + " 不是本地构建目标，请选择本地构建步骤"}
		}
	}
	return nil
}
