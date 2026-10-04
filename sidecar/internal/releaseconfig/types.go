package releaseconfig

const (
	SchemaVersion           = 1
	ManifestPath            = ".launcher/release.yaml"
	RunnerLocal             = "local"
	RunnerGitPush           = "git-push"
	AutomationGitHubActions = "github-actions"
	AutomationTriggerTag    = "tag"

	SourceDetected = "detected"
	SourceFile     = "file"

	RuleRecommend = "recommend"
	RuleLocal     = "local"
	RuleReview    = "review"
	RuleSensitive = "sensitive"
)

// Config describes how one repository can be versioned, built, packaged and
// delivered. Runtime metadata (Source, RepoRoot, ConfigPath, Confidence and
// Warnings) is returned to the UI but is not persisted in the manifest.
type Config struct {
	SchemaVersion int            `json:"schemaVersion"`
	Source        string         `json:"source,omitempty"`
	RepoRoot      string         `json:"repoRoot,omitempty"`
	ConfigPath    string         `json:"configPath,omitempty"`
	Confidence    float64        `json:"confidence"`
	VersionGroups []VersionGroup `json:"versionGroups"`
	Targets       []Target       `json:"targets"`
	Automation    *Automation    `json:"automation,omitempty"`
	FileRules     []FileRule     `json:"fileRules,omitempty"`
	CheckProfiles []CheckProfile `json:"checkProfiles,omitempty"`
	Warnings      []string       `json:"warnings"`
}

// FileRule classifies repository paths for release selection. Patterns are
// repository-relative globs; they never rewrite .gitignore or Git tracking.
type FileRule struct {
	ID      string `json:"id"`
	Pattern string `json:"pattern"`
	Kind    string `json:"kind"`
	Reason  string `json:"reason,omitempty"`
}

// CheckProfile is an explicitly confirmed command run against an isolated
// candidate. Commands are never auto-discovered from hooks.
type CheckProfile struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Command        string   `json:"command"`
	WorkingDir     string   `json:"workingDir,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	Required       bool     `json:"required"`
	OS             []string `json:"os,omitempty"`
	TargetKinds    []string `json:"targetKinds,omitempty"`
}

// Automation describes an external release pipeline. It is deliberately
// declarative: legacy targets hand off through Tag push; dispatch targets use
// the credential provider with a frozen source and operation identity.
type Automation struct {
	Account          string `json:"account,omitempty"`
	Provider         string `json:"provider"`
	Workflow         string `json:"workflow"`
	Trigger          string `json:"trigger"`
	ReleaseBranch    string `json:"releaseBranch"`
	PublishesRelease bool   `json:"publishesRelease"`
}

// VersionGroup lets several targets share one release version while other
// targets in the same repository can advance independently.
type VersionGroup struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	TagPrefix      string        `json:"tagPrefix,omitempty"`
	CurrentVersion string        `json:"currentVersion,omitempty"`
	VersionFiles   []VersionFile `json:"versionFiles"`
}

// VersionFile identifies one version location updated for selected targets.
// JSONPointer is used by JSON/Tauri files. npm-lock, cargo-lock, Gradle and
// Cargo formats identify their version location from the file structure.
type VersionFile struct {
	Path        string `json:"path"`
	Format      string `json:"format"`
	JSONPointer string `json:"jsonPointer,omitempty"`
}

type Target struct {
	Delivery      *Delivery      `json:"delivery,omitempty"`
	Timeouts      map[string]int `json:"timeouts,omitempty"`
	ArtifactRules []ArtifactRule `json:"artifactRules,omitempty"`
	Verification  []Verification `json:"verification,omitempty"`
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Kind          string         `json:"kind"`
	VersionGroup  string         `json:"versionGroup"`
	WorkingDir    string         `json:"workingDir"`
	Runner        Runner         `json:"runner"`
	Enabled       bool           `json:"enabled"`
	Detected      bool           `json:"detected"`
	Confidence    float64        `json:"confidence"`
	Steps         Steps          `json:"steps"`
	Artifacts     []string       `json:"artifacts"`
}

type Runner struct {
	Type string   `json:"type"`
	OS   []string `json:"os"`
}

// Steps are intentionally plain commands. The executor freezes the selected
// target definitions into each release run before it starts, so later config
// edits cannot alter a retry that is already in progress.
type Steps struct {
	Check   string `json:"check,omitempty"`
	Build   string `json:"build,omitempty"`
	Package string `json:"package,omitempty"`
	Publish string `json:"publish,omitempty"`
	Deploy  string `json:"deploy,omitempty"`
}

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }
