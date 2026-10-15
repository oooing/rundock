package publisher

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

const maxCheckLogBytes = 64 * 1024

// These results describe work already performed by PrepareCandidate. They are
// built-in checks, not inferred project commands or evidence of a passing build.
func automaticCandidateChecks(secrets []SensitiveFinding, deps []DependencyFinding, unresolved int) []CheckResult {
	results := []CheckResult{
		{ID: "rundock:scope", Name: "文件范围与版本", Status: CheckPassed, Required: true, Reason: "已生成本次提交的独立副本，并校验所选文件和计划版本。"},
		{ID: "rundock:secrets", Name: "敏感内容", Status: CheckPassed, Required: true, Reason: "已扫描所选文件和保留的历史内容，未发现需要阻止提交的敏感内容。"},
		{ID: "rundock:dependencies", Name: "文件读取", Status: CheckPassed, Required: true, Reason: "所选文件可读取。引用推断仅供参考，代码是否有效由项目检查和构建确认。"},
	}
	if unresolved > 0 {
		results[0].Status = CheckBlocked
		results[0].Reason = "仍有文件用途需要确认，请在文件范围中处理。"
	}
	if len(secrets) > 0 {
		results[1].Status = CheckFailed
		results[1].Reason = "发现需要阻止提交的敏感内容，请查看具体文件。"
	}
	if hasBlockingDependency(deps) {
		results[2].Status = CheckFailed
		results[2].Reason = "有选中文件无法读取，请检查文件权限或刷新后重试。"
	}
	return results
}

func candidateCheckProfiles(req CandidateRequest, pf *Preflight, cfg *releaseconfig.Config) []releaseconfig.CheckProfile {
	profiles := append([]releaseconfig.CheckProfile{}, cfg.CheckProfiles...)
	if command := strings.TrimSpace(pf.Profile.PreReleaseCommand); command != "" && req.BuildMode != "github" {
		profiles = append(profiles, releaseconfig.CheckProfile{ID: "legacy:pre-release", Name: "发布前检查", Command: command, Required: true})
	}
	// Only persisted target commands are treated as confirmed. Discovery alone
	// never authorizes executing code or installation hooks.
	if cfg.Source == releaseconfig.SourceFile {
		for _, selection := range req.SelectedTargets {
			for _, target := range cfg.Targets {
				if target.ID == selection.TargetID && strings.TrimSpace(target.Steps.Check) != "" {
					// Keep VERSION symbolic until checks consume the actual candidate
					// bytes. A suggestion is not the version of an unchanged build.
					command := strings.ReplaceAll(target.Steps.Check, "${TARGET_ID}", target.ID)
					profiles = append(profiles, releaseconfig.CheckProfile{ID: "target:" + target.ID, Name: target.Name + " · 检查", Command: command, WorkingDir: target.WorkingDir, Required: true, OS: target.Runner.OS, TimeoutSeconds: target.Timeouts["check"]})
				}
			}
		}
	}
	return profiles
}

