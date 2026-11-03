package store

import (
	"github.com/launcher-sidecar/internal/accesslink"
	"path/filepath"
	"testing"
)

func TestAccessLinksSurviveUpdateAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "links.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{ID: "blog", Name: "Blog", EntryScript: "start.cmd", AdapterType: "batch", LastStatus: "stopped", AccessLinks: []accesslink.Link{{Name: "首页", URL: "http://localhost:3000/"}, {Name: "管理后台", URL: "http://localhost:3000/admin"}}}
	if err = s.CreateApp(a); err != nil {
		t.Fatal(err)
	}
	a, err = s.GetApp("blog")
	if err != nil {
		t.Fatal(err)
	}
	a.Name = "Renamed"
	if err = s.UpdateApp(a); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	apps, err := s.ListApps()
	if err != nil || len(apps) != 1 {
		t.Fatalf("%v %v", apps, err)
	}
	if len(apps[0].AccessLinks) != 2 || apps[0].AccessLinks[1].URL != "http://localhost:3000/admin" {
		t.Fatalf("lost links: %+v", apps[0])
	}
	apps[0].AccessLinks = []accesslink.Link{}
	if err = s.UpdateApp(apps[0]); err != nil {
		t.Fatal(err)
	}
	a, err = s.GetApp("blog")
	if err != nil || len(a.AccessLinks) != 0 {
		t.Fatalf("clear failed: %+v %v", a, err)
	}
}
