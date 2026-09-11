package importer

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestDeclaredListenPortsSeparateDependenciesFromBindings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "start.cmd")
	err := os.WriteFile(p, []byte("set PROXY=http://127.0.0.1:7890\nset DATABASE_URL=http://localhost:5432\nset PORT=3000\nnode server --port 8100\nrem rundock:ready http://localhost:9100/health\n# rundock:open http://[::1]:9200/\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	got := DeclaredListenPorts(p)
	sort.Ints(got)
	if !reflect.DeepEqual(got, []int{3000, 8100, 9100, 9200}) {
		t.Fatal(got)
	}
	// Display hints retain dependencies; only launch prechecks use strict bindings.
	hints := scanPortHints(p)
	sort.Ints(hints)
	if !reflect.DeepEqual(hints, []int{3000, 5432, 7890, 8100, 9100, 9200}) {
		t.Fatal(hints)
	}
}
