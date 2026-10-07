package publisher

import (
	"github.com/launcher-sidecar/internal/store"
	"path/filepath"
	"testing"
)

func TestRestartReservationExcludesCommands(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st)
	repo := t.TempDir()
	if !s.reserve(repo) {
		t.Fatal("reserve")
	}
	if _, err = s.BeginRestart(); err == nil {
		t.Fatal("restart allowed during build/check/release")
	}
	s.release(repo)
	resume, err := s.BeginRestart()
	if err != nil {
		t.Fatal(err)
	}
	if s.reserve(repo) || !s.repositoryBusy(repo) {
		t.Fatal("command raced restart")
	}
	if _, err = s.BeginRestart(); err == nil {
		t.Fatal("parallel restart allowed")
	}
	resume()
	if !s.reserve(repo) {
		t.Fatal("restart did not release reservation")
	}
	s.release(repo)
}
