package releaseconfig

import "testing"

func TestAutomationValidation(t *testing.T) {
	cfg := &Config{
		SchemaVersion: SchemaVersion,
		VersionGroups: []VersionGroup{{ID: "product", Name: "产品版本", VersionFiles: []VersionFile{}}},
		Targets:       []Target{},
		Automation: &Automation{
			Provider: AutomationGitHubActions, Workflow: "release.yml", Trigger: AutomationTriggerTag,
			ReleaseBranch: "v2", PublishesRelease: true,
		},
	}
	if err := validate(cfg); err != nil {
		t.Fatalf("valid automation: %v", err)
	}
	base := *cfg.Automation
	for _, mutate := range []func(*Automation){
		func(a *Automation) { a.Provider = "gitlab" },
		func(a *Automation) { a.Workflow = "../release.yml" },
		func(a *Automation) { a.Workflow = "release.txt" },
		func(a *Automation) { a.Trigger = "push" },
		func(a *Automation) { a.ReleaseBranch = "bad branch" },
	} {
		candidate := base
		mutate(&candidate)
		cfg.Automation = &candidate
		if err := validate(cfg); err == nil {
			t.Fatalf("invalid automation was accepted: %+v", candidate)
		}
	}
}
