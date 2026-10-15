package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestReleasePreparationRecoveryAndConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preparations.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateApp(&App{ID: "app", Name: "app", Cwd: t.TempDir(), EntryScript: "start.bat", LastStatus: "stopped"}); err != nil {
		t.Fatal(err)
	}
	p := ReleasePreparation{ID: "preparation-1", AppID: "app", Status: "checking", CandidateID: "cand-1", Draft: json.RawMessage(`{"request":{"selectedPaths":["test.py"]}}`)}
	if err = s.SaveReleasePreparation(&p); err != nil {
		t.Fatal(err)
	}
	stale := p
	p.Status = "submitting"
	if err = s.SaveReleasePreparation(&p); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveReleasePreparation(&stale); !errors.Is(err, ErrPreparationConflict) {
		t.Fatalf("late write: %v", err)
	}
	alien := p
	alien.AppID = "another"
	if err = s.SaveReleasePreparation(&alien); !errors.Is(err, ErrPreparationConflict) {
		t.Fatalf("cross app write: %v", err)
	}
	if err = s.RejectReleasePreparation("app", "different"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListReleasePreparations("app")
	if err != nil || len(rows) != 1 || rows[0].Status != "submitting" {
		t.Fatalf("incorrect match: %+v %v", rows, err)
	}
	if err = s.RejectReleasePreparation("app", "cand-1"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err = s.ListReleasePreparations("app")
	if err != nil || len(rows) != 1 || rows[0].Revision != 3 || rows[0].Status != "error" || string(rows[0].Draft) != string(p.Draft) {
		t.Fatalf("lost recovery: %+v %v", rows, err)
	}
}
