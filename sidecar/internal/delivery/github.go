package delivery

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type githubRelease struct {
	ID         int64  `json:"id"`
	Tag        string `json:"tag_name"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	URL        string `json:"html_url"`
}
type githubAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	State  string `json:"state"`
	Digest string `json:"digest"`
}

func (e *Engine) Preflight(ctx context.Context, b Batch) error {
	account, err := e.Client.Identity(ctx)
	if err != nil {
		return err
	}
	if !strings.EqualFold(account, b.Account) {
		return failure("github_account_changed", "当前 GitHub 登录账号与发布配置不一致")
	}
	var repo struct {
		FullName    string `json:"full_name"`
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if err = e.Client.JSON(ctx, "GET", "repos/"+b.Repository, nil, &repo); err != nil {
		return err
	}
	if !strings.EqualFold(repo.FullName, b.Repository) || !repo.Permissions.Push {
		return failure("github_repository_forbidden", "发布仓库身份或写入权限不符合配置")
	}
	return e.CheckRemoteWorkflows(ctx, b)
}

func (e *Engine) verifyTag(ctx context.Context, b Batch) error {
	var ref struct {
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := e.Client.JSON(ctx, "GET", "repos/"+b.Repository+"/git/ref/tags/"+url.PathEscape(b.Tag), nil, &ref); err != nil {
		return err
	}
	for n := 0; ref.Object.Type == "tag" && n < 8; n++ {
		var tag struct {
			Object struct {
				Type string `json:"type"`
				SHA  string `json:"sha"`
			} `json:"object"`
		}
		if err := e.Client.JSON(ctx, "GET", "repos/"+b.Repository+"/git/tags/"+ref.Object.SHA, nil, &tag); err != nil {
			return err
		}
		ref.Object = tag.Object
	}
	if ref.Object.Type != "commit" || ref.Object.SHA != b.Commit {
		return failure("release_tag_conflict", "远端 Tag 指向的提交与本次安装包不一致")
	}
	return nil
}

func (e *Engine) findRelease(ctx context.Context, b Batch) (*githubRelease, error) {
	var release githubRelease
	err := e.Client.JSON(ctx, "GET", "repos/"+b.Repository+"/releases/tags/"+url.PathEscape(b.Tag), nil, &release)
	if err == nil {
		return &release, nil
	}
	if !isStatus(err, 404) {
		return nil, err
	}
	// Include drafts visible to this account, not only published tag lookups.
	for page := 1; page <= 100; page++ {
		var list []githubRelease
		if err = e.Client.JSON(ctx, "GET", fmt.Sprintf("repos/%s/releases?per_page=100&page=%d", b.Repository, page), nil, &list); err != nil {
			return nil, err
		}
		for _, r := range list {
			if r.Tag == b.Tag {
				copy := r
				return &copy, nil
			}
		}
		if len(list) < 100 {
			return nil, nil
		}
	}
	return nil, failure("release_lookup_incomplete", "远端发布记录过多，无法完整核查同版本冲突")
}

func (e *Engine) assets(ctx context.Context, b Batch, id int64) (map[string]githubAsset, error) {
	out := map[string]githubAsset{}
	for page := 1; page <= 21; page++ {
		var list []githubAsset
		if err := e.Client.JSON(ctx, "GET", fmt.Sprintf("%s/assets?per_page=100&page=%d", releasePath(b.Repository, id), page), nil, &list); err != nil {
			return nil, err
		}
		for _, a := range list {
			if _, ok := out[a.Name]; ok {
				return nil, failure("release_asset_conflict", "远端出现重名产物")
			}
			out[a.Name] = a
		}
		if len(list) < 100 {
			return out, nil
		}
	}
	return nil, failure("release_assets_incomplete", "远端产物集合超过核查范围")
}

func (e *Engine) sameAsset(ctx context.Context, b Batch, want File, actual githubAsset) error {
	if actual.State != "uploaded" || actual.Size != want.Size {
		return failure("release_asset_conflict", "远端产物状态或大小不一致："+want.Name)
	}
	digest := strings.TrimPrefix(actual.Digest, "sha256:")
	if !hashPattern.MatchString(digest) {
		var err error
		digest, err = e.Client.Digest(ctx, fmt.Sprintf("repos/%s/releases/assets/%d", b.Repository, actual.ID))
		if err != nil {
			return err
		}
	}
	if digest != want.SHA256 {
		return failure("release_asset_conflict", "远端同名产物内容不同："+want.Name)
	}
	return nil
}

func (e *Engine) verifyAssets(ctx context.Context, b Batch, id int64) error {
	assets, err := e.assets(ctx, b, id)
	if err != nil {
		return err
	}
	if len(assets) != len(b.Files) {
		return failure("release_assets_incomplete", "远端产物集合与封存清单不一致")
	}
	for _, f := range b.Files {
		a, ok := assets[f.Name]
		if !ok {
			return failure("release_assets_incomplete", "远端缺少产物："+f.Name)
		}
		if err = e.sameAsset(ctx, b, f, a); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) Publish(ctx context.Context, b Batch) (resultErr error) {
	defer func() {
		if resultErr != nil {
			code, message := ErrorInfo(resultErr)
			_ = e.Store.UpdateReleaseDelivery(b.RunID, b.GroupID, "failed", 0, "", code, message)
		}
	}()
	if err := e.Verify(ctx, b); err != nil {
		return err
	}
	if err := e.Preflight(ctx, b); err != nil {
		return err
	}
	if err := e.verifyTag(ctx, b); err != nil {
		return err
	}
	rows, err := e.Store.ReleaseDeliveries(b.RunID)
	if err != nil {
		return err
	}
	digest := ""
	for _, r := range rows {
		if r.GroupID == b.GroupID {
			digest = r.ManifestSHA256
		}
	}
	if digest == "" {
		return failure("manifest_missing", "缺少持久产物清单")
	}
	marker := "<!-- rundock-delivery:" + b.RunID + ":" + b.GroupID + ":" + digest + " -->"
	release, err := e.findRelease(ctx, b)
	if err != nil {
		return err
	}
	if release == nil {
		if err = e.Store.UpdateReleaseDelivery(b.RunID, b.GroupID, "creating_draft", 0, "", "", ""); err != nil {
			return err
		}
		var created githubRelease
		createErr := e.Client.JSON(ctx, "POST", "repos/"+b.Repository+"/releases", map[string]any{"tag_name": b.Tag, "target_commitish": b.Commit, "name": b.Tag, "body": b.Notes + "\n\n" + marker, "draft": true, "prerelease": b.Prerelease, "make_latest": "false"}, &created)
		// Also reconcile a successful mutation whose response was lost.
		release, err = e.findRelease(ctx, b)
		if err != nil {
			return err
		}
		if release == nil {
			if createErr != nil {
				return createErr
			}
			return failure("draft_unconfirmed", "尚未确认草稿创建结果")
		}
	}
	if release.Tag != b.Tag || release.Prerelease != b.Prerelease {
		return failure("release_conflict", "远端版本或发布渠道与计划不同")
	}
	if !release.Draft {
		if err = e.verifyAssets(ctx, b, release.ID); err != nil {
			return err
		}
		return e.Store.UpdateReleaseDelivery(b.RunID, b.GroupID, "published", release.ID, release.URL, "", "")
	}
	if !strings.Contains(release.Body, marker) {
		return failure("release_draft_conflict", "此版本存在其他任务创建的草稿，未自动接管")
	}
	if err = e.Store.UpdateReleaseDelivery(b.RunID, b.GroupID, "uploading", release.ID, release.URL, "", ""); err != nil {
		return err
	}
	assets, err := e.assets(ctx, b, release.ID)
	if err != nil {
		return err
	}
	expected := map[string]bool{}
	for _, f := range b.Files {
		expected[f.Name] = true
	}
	for name := range assets {
		if !expected[name] {
			return failure("release_asset_conflict", "草稿包含本次清单之外的文件："+name)
		}
	}
	for _, f := range b.Files {
		if err = ctx.Err(); err != nil {
			return err
		}
		if a, ok := assets[f.Name]; ok {
			if a.State == "starter" && a.Size == 0 {
				if err = e.Client.JSON(ctx, "DELETE", fmt.Sprintf("repos/%s/releases/assets/%d", b.Repository, a.ID), nil, nil); err != nil {
					return err
				}
			} else {
				if err = e.sameAsset(ctx, b, f, a); err != nil {
					return err
				}
				continue
			}
		}
		path, err := e.FilePath(b, f)
		if err != nil {
			return err
		}
		uploadErr := e.Client.Upload(ctx, b.Repository, release.ID, path, f.Name)
		current, readErr := e.assets(ctx, b, release.ID)
		if readErr != nil {
			return readErr
		}
		a, ok := current[f.Name]
		if !ok {
			if uploadErr != nil {
				return uploadErr
			}
			return failure("upload_unconfirmed", "尚未确认文件上传结果："+f.Name)
		}
		if err = e.sameAsset(ctx, b, f, a); err != nil {
			return err
		}
	}
	if err = e.Verify(ctx, b); err != nil {
		return err
	}
	if err = e.verifyTag(ctx, b); err != nil {
		return err
	}
	if err = e.verifyAssets(ctx, b, release.ID); err != nil {
		return err
	}
	if err = e.Store.UpdateReleaseDelivery(b.RunID, b.GroupID, "publishing", release.ID, release.URL, "", ""); err != nil {
		return err
	}
	var updated githubRelease
	patchErr := e.Client.JSON(ctx, "PATCH", releasePath(b.Repository, release.ID), map[string]any{"draft": false, "make_latest": strconv.FormatBool(b.MakeLatest)}, &updated)
	if err = e.Client.JSON(ctx, "GET", releasePath(b.Repository, release.ID), nil, &updated); err != nil {
		return err
	}
	if updated.Draft {
		if patchErr != nil {
			return patchErr
		}
		return failure("publish_unconfirmed", "草稿尚未公开")
	}
	if updated.Tag != b.Tag || updated.Prerelease != b.Prerelease {
		return failure("release_conflict", "发布结果与本次计划不同")
	}
	if err = e.verifyTag(ctx, b); err != nil {
		return err
	}
	if err = e.verifyAssets(ctx, b, release.ID); err != nil {
		return err
	}
	return e.Store.UpdateReleaseDelivery(b.RunID, b.GroupID, "published", release.ID, updated.URL, "", "")
}
