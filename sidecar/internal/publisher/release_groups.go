package publisher

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/launcher-sidecar/internal/delivery"
	"github.com/launcher-sidecar/internal/releaseconfig"
)

// Opt-in only: other projects and previously frozen runs keep their old layout.
func validateReleaseGroups(plan *executionPlan) error {
	settings := map[string]*releaseconfig.Delivery{}
	versionGroups := map[string]bool{}
	for _, t := range plan.Targets {
		versionGroups[t.VersionGroup] = true
	}
	for _, t := range plan.Targets {
		d := t.Delivery
		if d == nil || !t.Selection.Publish || d.ReleaseGroup == "" {
			continue
		}
		if versionGroups["release-"+d.ReleaseGroup] {
			return &Error{Code: "delivery_group_conflict", Message: "合并发布标识与版本组冲突"}
		}
		if d.Deployment != nil && d.Deployment.Workflow != "" {
			return &Error{Code: "delivery_group_conflict", Message: "合并发布暂不支持自动触发部署工作流"}
		}
		if prev := settings[d.ReleaseGroup]; prev != nil {
			if !strings.EqualFold(prev.Repository, d.Repository) || !strings.EqualFold(prev.Account, d.Account) || prev.Prerelease != d.Prerelease {
				return &Error{Code: "delivery_group_conflict", Message: "合并发布必须使用相同仓库、账号和发布渠道"}
			}
		}
		settings[d.ReleaseGroup] = d
	}
	return nil
}

func releasePlatformName(plan *executionPlan, id string) string {
	for _, group := range plan.VersionGroups {
		if group.ID == id && strings.TrimSpace(group.Name) != "" {
			return group.Name
		}
	}
	return id
}

// combineReleaseGroups runs before any Seal/Publish. All artifacts participate
// in one immutable manifest; partial uploads cannot publish half a release.
func combineReleaseGroups(plan *executionPlan, groups map[string]delivery.Batch, sources map[string][]delivery.Source) (map[string]delivery.Batch, map[string][]delivery.Source, error) {
	if err := validateReleaseGroups(plan); err != nil {
		return nil, nil, err
	}
	members := map[string]map[string]bool{}
	for _, t := range plan.Targets {
		if t.Delivery == nil || !t.Selection.Publish || t.Delivery.ReleaseGroup == "" {
			continue
		}
		key := "release-" + t.Delivery.ReleaseGroup
		if members[key] == nil {
			members[key] = map[string]bool{}
		}
		members[key][t.VersionGroup] = true
	}
	for key, selected := range members {
		ids := make([]string, 0, len(selected))
		for id := range selected {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		primary := ids[0]
		for _, id := range ids {
			b, ok := groups[id]
			if !ok {
				return nil, nil, fmt.Errorf("missing delivery group %s", id)
			}
			if b.MakeLatest {
				primary = id
			}
		}
		b := groups[primary]
		b.GroupID, b.SyncURL, b.SyncPointer = key, "", ""
		b.DeploymentWorkflow, b.DeploymentStrategy = "", ""
		b.Components = []delivery.Component{}
		allSources := []delivery.Source{}
		titles := []string{}
		versions := map[string]string{}
		var notes strings.Builder
		notes.WriteString("## 本次发布\n\n| 平台 | 版本 | 下载文件 |\n| --- | --- | --- |\n")
		for _, id := range ids {
			member := groups[id]
			name := releasePlatformName(plan, id)
			b.Components = append(b.Components, delivery.Component{GroupID: id, Name: name, Tag: member.Tag, Version: member.Version, SyncURL: member.SyncURL, SyncPointer: member.SyncPointer})
			b.MakeLatest = b.MakeLatest || member.MakeLatest
			titles = append(titles, name+" v"+member.Version)
			versions[id] = member.Version
			links := []string{}
			for _, source := range sources[id] {
				file := filepath.Base(source.Path)
				lower := strings.ToLower(file)
				if strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".sig") || strings.HasSuffix(lower, ".sha256") {
					continue
				}
				links = append(links, "["+file+"](https://github.com/"+b.Repository+"/releases/download/"+url.PathEscape(b.Tag)+"/"+url.PathEscape(file)+")")
			}
			sort.Strings(links)
			fmt.Fprintf(&notes, "| %s | %s | %s |\n", strings.ReplaceAll(name, "|", "\\|"), member.Version, strings.Join(links, "<br>"))
			allSources = append(allSources, sources[id]...)
		}
		// Stable order also makes a sealing retry byte-identical.
		sort.Slice(allSources, func(i, j int) bool { return allSources[i].Path < allSources[j].Path })
		b.Title = strings.SplitN(b.Repository, "/", 2)[1] + " · " + strings.Join(titles, " / ")
		notes.WriteString("\n构建方式：本地构建。发布完成不代表服务器已更新。\n\n")
		body := strings.TrimSpace(plan.ReleaseNotes)
		if strings.HasPrefix(body, "**发布范围：**") {
			_, body, _ = strings.Cut(body, "\n")
		}
		notes.WriteString(strings.TrimSpace(body))
		metadata, err := json.Marshal(struct {
			SchemaVersion int               `json:"schemaVersion"`
			Versions      map[string]string `json:"versions"`
		}{1, versions})
		if err != nil {
			return nil, nil, err
		}
		notes.WriteString("\n\n<!-- rundock-release:" + string(metadata) + " -->")
		b.Notes = notes.String()
		for _, id := range ids {
			delete(groups, id)
			delete(sources, id)
		}
		groups[key], sources[key] = b, allSources
	}
	return groups, sources, nil
}
