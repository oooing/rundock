package publisher

import "github.com/launcher-sidecar/internal/store"

const (
	StrategyAuto   = "auto"
	StrategyManual = "manual"
	StrategyNode   = "node"
	StrategyTauri  = "tauri"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type Issue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type FileChange struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Tracked bool   `json:"tracked"`
	Staged  bool   `json:"staged"`
}

type CommittedFileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type Preflight struct {
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
}

type CreateRequest struct {
	TargetVersion            string                         `json:"targetVersion"`
	Versions                 []ReleaseVersionInput          `json:"versions"`
	SelectedPaths            []string                       `json:"selectedPaths"`
	CommitMessage            string                         `json:"commitMessage"`
	StatusFingerprint        string                         `json:"statusFingerprint"`
	CreateTag                *bool                          `json:"createTag"`
	PushRemote               *bool                          `json:"pushRemote"`
	VersionMode              string                         `json:"versionMode"`
	SelectedTargets          []store.ReleaseTargetSelection `json:"selectedTargets"`
	ExternalActionsConfirmed bool                           `json:"externalActionsConfirmed"`
	ReleaseNotes             string                         `json:"releaseNotes"`
	ReleaseNotesConfirmed    bool                           `json:"releaseNotesConfirmed"`
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
	Run        *store.ReleaseRun         `json:"run"`
	Targets    []*store.ReleaseTargetRun `json:"targets"`
	Artifacts  []*store.ReleaseArtifact  `json:"artifacts"`
	Logs       []*store.ReleaseLog       `json:"logs"`
	Automation *AutomationHandoff        `json:"automation,omitempty"`
}

type AutomationHandoff struct {
	Provider string `json:"provider"`
	Workflow string `json:"workflow"`
	URL      string `json:"url,omitempty"`
	State    string `json:"state"`
	Message  string `json:"message"`
}
