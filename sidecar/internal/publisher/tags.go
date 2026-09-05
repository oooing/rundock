package publisher

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/launcher-sidecar/internal/store"
)

const tagReleasePlanSchemaVersion = 1

type tagReleasePlan struct {
	SchemaVersion    int             `json:"schemaVersion"`
	TagName          string          `json:"tagName"`
	TargetVersion    string          `json:"targetVersion"`
	VersionGroupID   string          `json:"versionGroupId"`
	PushRemote       bool            `json:"pushRemote"`
	PublishesRelease bool            `json:"publishesRelease"`
	Targets          []tagPlanTarget `json:"targets"`
}

type tagPlanTarget struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Build   bool   `json:"build"`
	Package bool   `json:"package"`
	Publish bool   `json:"publish"`
	Deploy  bool   `json:"deploy"`
}

type tagOperationError struct {
	Code    string
	Message string
}

func (e *tagOperationError) Error() string { return e.Message }

func tagMessageForVersion(plan *executionPlan, version store.ReleaseVersion) (string, error) {
	if err := validateFrozenTagPlan(plan); err != nil {
		return "", err
	}
	notes := strings.TrimSpace(plan.ReleaseNotes)
	pushRemote := plan.PushRemote != nil && *plan.PushRemote
	metadata := tagReleasePlan{
		SchemaVersion: tagReleasePlanSchemaVersion, TagName: version.TagName,
		TargetVersion: version.TargetVersion, VersionGroupID: version.VersionGroupID,
		PushRemote:       pushRemote,
		PublishesRelease: pushRemote && plan.Automation != nil && plan.Automation.PublishesRelease,
		Targets:          []tagPlanTarget{},
	}
	for _, target := range plan.Targets {
		if version.VersionGroupID != "" && version.VersionGroupID != "repository" && target.VersionGroup != version.VersionGroupID {
			continue
		}
		selection := target.Selection
		metadata.Targets = append(metadata.Targets, tagPlanTarget{
			ID: target.ID, Kind: target.Kind, Build: selection.Build, Package: selection.Package,
			Publish: selection.Publish, Deploy: selection.Deploy,
		})
	}
	sort.Slice(metadata.Targets, func(i, j int) bool { return metadata.Targets[i].ID < metadata.Targets[j].ID })
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return "Release " + version.TagName + "\n\n" + notes + "\n\n<!-- launcher-release-plan:" + encoded + " -->", nil
}

func validateFrozenTagPlan(plan *executionPlan) error {
	if plan == nil || plan.PushRemote == nil || !plan.ReleaseNotesConfirmed || len(plan.ReleaseVersions) == 0 {
		return &Error{Code: "release_retry_requires_preflight", Message: "发布记录缺少完整的冻结 Tag 计划，请重新执行发布预检后再发布"}
	}
	if _, err := normalizeReleaseNotes(plan.ReleaseNotes); err != nil {
		return &Error{Code: "release_retry_requires_preflight", Message: "发布记录中的更新说明不完整，请重新执行发布预检后再发布"}
	}
	return nil
}

// ensureFrozenTag creates exactly the tag described by the frozen execution
// plan. During retry an existing tag is accepted only when it is annotated,
// points to the frozen commit and has byte-equivalent normalized contents.
func (s *Service) ensureFrozenTag(ctx context.Context, run *store.ReleaseRun, plan *executionPlan, version store.ReleaseVersion, allowExisting bool) error {
	expected, err := tagMessageForVersion(plan, version)
	if err != nil {
		if planErr, ok := err.(*Error); ok {
			return &tagOperationError{Code: planErr.Code, Message: planErr.Message}
		}
		return &tagOperationError{Code: "tag_failed", Message: "无法生成 Tag 说明"}
	}
	ref := "refs/tags/" + version.TagName
	if _, existsErr := s.git(ctx, run.RepoRoot, "rev-parse", "--verify", ref); existsErr == nil {
		if !allowExisting {
			return &tagOperationError{Code: "tag_collision", Message: "同名 Tag 已在发布过程中被创建：" + version.TagName}
		}
		kind, kindErr := s.git(ctx, run.RepoRoot, "cat-file", "-t", ref)
		commit, commitErr := s.git(ctx, run.RepoRoot, "rev-parse", ref+"^{}")
		contents, contentsErr := s.git(ctx, run.RepoRoot, "for-each-ref", "--format=%(contents)", ref)
		if kindErr != nil || commitErr != nil || contentsErr != nil || kind != "tag" || commit != run.CommitSHA || normalizeTagMessage(contents) != normalizeTagMessage(expected) {
			return &tagOperationError{Code: "tag_collision", Message: "同名 Tag 与冻结的发布内容不一致：" + version.TagName}
		}
		return nil
	}

	file, err := os.CreateTemp("", "launcher-release-tag-*.txt")
	if err != nil {
		return &tagOperationError{Code: "tag_failed", Message: "无法创建临时 Tag 说明文件"}
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return &tagOperationError{Code: "tag_failed", Message: "无法保护临时 Tag 说明文件"}
	}
	if _, err := file.WriteString(expected + "\n"); err != nil {
		_ = file.Close()
		return &tagOperationError{Code: "tag_failed", Message: "无法写入临时 Tag 说明文件"}
	}
	if err := file.Close(); err != nil {
		return &tagOperationError{Code: "tag_failed", Message: "无法关闭临时 Tag 说明文件"}
	}
	if out, err := s.git(ctx, run.RepoRoot, "tag", "-a", version.TagName, run.CommitSHA, "--cleanup=verbatim", "-F", path); err != nil {
		return &tagOperationError{Code: "tag_failed", Message: redact(out)}
	}
	return nil
}

func normalizeTagMessage(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
}