func (s *Service) RunCandidateChecks(ctx context.Context, appID, candidateID string) (*CandidateView, error) {
	cand := s.lookupCandidate(candidateID)
	if cand == nil || cand.AppID != appID {
		return nil, &Error{Code: "candidate_not_found", Message: "候选版本不存在或已失效"}
	}
	if !s.reserve(cand.RepoRoot) {
		return nil, &Error{Code: "release_in_progress", Message: "该仓库已有发布或检查任务正在执行"}
	}
	defer s.release(cand.RepoRoot)

	cand.mu.Lock()
	if err := s.validateCandidateBinding(ctx, cand, cand.Request); err != nil {
		cand.View.Status = CheckStale
		cand.View.Accepted = false
		cand.View.CanFormal = false
		cand.mu.Unlock()
		return nil, err
	}
	if cand.Request.SkipChecks || cand.View.Status == CheckCancelled {
		view := cloneView(cand.View)
		cand.mu.Unlock()
		return view, nil
	}
	if cand.View.Status == CheckRunning {
		cand.mu.Unlock()
		return nil, &Error{Code: "release_in_progress", Message: "该候选版本正在检查"}
	}
	if len(cand.View.SensitiveFindings) > 0 {
		view := cloneView(cand.View)
		cand.mu.Unlock()
		return view, nil
	}
	if cand.View.Status == CheckPassed && cand.View.Accepted {
		view := cloneView(cand.View)
		cand.mu.Unlock()
		return view, nil
	}
	profiles := append([]releaseconfig.CheckProfile{}, cand.FrozenProfiles...)
	profiles, versionErr := s.expandCandidateCheckVersions(ctx, cand, profiles)
	if versionErr != nil {
		cand.View.Status = CheckStale
		cand.View.Accepted = false
		cand.View.CanFormal = false
		cand.mu.Unlock()
		return nil, versionErr
	}
	automatic := append([]CheckResult{}, cand.AutomaticChecks...)
	results := plannedCheckResults(profiles, append([]string{}, cand.TargetKinds...))
	// Permissions changed without changing the tree or frozen commands. Previously
	// completed checks remain evidence; only unfinished or failed commands run.
	for i := range results {
		for _, previous := range cand.View.CheckResults {
			if previous.ID == results[i].ID && previous.Status == CheckPassed {
				results[i] = previous
				break
			}
		}
	}
	withAutomatic := func() []CheckResult { return append(append([]CheckResult{}, automatic...), results...) }
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cand.cancel = cancel
	cand.View.Status = CheckRunning
	cand.View.Accepted = false
	cand.View.CheckResults = withAutomatic()
	cand.mu.Unlock()

	profileByID := map[string]releaseconfig.CheckProfile{}
	for _, profile := range profiles {
		profileByID[profile.ID] = profile
	}
	for i := range results {
		if results[i].Status == CheckPassed || results[i].Status == CheckSkipped || results[i].Status == CheckUnverified {
			continue
		}
		profile, ok := profileByID[results[i].ID]
		if !ok {
			results[i].Status = CheckUnverified
			results[i].Reason = "冻结的检查配置已不存在"
			continue
		}
		started := time.Now()
		command := s.candidateCommand
		if command == nil {
			command = runProfileCommand
		}
		status, logText, reason := command(runCtx, cand.Work, profile)
		results[i].Status = status
		results[i].Log = redactSensitiveLog(logText)
		results[i].Reason = reason
		results[i].DurationMS = time.Since(started).Milliseconds()
		cand.mu.Lock()
		cand.View.CheckResults = withAutomatic()
		if status == CheckCancelled {
			cand.View.Status = CheckCancelled
			cand.View.Accepted = false
			cand.View.CanFormal = false
			cand.mu.Unlock()
			break
		}
		cand.mu.Unlock()
		if runCtx.Err() != nil {
			results[i].Status = CheckCancelled
			results[i].Reason = "已取消"
			break
		}
	}

	mutated := false
	var staleErr error
	if runCtx.Err() == nil {
		mutated = validateCandidateBytes(cand) != nil
		staleErr = s.validateCandidateBinding(ctx, cand, cand.Request)
	}
	cand.mu.Lock()
	defer cand.mu.Unlock()
	results = withAutomatic()
	cand.View.CheckResults = append([]CheckResult(nil), results...)
	cand.View.MutationDetected = mutated
	if staleErr != nil && runCtx.Err() == nil && cand.View.Status != CheckCancelled {
		cand.View.Status = CheckStale
		cand.View.Accepted = false
		cand.View.CanFormal = false
		warning := "源文件或发布配置已变化，请重新创建候选并检查"
		if mutated {
			warning = "检查命令修改了候选源内容，必须重新创建候选并检查"
		}
		cand.View.Warnings = append(cand.View.Warnings, warning)
		return cloneView(cand.View), nil
	}
	failed, blocked, unverified, cancelled, pendingRequired, applicable := false, false, false, false, false, 0
	for _, result := range results {
		if result.Status == CheckSkipped {
			continue
		}
		applicable++
		switch result.Status {
		case CheckBlocked:
			if result.Required {
				blocked = true
			}
		case CheckFailed:
			if result.Required {
				failed = true
			}
		case CheckCancelled:
			cancelled = true
		case CheckUnverified:
			if result.Required {
				unverified = true
			}
		case CheckPending, CheckRunning:
			if result.Required {
				pendingRequired = true
			}
		}
	}
	switch {
	case cancelled || runCtx.Err() != nil || cand.View.Status == CheckCancelled:
		cand.View.Status = CheckCancelled
		cand.View.Accepted = false
		cand.View.CanFormal = false
	case failed:
		cand.View.Status = CheckFailed
		cand.View.Accepted = false
		cand.View.CanFormal = false
	case blocked:
		cand.View.Status = CheckBlocked
		cand.View.Accepted = false
		cand.View.CanFormal = false
	case unverified || pendingRequired || applicable == 0:
		cand.View.Status = CheckUnverified
		cand.View.Accepted = false
		cand.View.CanFormal = false
	default:
		cand.View.Status = CheckPassed
		cand.View.CanFormal = cand.SafetyReady && len(cand.View.SensitiveFindings) == 0 && !hasBlockingDependency(cand.View.DependencyFindings)
		cand.View.Accepted = cand.Intent == IntentFormal && cand.View.CanFormal && cand.View.Status == CheckPassed
	}
	if cand.Intent == IntentSaveProgress && len(cand.View.SensitiveFindings) == 0 && cand.View.Status != CheckCancelled {
		cand.View.CanSaveProgress = true
		if cand.View.Status != CheckFailed && cand.View.Status != CheckCancelled {
			cand.View.Status = "ready"
		}
		cand.View.Accepted = false
	}
	return cloneView(cand.View), nil
}

