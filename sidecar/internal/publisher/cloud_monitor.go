package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/store"
)

type githubWorkflowRun struct {
	ID         int64     `json:"id"`
	Attempt    int       `json:"run_attempt"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	HeadSHA    string    `json:"head_sha"`
	HeadBranch string    `json:"head_branch"`
	Event      string    `json:"event"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	CreatedAt  time.Time `json:"created_at"`
}
type githubRunList struct {
	Total int                 `json:"total_count"`
	Runs  []githubWorkflowRun `json:"workflow_runs"`
}
type githubJob struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	Steps      []struct {
		Name       string `json:"name"`
		Conclusion string `json:"conclusion"`
	} `json:"steps"`
}

type githubReader func(context.Context, string, any) error

const cloudPollInterval = 30 * time.Second

// MonitorCloudBuilds lives with the sidecar, independent of the release modal.
// State and acknowledgements survive application restarts. GitHub is read-only.
func (s *Service) MonitorCloudBuilds(ctx context.Context) {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.checkCloudBuilds(ctx, readGitHub, time.Now().UTC())
		timer.Reset(cloudPollInterval)
	}
}

func (s *Service) checkCloudBuilds(ctx context.Context, read githubReader, now time.Time) {
	ids, err := s.store.RecentCloudReleaseIDs()
	if err != nil {
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		run, err := s.store.GetReleaseRun(id)
		if err != nil || run == nil {
			continue
		}
		plan, err := parseExecutionPlan(run.ExecutionPlan)
		if err != nil || !automationHandoffApplies(run, plan) {
			continue
		}
		repo := githubRepository(plan.RemoteURL)
		if repo == "" {
			continue
		}
		old, err := s.store.GetCloudBuild(id)
		if err != nil {
			continue
		}
		if old != nil {
			if old.State == "succeeded" || old.State == "superseded" {
				continue
			}
			next, _ := time.Parse(time.RFC3339, old.NextCheck)
			if old.State == "failed" && old.Errors == 0 {
				// Older builds persisted a five-minute failure interval. Apply the
				// current cadence immediately without bypassing network backoff.
				checked, _ := time.Parse(time.RFC3339, old.CheckedAt)
				next = checked.Add(cloudPollInterval)
			}
			if now.Before(next) {
				continue
			}
		}
		build := &store.CloudBuild{ReleaseRunID: id, AppID: run.AppID, Version: strings.Join(releaseTagNames(releaseVersionsForRun(run, plan)), "、"), State: "pending", URL: githubWorkflowURL(plan.RemoteURL, ""), CheckedAt: now.Format(time.RFC3339), NextCheck: now.Add(cloudPollInterval).Format(time.RFC3339)}
		if app, _ := s.store.GetApp(run.AppID); app != nil {
			build.AppName = app.Name
		}
		queryCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		err = s.inspectCloudBuild(queryCtx, read, repo, run, plan, build, now)
		cancel()
		if err != nil {
			build.Errors = 1
			if old != nil {
				build.Errors = old.Errors + 1
			}
			// A lost connection never erases an already observed build failure.
			if old != nil && old.State == "failed" {
				build.State, build.Summary, build.URL, build.AlertKey = old.State, old.Summary, old.URL, old.AlertKey
			}
			if build.Errors >= 3 && build.State != "failed" {
				build.State = "unavailable"
				build.Summary = "暂时无法读取 GitHub 构建状态，请打开 GitHub 查看；私有仓库需要已登录且有 Actions 读取权限的 GitHub CLI。"
				build.AlertKey = id + ":unavailable"
			}
			delay := time.Duration(min(build.Errors, 10)) * time.Minute
			build.NextCheck = now.Add(delay).Format(time.RFC3339)
		}
		if ctx.Err() != nil {
			return
		}
		if err := s.store.SaveCloudBuild(build); err == nil && s.OnCloudBuildChange != nil &&
			(old == nil || old.State != build.State || old.AlertKey != build.AlertKey || old.Summary != build.Summary) {
			s.OnCloudBuildChange(build)
		}
	}
}

