package store

import (
	"path/filepath"
	"testing"
)

func TestReorderAppsPersistsAndRollsBackOnInvalidID(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "reorder.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for order, id := range []string{"a", "b", "c"} {
		if err := s.CreateApp(&App{
			ID: id, Name: id, EntryScript: `C:\` + id + `.bat`, Cwd: `C:\`,
			LastStatus: "stopped", SortOrder: order,
		}); err != nil {
			t.Fatal(err)
		}
	}

	assertOrder := func(want []string) {
		t.Helper()
		apps, err := s.ListApps()
		if err != nil {
			t.Fatal(err)
		}
		if len(apps) != len(want) {
			t.Fatalf("app count = %d, want %d", len(apps), len(want))
		}
		for i, id := range want {
			if apps[i].ID != id || apps[i].SortOrder != i {
				t.Fatalf("apps[%d] = %s/%d, want %s/%d", i, apps[i].ID, apps[i].SortOrder, id, i)
			}
		}
	}

	if err := s.ReorderApps([]string{"c", "a", "b"}); err != nil {
		t.Fatal(err)
	}
	assertOrder([]string{"c", "a", "b"})

	if err := s.ReorderApps([]string{"b", "missing", "a"}); err == nil {
		t.Fatal("expected invalid id to fail")
	}
	assertOrder([]string{"c", "a", "b"})
}
