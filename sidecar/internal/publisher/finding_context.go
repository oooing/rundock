package publisher

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

type FindingSourceLine struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}
type FindingContext struct {
	Path    string              `json:"path"`
	Line    int                 `json:"line"`
	Lines   []FindingSourceLine `json:"lines"`
	HasMore bool                `json:"hasMore"`
}

// Raw source is returned only by this local, no-store endpoint, never added to
// candidate findings, release logs, notes or persisted plans.
func (s *Service) FindingContext(appID, candidateID, fingerprint string, expanded bool) (*FindingContext, error) {
	cand := s.lookupCandidate(candidateID)
	if cand == nil || cand.AppID != appID {
		return nil, &Error{Code: "candidate_not_found", Message: "检查记录已失效，请重新检查"}
	}
	cand.mu.Lock()
	defer cand.mu.Unlock()
	if cand.View.Status == CheckRunning || cand.View.Status == CheckCancelled {
		return nil, &Error{Code: "candidate_stale", Message: "检查记录已失效，请重新检查"}
	}
	var finding *SensitiveFinding
	for i := range cand.View.SensitiveFindings {
		if fingerprint != "" && cand.View.SensitiveFindings[i].Fingerprint == fingerprint {
			finding = &cand.View.SensitiveFindings[i]
			break
		}
	}
	if finding == nil {
		return nil, &Error{Code: "finding_not_found", Message: "此问题已失效，请重新检查"}
	}
	invalid := func() (*FindingContext, error) {
		return nil, &Error{Code: "context_unavailable", Message: "无法读取对应代码，请重新检查"}
	}
	path, err := secureProjectPath(cand.Work, finding.Path, false)
	if err != nil {
		return invalid()
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || isPathLink(info) {
		return invalid()
	}
	const maxSource = 8 * 1024 * 1024
	if info.Size() > maxSource {
		return nil, &Error{Code: "context_too_large", Message: "文件过大，请在编辑器查看"}
	}
	f, err := os.Open(path)
	if err != nil {
		return invalid()
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return invalid()
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxSource+1))
	if err != nil || len(raw) > maxSource || !utf8.Valid(raw) || looksBinary(raw) {
		return invalid()
	}
	sum := sha256.Sum256(raw)
	if finding.ContentFingerprint == "" || hex.EncodeToString(sum[:]) != finding.ContentFingerprint {
		return invalid()
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	line := finding.Line
	if line < 1 {
		line = 1
	}
	if line > len(lines) {
		return invalid()
	}
	radius := 3
	if expanded {
		radius = 15
	}
	start, end := max(1, line-radius), min(len(lines), line+radius)
	result := &FindingContext{Path: finding.Path, Line: finding.Line, Lines: []FindingSourceLine{}, HasMore: start > 1 || end < len(lines)}
	for n := start; n <= end; n++ {
		result.Lines = append(result.Lines, FindingSourceLine{Number: n, Text: lines[n-1]})
	}
	return result, nil
}