func (s *Service) inspectCloudBuild(ctx context.Context, read githubReader, repo string, run *store.ReleaseRun, plan *executionPlan, build *store.CloudBuild, now time.Time) error {
	tags := releaseTagNames(releaseVersionsForRun(run, plan))
	matched := []githubWorkflowRun{}
	created := parseReleaseTime(run.CreatedAt)
	// A repository may route each version-group Tag to a different workflow.
	// Automation.Workflow is a repository-level entry/link, not an exclusive
	// identity for every target. Match the frozen commit + exact release Tag +
	// push event + creation time, and aggregate all workflows for that release.
	for page := 1; page <= 3; page++ {
		var list githubRunList
		endpoint := "repos/" + repo + "/actions/runs?event=push&per_page=100&head_sha=" + url.QueryEscape(run.CommitSHA) + "&page=" + strconv.Itoa(page)
		if err := read(ctx, endpoint, &list); err != nil {
			return err
		}
		for _, candidate := range list.Runs {
			if candidate.HeadSHA != run.CommitSHA || candidate.Event != "push" || !contains(tags, candidate.HeadBranch) {
				continue
			}
			if !created.IsZero() && candidate.CreatedAt.Before(created.Add(-time.Minute)) {
				continue
			}
			matched = append(matched, candidate)
		}
		if list.Total <= page*100 {
			break
		}
		if page == 3 {
			return fmt.Errorf("workflow result limit")
		}
	}
	// A new run for the same workflow/tag supersedes its older failed run.
	latest := map[string]githubWorkflowRun{}
	for _, candidate := range matched {
		key := candidate.HeadBranch + ":" + candidate.Path
		if previous, ok := latest[key]; !ok || candidate.ID > previous.ID || candidate.ID == previous.ID && candidate.Attempt > previous.Attempt {
			latest[key] = candidate
		}
	}
	matched = nil
	for _, candidate := range latest {
		matched = append(matched, candidate)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
	recovered, recoveryErr := newerSuccessfulBuilds(ctx, read, repo, matched)
	seen := map[string]bool{}
	failures := []string{}
	summaries := []string{}
	pending := false
	for _, candidate := range matched {
		seen[candidate.HeadBranch] = true
		if len(failures) == 0 {
			build.URL = fmt.Sprintf("https://github.com/%s/actions/runs/%d", repo, candidate.ID)
		}
		if _, ok := recovered[candidate.ID]; ok {
			continue
		}
		if candidate.Status != "completed" && !cloudFailure(candidate.Conclusion) {
			pending = true
			continue
		}
		if candidate.Conclusion == "success" {
			continue
		}
		if candidate.Conclusion == "neutral" || candidate.Conclusion == "skipped" {
			pending = true
			continue
		}
		if !cloudFailure(candidate.Conclusion) {
			pending = true
			continue
		}
		failures = append(failures, fmt.Sprintf("%d:%d", candidate.ID, candidate.Attempt))
		if len(summaries) >= 3 {
			continue
		}
		build.URL = fmt.Sprintf("https://github.com/%s/actions/runs/%d", repo, candidate.ID)
		summary := candidate.Name + "：" + cloudConclusion(candidate.Conclusion)
		var jobs struct {
			Jobs []githubJob `json:"jobs"`
		}
		if err := read(ctx, fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d/jobs?per_page=100", repo, candidate.ID, max(candidate.Attempt, 1)), &jobs); err == nil {
			for _, job := range jobs.Jobs {
				if !cloudFailure(job.Conclusion) {
					continue
				}
				summary += " · " + job.Name
				for _, step := range job.Steps {
					if cloudFailure(step.Conclusion) {
						summary += " / " + step.Name
						break
					}
				}
				break
			}
		}
		summaries = append(summaries, summary)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		build.State = "failed"
		build.AlertKey = run.ID + ":" + strings.Join(failures, ",")
		build.Summary = strings.Join(summaries, "；")
		build.NextCheck = now.Add(cloudPollInterval).Format(time.RFC3339)
		return recoveryErr
	}
	if len(seen) < len(tags) {
		if !created.IsZero() && now.Sub(created) > 10*time.Minute {
			build.State = "not_started"
			build.AlertKey = run.ID + ":not_started"
			build.Summary = "尚未找到本次版本对应的构建，请检查 GitHub 工作流是否由该 Tag 触发。"
		}
		return nil
	}
	build.State = "running"
	if !pending && len(matched) > 0 && now.Sub(created) > 2*time.Minute {
		build.State = "succeeded"
		if len(recovered) > 0 {
			// Keep the original release/history intact: its failure is obsolete,
			// not a successful rerun of that old version.
			build.State = "superseded"
			build.Summary = "同一构建端的新版本已构建成功，旧版本失败提醒已自动清除。"
			var newestID int64
			for _, replacement := range recovered {
				newestID = max(newestID, replacement.ID)
			}
			build.URL = fmt.Sprintf("https://github.com/%s/actions/runs/%d", repo, newestID)
		}
	}
	return nil
}

// A newer release may have been published from another RunDock instance or
// directly on GitHub. Do not require it to exist in this instance's database.
// Only compare stable version tags within the same prefix AND workflow path.
var cloudVersionTag = regexp.MustCompile(`^(.*?)(v?(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*))$`)

func newerSuccessfulBuilds(ctx context.Context, read githubReader, repo string, matched []githubWorkflowRun) (map[int64]githubWorkflowRun, error) {
	recovered := map[int64]githubWorkflowRun{}
	failed := []githubWorkflowRun{}
	for _, run := range matched {
		if cloudFailure(run.Conclusion) && run.Path != "" && cloudVersionTag.MatchString(run.HeadBranch) {
			failed = append(failed, run)
		}
	}
	if len(failed) == 0 {
		return recovered, nil
	}
	since := failed[0].CreatedAt
	for _, run := range failed[1:] {
		if run.CreatedAt.Before(since) {
			since = run.CreatedAt
		}
	}
	latest := map[int64]githubWorkflowRun{}
	for page := 1; page <= 3; page++ {
		var list githubRunList
		endpoint := "repos/" + repo + "/actions/runs?event=push&per_page=100&page=" + strconv.Itoa(page)
		if !since.IsZero() {
			endpoint += "&created=" + url.QueryEscape(">="+since.UTC().Format(time.RFC3339))
		}
		if err := read(ctx, endpoint, &list); err != nil {
			return nil, err
		}
		for _, candidate := range list.Runs {
			newTag := cloudVersionTag.FindStringSubmatch(candidate.HeadBranch)
			if candidate.Event != "push" || newTag == nil {
				continue
			}
			newVersion, _ := parseSemver(newTag[2])
			for _, old := range failed {
				oldTag := cloudVersionTag.FindStringSubmatch(old.HeadBranch)
				oldVersion, _ := parseSemver(oldTag[2])
				if candidate.Path != old.Path || newTag[1] != oldTag[1] ||
					candidate.ID <= old.ID || !candidate.CreatedAt.After(old.CreatedAt) ||
					compareSemver(newVersion, oldVersion) <= 0 {
					continue
				}
				previous, exists := latest[old.ID]
				if exists {
					previousTag := cloudVersionTag.FindStringSubmatch(previous.HeadBranch)
					previousVersion, _ := parseSemver(previousTag[2])
					comparison := compareSemver(newVersion, previousVersion)
					if comparison < 0 || comparison == 0 && (candidate.ID < previous.ID || candidate.ID == previous.ID && candidate.Attempt <= previous.Attempt) {
						continue
					}
				}
				latest[old.ID] = candidate
			}
		}
		if list.Total <= page*100 {
			break
		}
		if page == 3 {
			// Incomplete evidence must never dismiss an actionable failure.
			return nil, fmt.Errorf("newer workflow result limit")
		}
	}
	for id, candidate := range latest {
		if candidate.Status == "completed" && candidate.Conclusion == "success" {
			recovered[id] = candidate
		}
	}
	return recovered, nil
}

func cloudFailure(value string) bool {
	return contains([]string{"failure", "timed_out", "cancelled", "action_required", "startup_failure", "stale"}, value)
}
func cloudConclusion(value string) string {
	switch value {
	case "timed_out":
		return "构建超时"
	case "cancelled":
		return "构建已取消"
	case "action_required":
		return "需要处理或批准"
	default:
		return "构建失败"
	}
}
func parseReleaseTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

var repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func githubRepository(remote string) string {
	link := githubWorkflowURL(remote, "")
	if link == "" {
		return ""
	}
	repo := strings.TrimSuffix(strings.TrimPrefix(link, "https://github.com/"), "/actions")
	if !repositoryName.MatchString(repo) {
		return ""
	}
	return repo
}

func readGitHub(ctx context.Context, endpoint string, out any) error {
	// Prefer existing GitHub CLI authentication; do not save or print tokens.
	gh, err := exec.LookPath("gh")
	if err != nil {
		if home, e := os.UserHomeDir(); e == nil {
			p := filepath.Join(home, ".local", "bin", "gh.exe")
			if _, e = os.Stat(p); e == nil {
				gh = p
			}
		}
	}
	if gh != "" {
		cmd := exec.CommandContext(ctx, gh, "api", "--hostname", "github.com", endpoint)
		cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1")
		cmd.WaitDelay = time.Second
		if data, e := cmd.Output(); e == nil {
			return json.Unmarshal(data, out)
		}
	}
	// Public repositories can be read without authentication. Network/auth/rate
	// errors remain 'unavailable', never a build failure, and are backed off.
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/"+endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "RunDock")
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("GitHub status %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(out)
}
