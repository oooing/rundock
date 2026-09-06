package api

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/store"
)

func TestMoveProjectGroupsPersistsWithoutChangingLaunchConfig(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "groups.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	project := &store.App{ID: "project", Name: "项目", EntryScript: `C:\test\start.bat`, Cwd: `C:\test`, AdapterType: "batch", Cmd: "cmd.exe", Args: []string{"/c", "start.bat"}, Env: map[string]string{"KEEP": "yes"}, Tags: []string{}, PortHints: []int{8080}, LastStatus: "stopped", Confirmed: true, SortOrder: 7}
	if err := st.CreateApp(project); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := st.CreateGroup(&store.Group{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	router := New(st, logbus.NewHub(), adapter.NewRegistry()).Router()
	for _, id := range []string{"a", "b", ""} {
		res := requestAPI(t, router, http.MethodPatch, "/api/apps/project", []byte(`{"groupId":"`+id+`"}`))
		if res.Code != http.StatusOK {
			t.Fatalf("move to %q: %d %s", id, res.Code, res.Body.String())
		}
		saved, err := st.GetApp("project")
		if err != nil {
			t.Fatal(err)
		}
		if id == "" {
			if saved.GroupID != nil {
				t.Fatalf("ungrouped must persist NULL: %+v", saved.GroupID)
			}
		} else if saved.GroupID == nil || *saved.GroupID != id {
			t.Fatalf("wrong group: %+v", saved.GroupID)
		}
		if saved.Cmd != project.Cmd || saved.Cwd != project.Cwd || saved.EntryScript != project.EntryScript || saved.SortOrder != 7 || !saved.Confirmed || saved.Env["KEEP"] != "yes" {
			t.Fatalf("move changed launch config: %+v", saved)
		}
	}
	res := requestAPI(t, router, http.MethodPatch, "/api/apps/project", []byte(`{"groupId":"deleted"}`))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("missing group accepted: %d", res.Code)
	}
	saved, _ := st.GetApp("project")
	if saved.GroupID != nil {
		t.Fatal("failed move changed group")
	}
	requestAPI(t, router, http.MethodPatch, "/api/apps/project", []byte(`{"groupId":"a"}`))
	res = requestAPI(t, router, http.MethodPatch, "/api/apps/project", []byte(`{"name":"renamed"}`))
	if res.Code != http.StatusOK {
		t.Fatal(res.Body.String())
	}
	saved, _ = st.GetApp("project")
	if saved.GroupID == nil || *saved.GroupID != "a" {
		t.Fatal("omitted group must remain unchanged")
	}
}
