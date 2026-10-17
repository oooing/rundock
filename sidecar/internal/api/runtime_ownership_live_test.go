package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
)

// Opt-in, read-only acceptance using the actual dev server's project config
// and Windows process/listener identities. Writes only a disposable test DB.
func TestLiveProjectRuntimeOwnership(t *testing.T) {
	if os.Getenv("RUNDOCK_RUNTIME_LIVE") != "1" {
		t.Skip("opt-in live check")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get("http://127.0.0.1:17655/api/apps")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	var projects []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Cwd         string `json:"cwd"`
		EntryScript string `json:"entryScript"`
		PID         int    `json:"pid"`
		RunID       string `json:"runId"`
		Status      string `json:"status"`
	}
	if err = json.NewDecoder(response.Body).Decode(&projects); err != nil {
		t.Fatal(err)
	}
	before, err := recovery.ReadRuntimeSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	byPID := map[int]recovery.Process{}
	for _, p := range before.Processes {
		byPID[p.PID] = p
	}
	backendPID := 0
	protected := map[int]recovery.Process{}
	for _, l := range before.Listeners {
		switch l.Port {
		case 17655:
			backendPID = l.PID
			fallthrough
		case 17656, 11784, 11785, 17618:
			protected[l.PID] = byPID[l.PID]
		}
	}
	if backendPID == 0 || len(protected) != 5 {
		t.Fatalf("expected five existing service processes, got %d", len(protected))
	}
	// Vite may open an additional internal listener (for example 4318).
	// Include it only when its PID is the same verified web/extension process.
	duckOwners := map[int]bool{}
	for _, l := range before.Listeners {
		if l.Port == 11784 || l.Port == 11785 {
			duckOwners[l.PID] = true
		}
	}
	duckPortSet := map[int]bool{}
	for _, l := range before.Listeners {
		if duckOwners[l.PID] {
			duckPortSet[l.Port] = true
		}
	}
	var duckPorts []int
	for port := range duckPortSet {
		duckPorts = append(duckPorts, port)
	}
	sort.Ints(duckPorts)
	db, err := store.Open(filepath.Join(t.TempDir(), "ownership.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Server{Store: db, Manager: app.NewManager(db), webEntries: newWebEntryCache()}
	var dock, duck *store.App
	for _, p := range projects {
		a := &store.App{ID: p.ID, Name: p.Name, Cwd: p.Cwd, EntryScript: p.EntryScript}
		if err := db.CreateApp(a); err != nil {
			t.Fatal(err)
		}
		if p.ID == "2f1a038659086733" {
			dock = a
		}
		if p.ID == "20ce75a51ae95044" {
			duck = a
		}
		if p.RunID != "" && p.Status == "running" {
			identity := byPID[p.PID]
			if identity.Created == "" {
				t.Fatal("missing managed root identity")
			}
			s.Manager.Registry.Set(p.ID, &app.Runtime{AppID: p.ID, RunID: p.RunID, PID: p.PID, RootPID: p.PID, RootCreated: identity.Created, Status: app.StatusRunning})
		}
	}
	if dock == nil || duck == nil {
		t.Fatal("required dev projects missing")
	}
	snapshot, err := s.readProjectRuntimeSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	// This probe is not the launcher. Use the existing backend's verified PID.
	snapshot.LauncherPID = backendPID
	s.runtimeMonitor = &runtimeMonitor{snapshot: snapshot, checked: time.Now()}
	for _, tc := range []struct {
		project *store.App
		ports   []int
	}{{dock, []int{17655, 17656}}, {duck, duckPorts}} {
		result := recovery.InspectRuntime(tc.project, nil, snapshot)
		var ports []int
		for _, service := range result.Services {
			ports = append(ports, service.Port)
		}
		t.Logf("%s: %s ports=%v", tc.project.Name, result.State, ports)
		if result.State != "running" || !reflect.DeepEqual(ports, tc.ports) {
			t.Errorf("wrong project ownership: got %v want %v", ports, tc.ports)
		}
	}
	row := appView(dock, s)
	var cardPorts []int
	for _, service := range row["services"].([]*store.AppService) {
		cardPorts = append(cardPorts, service.Port)
	}
	if !reflect.DeepEqual(cardPorts, []int{17655, 17656}) {
		t.Errorf("card ports: %v", cardPorts)
	}
	for pid, p := range protected {
		if p.Created == "" || !recovery.SameProcess(p) {
			t.Errorf("existing process changed: %d", pid)
		}
	}
	for _, raw := range []string{"http://127.0.0.1:11784/", "http://127.0.0.1:11785/@vite/client", "http://127.0.0.1:17656/"} {
		r, err := client.Get(raw)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Errorf("%s: HTTP %d", raw, r.StatusCode)
		}
	}
	t.Log("All five existing listener process identities unchanged; DuckyPlay and RunDock HTTP checks passed.")
}
