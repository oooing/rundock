package publisher

import "github.com/launcher-sidecar/internal/store"

const (
	StrategyAuto   = "auto"
	StrategyManual = "manual"
	StrategyNode   = "node"
	StrategyTauri  = "tauri"
)

type Error struct {
	Code      string     `json:"code"`
	Message   string     `json:"message"`
	Preflight *Preflight `json:"preflight,omitempty"`
}

func (e *Error) Error() string { return e.Message }

type Issue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type FileChange struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Status  string `json:"status"`
	Tracked bool   `json:"tracked"`
	Staged  bool   `json:"staged"`
}

const (
	IntentSaveProgress = "save-progress"
	IntentFormal       = "formal"

	CategoryRecommend = "recommend"
	CategoryLocal     = "local"
	CategoryReview    = "review"
	CategorySensitive = "sensitive"

	DecisionInclude = "include"
	DecisionExclude = "exclude"

	CheckPending    = "pending"
	CheckRunning    = "running"
	CheckPassed     = "passed"
	CheckFailed     = "failed"
	CheckBlocked    = "blocked"
	CheckCancelled  = "cancelled"
	CheckUnverified = "unverified"
	CheckStale      = "stale"
	CheckSkipped    = "skipped"

	VersionModeUnchanged = "unchanged"
	BuildModeNone        = "none"
)

type FileClassification struct {
	Path               string   `json:"path"`
	OldPath            string   `json:"oldPath,omitempty"`
	Status             string   `json:"status"`
	Tracked            bool     `json:"tracked"`
	Category           string   `json:"category"`
	Reasons            []string `json:"reasons"`
	Sources            []string `json:"sources"`
	RuleIDs            []string `json:"ruleIds,omitempty"`
	SelectedDefault    bool     `json:"selectedDefault"`
	BaselineKept       bool     `json:"baselineKept"`
	Group              string   `json:"group"`
	SensitiveKind      string   `json:"sensitiveKind,omitempty"`
	ContentFingerprint string   `json:"contentFingerprint,omitempty"`
}

type ManualDecision struct {
	Path               string `json:"path"`
	Decision           string `json:"decision"`
	Reason             string `json:"reason,omitempty"`
	ContentFingerprint string `json:"contentFingerprint,omitempty"`
}

type SensitiveException struct {
	Path               string `json:"path"`
	Reason             string `json:"reason"`
	ContentFingerprint string `json:"contentFingerprint"`
	FindingFingerprint string `json:"findingFingerprint"`
}

type SensitiveFinding struct {
	Column             int    `json:"column,omitempty"`
	Fingerprint        string `json:"fingerprint"`
	ContentFingerprint string `json:"contentFingerprint"`
	Path               string `json:"path"`
	Kind               string `json:"kind"`
	Reason             string `json:"reason"`
	Line               int    `json:"line,omitempty"`
	Redacted           string `json:"redacted"`
}

type DependencyFinding struct {
	Path       string `json:"path"`
	Reference  string `json:"reference"`
	Missing    string `json:"missing"`
	Reason     string `json:"reason"`
	Blocked    bool   `json:"blocked"`
	Suggestion string `json:"suggestion,omitempty"`
}

type CheckResult struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Log        string `json:"log,omitempty"`
	Required   bool   `json:"required"`
	DurationMS int64  `json:"durationMs,omitempty"`
}

type CandidateRequest struct {
	SkipChecks          bool                           `json:"skipChecks,omitempty"`
	StatusFingerprint   string                         `json:"statusFingerprint"`
	SelectedPaths       []string                       `json:"selectedPaths"`
	ManualDecisions     []ManualDecision               `json:"manualDecisions"`
	Intent              string                         `json:"intent"`
	TargetVersion       string                         `json:"targetVersion"`
	Versions            []ReleaseVersionInput          `json:"versions"`
	VersionMode         string                         `json:"versionMode"`
	CreateTag           *bool                          `json:"createTag"`
	PushRemote          *bool                          `json:"pushRemote"`
	BuildMode           string                         `json:"buildMode"`
	SelectedTargets     []store.ReleaseTargetSelection `json:"selectedTargets"`
	SensitiveExceptions []SensitiveException           `json:"sensitiveExceptions,omitempty"`
}

type CandidateView struct {
	ChecksSkipped      bool                 `json:"checksSkipped,omitempty"`
	ID                 string               `json:"id"`
	Fingerprint        string               `json:"fingerprint"`
	Status             string               `json:"status"`
	Intent             string               `json:"intent"`
	Classifications    []FileClassification `json:"classifications"`
	SelectedPaths      []string             `json:"selectedPaths"`
	SensitiveFindings  []SensitiveFinding   `json:"sensitiveFindings"`
	DependencyFindings []DependencyFinding  `json:"dependencyFindings"`
	CheckResults       []CheckResult        `json:"checkResults"`
	Warnings           []string             `json:"warnings"`
	Accepted           bool                 `json:"accepted"`
	CanFormal          bool                 `json:"canFormal"`
	CanSaveProgress    bool                 `json:"canSaveProgress"`
	TreeHash           string               `json:"treeHash,omitempty"`
	MutationDetected   bool                 `json:"mutationDetected"`
}

type CommittedFileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type Preflight struct {
	remoteTags        map[string]string
	RepoRoot          string                `json:"repoRoot"`
	Branch            string                `json:"branch"`
	HeadSHA           string                `json:"headSha"`
	RemoteName        string                `json:"remoteName"`
	RemoteURL         string                `json:"remoteUrl"`
	Remotes           []string              `json:"remotes"`
	LatestTag         string                `json:"latestTag"`
	LatestGroupTags   map[string]string     `json:"latestGroupTags"`
	CommitsSinceTags  map[string]int        `json:"commitsSinceTags"` // missing key = comparison unavailable; empty tag = first release
	SuggestedVersion  string                `json:"suggestedVersion"`
	SuggestedVersions map[string]string     `json:"suggestedVersions"`
	VersionStrategy   string                `json:"versionStrategy"`
	VersionFiles      []string              `json:"versionFiles"`
	CurrentVersions   map[string]string     `json:"currentVersions"`
	Changes           []FileChange          `json:"changes"`
	AheadCount        int                   `json:"aheadCount"`
	UnpushedChanges   []CommittedFileChange `json:"unpushedChanges"`
	BlockingIssues    []Issue               `json:"blockingIssues"`
	CanRelease        bool                  `json:"canRelease"`
	RemoteChecked     bool                  `json:"remoteChecked"`
	StatusFingerprint string                `json:"statusFingerprint"`
	Profile           *store.ReleaseProfile `json:"profile"`
	Classifications   []FileClassification  `json:"classifications,omitempty"`
}

type CreateRequest struct {
	SkipChecks               bool                           `json:"skipChecks,omitempty"`
	TargetVersion            string                         `json:"targetVersion"`
	Versions                 []ReleaseVersionInput          `json:"versions"`
	SelectedPaths            []string                       `json:"selectedPaths"`
	CommitMessage            string                         `json:"commitMessage"`
	StatusFingerprint        string                         `json:"statusFingerprint"`
	CreateTag                *bool                          `json:"createTag"`
	PushRemote               *bool                          `json:"pushRemote"`
	VersionMode              string                         `json:"versionMode"`
	BuildMode                string                         `json:"buildMode"`
	SelectedTargets          []store.ReleaseTargetSelection `json:"selectedTargets"`
	ExternalActionsConfirmed bool                           `json:"externalActionsConfirmed"`
	ReleaseNotes             string                         `json:"releaseNotes"`
	ReleaseNotesConfirmed    bool                           `json:"releaseNotesConfirmed"`
	Intent                   string                         `json:"intent"`
	CandidateID              string                         `json:"candidateId"`
	ManualDecisions          []ManualDecision               `json:"manualDecisions"`
	SensitiveExceptions      []SensitiveException           `json:"sensitiveExceptions,omitempty"`
}

type NotesDraftRequest struct {
	StatusFingerprint string                         `json:"statusFingerprint"`
	SelectedPaths     []string                       `json:"selectedPaths"`
	SelectedTargets   []store.ReleaseTargetSelection `json:"selectedTargets"`
}

type NotesDraft struct {
	Text              string `json:"text"`
	BaseTag           string `json:"baseTag"`
	CommitCount       int    `json:"commitCount"`
	ChangeCount       int    `json:"changeCount"`
	SourceFingerprint string `json:"sourceFingerprint"`
}

type ReleaseVersionInput struct {
	VersionGroupID string `json:"versionGroupId"`
	TargetVersion  string `json:"targetVersion"`
}

type RetryRequest struct {
	ExternalActionsConfirmed bool `json:"externalActionsConfirmed"`
}

type RunView struct {
	Deliveries                []*store.ReleaseDelivery  `json:"deliveries"`
	Run                       *store.ReleaseRun         `json:"run"`
	Targets                   []*store.ReleaseTargetRun `json:"targets"`
	Artifacts                 []*store.ReleaseArtifact  `json:"artifacts"`
	Logs                      []*store.ReleaseLog       `json:"logs"`
	Automation                *AutomationHandoff        `json:"automation,omitempty"`
	CloudBuild                *store.CloudBuild         `json:"cloudBuild,omitempty"`
	RetryConfirmationRequired bool                      `json:"retryConfirmationRequired"`
	RetryConfirmationTargets  []string                  `json:"retryConfirmationTargets,omitempty"`
}

type AutomationHandoff struct {
	Provider string `json:"provider"`
	Workflow string `json:"workflow"`
	URL      string `json:"url,omitempty"`
	State    string `json:"state"`
	Message  string `json:"message"`
}