func cloneView(view CandidateView) *CandidateView {
	out := view
	out.Classifications = append([]FileClassification{}, view.Classifications...)
	for i := range out.Classifications {
		out.Classifications[i].Reasons = append([]string{}, view.Classifications[i].Reasons...)
		out.Classifications[i].Sources = append([]string{}, view.Classifications[i].Sources...)
		out.Classifications[i].RuleIDs = append([]string{}, view.Classifications[i].RuleIDs...)
	}
	out.SelectedPaths = append([]string{}, view.SelectedPaths...)
	out.SensitiveFindings = append([]SensitiveFinding{}, view.SensitiveFindings...)
	out.DependencyFindings = append([]DependencyFinding{}, view.DependencyFindings...)
	out.CheckResults = append([]CheckResult{}, view.CheckResults...)
	out.Warnings = append([]string{}, view.Warnings...)
	return &out
}

func snapshotCandidateBytes(work string) (map[string][]byte, error) {
	files, err := listCandidateFiles(work)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		out[rel] = append([]byte(nil), raw...)
	}
	return out, nil
}

func sameByteMaps(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		other, ok := b[key]
		if !ok || !bytes.Equal(value, other) {
			return false
		}
	}
	return true
}

func runProfileCommand(ctx context.Context, candidateRoot string, profile releaseconfig.CheckProfile) (status, logText, reason string) {
	timeout := time.Duration(profile.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cwd := candidateRoot
	if strings.TrimSpace(profile.WorkingDir) != "" {
		abs, err := secureProjectPath(candidateRoot, profile.WorkingDir, true)
		if err != nil {
			return CheckUnverified, "", "检查工作目录无效或不可用"
		}
		cwd = abs
	}
	name, args := checkShell(profile.Command)
	workspace, tempErr := newScratch("command")
	if tempErr != nil {
		return CheckUnverified, "", "无法创建检查临时目录，请确认磁盘空间和目录权限"
	}
	defer func() {
		if err := workspace.cleanup(); err != nil {
			logText += "\n部分临时文件仍被占用，RunDock 将自动重试清理。"
		}
	}()
	cmd := exec.Command(name, args...)
	cmd.Dir = cwd
	cmd.Env = scratchEnvironment(sanitizedCheckEnv(), workspace.Dir)
	prepareCheckProcess(cmd)
	output := &limitedBuffer{max: maxCheckLogBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		if isMissingTool(err) {
			return CheckUnverified, "", missingCheckToolReason(name)
		}
		return CheckUnverified, redactSensitiveLog(err.Error()), "无法启动检查命令，请查看执行日志"
	}
	closeJob, jobErr := attachCheckProcess(cmd)
	if jobErr != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return CheckUnverified, "", "无法建立检查进程的取消保护"
	}
	defer closeJob()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		text := decodeCheckOutput(output.String())
		if err != nil {
			missingTool := missingExecutedCheckTool(profile.Command, cwd)
			if missingTool != "" || isMissingTool(err) || cmd.ProcessState.ExitCode() == 127 || cmd.ProcessState.ExitCode() == 9009 {
				// Diagnose only after shell execution failed. A preliminary PATH
				// lookup would wrongly reject local scripts and fallback commands.
				if missingTool != "" {
					return CheckUnverified, text, missingCheckToolReason(missingTool)
				}
				return CheckUnverified, text, "必需检查工具不可用"
			}
			return CheckFailed, text, fmt.Sprintf("项目检查未通过（退出码 %d），请查看执行日志", cmd.ProcessState.ExitCode())
		}
		return CheckPassed, text, ""
	case <-ctx.Done():
		closeJob()
		cmd.Process.Kill()
		<-done
		text := decodeCheckOutput(output.String())
		if ctx.Err() == context.DeadlineExceeded {
			return CheckFailed, text, "检查超时"
		}
		return CheckCancelled, text, "已取消"
	}
}

type limitedBuffer struct {
	buf bytes.Buffer
	n   int
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.n >= l.max {
		return len(p), nil
	}
	remain := l.max - l.n
	if len(p) > remain {
		_, _ = l.buf.Write(p[:remain])
		l.n = l.max
		return len(p), nil
	}
	n, err := l.buf.Write(p)
	l.n += n
	return n, err
}

func (l *limitedBuffer) String() string { return l.buf.String() }

func sanitizedCheckEnv() []string {
	allow := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "SYSTEMDRIVE": true, "WINDIR": true,
		"COMSPEC": true, "TEMP": true, "TMP": true, "TMPDIR": true, "HOME": true, "USERPROFILE": true,
		"HOMEDRIVE": true, "HOMEPATH": true, "LANG": true, "LC_ALL": true, "NUMBER_OF_PROCESSORS": true,
		"PROCESSOR_ARCHITECTURE": true, "PROGRAMFILES": true, "PROGRAMDATA": true, "PROGRAMW6432": true,
	}
	out := []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_ASKPASS=", "GCM_INTERACTIVE=never"}
	for _, env := range os.Environ() {
		key := env
		if i := strings.Index(env, "="); i >= 0 {
			key = env[:i]
		}
		if allow[strings.ToUpper(key)] {
			out = append(out, env)
		}
	}
	return out
}

func checkShell(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/d", "/s", "/c", command}
	}
	return "/bin/sh", []string{"-c", command}
}

func isMissingTool(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "executable file not found") || strings.Contains(text, "not found") || strings.Contains(text, "cannot find")
}
