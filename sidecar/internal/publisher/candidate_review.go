package publisher

import (
	"context"
	"strings"
)

// Confirmation changes permission for exact findings, never the saved tree or
// the build plan. Reuse the scan and commands, but reject changed inputs.
func (s *Service) ReviewCandidate(ctx context.Context, appID, candidateID string, exceptions []SensitiveException) (*CandidateView, error) {
	cand := s.lookupCandidate(candidateID)
	if cand == nil || cand.AppID != appID {
		return nil, &Error{Code: "candidate_not_found", Message: "检查记录已失效，请重新检查"}
	}
	if !s.reserve(cand.RepoRoot) {
		return nil, &Error{Code: "release_in_progress", Message: "该仓库已有检查或发布正在执行"}
	}
	defer s.release(cand.RepoRoot)
	cand.mu.Lock()
	defer cand.mu.Unlock()
	if cand.View.Status == CheckCancelled || cand.View.Status == CheckRunning || cand.View.Status == CheckStale || cand.Request.SkipChecks {
		return nil, &Error{Code: "candidate_stale", Message: "当前检查记录不能确认，请重新检查"}
	}
	pf, cfg, err := s.candidateBindingState(ctx, cand, cand.Request)
	if err != nil {
		cand.View.Status, cand.View.Accepted, cand.View.CanFormal = CheckStale, false, false
		return nil, err
	}
	next := append([]SensitiveException{}, cand.Request.SensitiveExceptions...)
	for _, ex := range exceptions {
		if strings.TrimSpace(ex.Reason) == "" {
			return nil, &Error{Code: "invalid_exception", Message: "请说明确认原因"}
		}
		duplicate := false
		for _, saved := range next {
			if saved.Path == ex.Path && saved.ContentFingerprint == ex.ContentFingerprint && saved.FindingFingerprint == ex.FindingFingerprint && saved.Reason == ex.Reason {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		valid := false
		for _, finding := range cand.View.SensitiveFindings {
			if ex.Path == finding.Path && ex.ContentFingerprint == finding.ContentFingerprint && ex.FindingFingerprint == finding.Fingerprint {
				valid = true
				break
			}
		}
		if !valid {
			return nil, &Error{Code: "invalid_exception", Message: "确认与当前问题不匹配，请重新检查"}
		}
		next = append(next, ex)
	}
	if len(next) == len(cand.Request.SensitiveExceptions) {
		return cloneView(cand.View), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content := map[string]string{}
	for _, finding := range cand.View.SensitiveFindings {
		content[finding.Path] = finding.ContentFingerprint
	}
	cand.View.SensitiveFindings = filterSensitiveExceptions(cand.View.SensitiveFindings, next, content)
	cand.Request.SensitiveExceptions = next
	cand.Binding = candidateBinding(cand.Request, pf, cfg)
	cand.View.Classifications = cloneView(CandidateView{Classifications: cand.BaseClassifications}).Classifications
	for i := range cand.View.Classifications {
		for _, finding := range cand.View.SensitiveFindings {
			if finding.Path == cand.View.Classifications[i].Path {
				cand.View.Classifications[i].Category = CategorySensitive
				cand.View.Classifications[i].SensitiveKind = finding.Kind
				cand.View.Classifications[i].SelectedDefault = false
			}
		}
	}
	scopeReady := true
	for i := range cand.AutomaticChecks {
		check := &cand.AutomaticChecks[i]
		if check.ID == "rundock:scope" && check.Status != CheckPassed {
			scopeReady = false
		}
		if check.ID == "rundock:secrets" && len(cand.View.SensitiveFindings) == 0 {
			check.Status, check.Reason = CheckPassed, "敏感内容已按当前文件确认，其他扫描结果保持有效。"
		}
		for j := range cand.View.CheckResults {
			if cand.View.CheckResults[j].ID == check.ID {
				cand.View.CheckResults[j] = *check
			}
		}
	}
	cand.View.CanSaveProgress = len(cand.View.SensitiveFindings) == 0
	cand.SafetyReady = cand.View.CanSaveProgress && scopeReady && !hasBlockingDependency(cand.View.DependencyFindings)
	if cand.SafetyReady {
		cand.View.Status = CheckPending
		if cand.Intent == IntentSaveProgress {
			cand.View.Status = "ready"
		}
	}
	// Required commands blocked by the original finding still need their first
	// execution. Confirmation alone cannot manufacture an accepted release.
	cand.View.Accepted, cand.View.CanFormal = false, false
	return cloneView(cand.View), nil
}
