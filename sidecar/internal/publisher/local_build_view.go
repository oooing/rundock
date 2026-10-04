package publisher

import (
	"context"
	"time"

	"github.com/launcher-sidecar/internal/store"
)

type LocalBuildView struct {
	Run             *store.ReleaseRun         `json:"run"`
	Targets         []*store.ReleaseTargetRun `json:"targets"`
	Logs            []*store.ReleaseLog       `json:"logs"`
	Artifacts       []LocalBuildArtifact      `json:"artifacts"`
	OutputDirectory string                    `json:"outputDirectory"`
	Help            string                    `json:"help"`
	SourceSHA256    string                    `json:"sourceSha256,omitempty"`
}

func (s *Service) GetLocalBuild(runID string, since int64) (*LocalBuildView, error) {
	run, err := s.store.GetReleaseRun(runID)
	if err != nil {
		return nil, err
	}
	plan, err := parseLocalBuildPlan(run)
	if err != nil {
		return nil, err
	}
	targets, err := s.store.ReleaseTargetRuns(runID)
	if err != nil {
		return nil, err
	}
	var logs []*store.ReleaseLog
	if run.Status == "queued" || run.Status == "running" || run.Status == "pending" {
		logs, err = s.store.ReleaseLogs(runID, since, 500)
	} else {
		// Terminal tasks are not polled again: prefer the bounded error tail
		// over the oldest unseen page so the final failure remains visible.
		logs, err = s.store.LocalBuildLogTail(runID, since, 500)
	}
	if err != nil {
		return nil, err
	}
	if logs == nil {
		logs = []*store.ReleaseLog{}
	}
	verifyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	artifacts, err := s.LocalBuildArtifacts(verifyCtx, runID)
	if err != nil {
		return nil, err
	}
	directory, _ := s.LocalBuildOutputDirectory(runID)
	return &LocalBuildView{Run: run, Targets: targets, Logs: logs, Artifacts: artifacts, OutputDirectory: directory,
		Help: localBuildHelp, SourceSHA256: plan.SourceSHA256}, nil
}
