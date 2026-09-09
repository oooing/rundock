package launcher

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/store"
)

func healthTestLauncher(t *testing.T) (*Launcher, *app.Runtime) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	rt := &app.Runtime{AppID: "app", RunID: "run", Status: app.StatusStarting}
	if err := s.CreateApp(&store.App{ID: rt.AppID, Name: "Health test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRun(&store.AppRun{ID: rt.RunID, AppID: rt.AppID, Status: rt.Status, StartedAt: time.Now().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	return &Launcher{Store: s, Manager: app.NewManager(s)}, rt
}

func addHealthTestService(t *testing.T, l *Launcher, rt *app.Runtime, id string, server *httptest.Server) {
	t.Helper()
	err := l.Store.UpsertService(&store.AppService{ID: id, AppID: rt.AppID, AppRunID: rt.RunID,
		Port: server.Listener.Addr().(*net.TCPAddr).Port, URL: server.URL, Health: "unknown",
		Role: store.RoleBackend, RoleSource: store.RoleSourceManual, DetectedAt: time.Now().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
}

func healthTestSnapshot(t *testing.T, l *Launcher, rt *app.Runtime, id string) *store.AppService {
	t.Helper()
	services, err := l.Store.ListServicesByRun(rt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range services {
		if svc.ID == id {
			return svc
		}
	}
	t.Fatal("service missing", id)
	return nil
}

func TestHealthMonitorRechecksDebouncesAndRecovers(t *testing.T) {
	var status atomic.Int32
	var requests atomic.Int32
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(int(status.Load()))
	}))
	defer server.Close()
	l, rt := healthTestLauncher(t)
	addHealthTestService(t, l, rt, "svc", server)
	checks := map[string]*serviceHealthCheck{}
	now := time.Now()
	tick := func(at time.Time) { l.recheckAndAggregate(rt.AppID, rt, nil, checks, at, nil) }
	assertState := func(want string) {
		t.Helper()
		if rt.GetStatus() != want {
			t.Fatalf("state=%s, want %s", rt.GetStatus(), want)
		}
	}
	due := func() { tick(checks["svc"].nextCheck.Add(time.Millisecond)) }

	tick(now)
	assertState(app.StatusRunning)
	first := healthTestSnapshot(t, l, rt, "svc")
	initialRequests := requests.Load()
	tick(now.Add(3 * time.Second))
	if requests.Load() != initialRequests {
		t.Fatal("healthy service probed before interval")
	}
	if got := healthTestSnapshot(t, l, rt, "svc"); got.LastChecked != first.LastChecked {
		t.Fatal("timestamp changed without a probe")
	}

	status.Store(http.StatusServiceUnavailable)
	due()
	assertState(app.StatusRunning) // Single transient failure is tolerated.
	firstFailure := healthTestSnapshot(t, l, rt, "svc")
	if firstFailure.LastChecked == first.LastChecked {
		t.Fatal("healthy service was never rechecked")
	}
	requestsAfterFailure := requests.Load()
	tick(checks["svc"].nextCheck.Add(-time.Second))
	if requests.Load() != requestsAfterFailure {
		t.Fatal("retry throttling did not apply")
	}
	due()
	assertState(app.StatusDegraded)
	if got := healthTestSnapshot(t, l, rt, "svc"); got.Health != "unhealthy" {
		t.Fatal("confirmed failure not persisted")
	}
	status.Store(http.StatusOK)
	due()
	assertState(app.StatusRunning)
	if got := healthTestSnapshot(t, l, rt, "svc"); got.Health != "healthy" {
		t.Fatal("recovery not persisted")
	}

	// Success resets the failure streak: separated single failures never combine.
	status.Store(http.StatusServiceUnavailable)
	due()
	assertState(app.StatusRunning)
	status.Store(http.StatusOK)
	due()
	assertState(app.StatusRunning)
	status.Store(http.StatusServiceUnavailable)
	due()
	assertState(app.StatusRunning)
}

func TestHealthMonitorAggregatesServicesAndDetectsClosedPort(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(int(status.Load())) }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer second.Close()
	l, rt := healthTestLauncher(t)
	addHealthTestService(t, l, rt, "first", first)
	addHealthTestService(t, l, rt, "second", second)
	checks := map[string]*serviceHealthCheck{}
	now := time.Now()
	l.recheckAndAggregate(rt.AppID, rt, nil, checks, now, nil)
	if rt.GetStatus() != app.StatusDegraded {
		t.Fatal("initial unhealthy service must not count as ready")
	}
	status.Store(http.StatusOK)
	l.recheckAndAggregate(rt.AppID, rt, nil, checks, now.Add(5*time.Second), nil)
	if rt.GetStatus() != app.StatusRunning {
		t.Fatal("both healthy services should recover")
	}
	second.Close()
	l.recheckAndAggregate(rt.AppID, rt, nil, checks, now.Add(25*time.Second), nil)
	l.recheckAndAggregate(rt.AppID, rt, nil, checks, now.Add(30*time.Second), nil)
	if rt.GetStatus() != app.StatusDegraded {
		t.Fatal("a closed service port must degrade the whole project")
	}
	if healthTestSnapshot(t, l, rt, "first").Health != "healthy" {
		t.Fatal("failure leaked to another service")
	}
}

func TestHealthMonitorDoesNotResurrectStoppedRun(t *testing.T) {
	l, rt := healthTestLauncher(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rt.SetStatus(app.StatusStopping)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	addHealthTestService(t, l, rt, "svc", server)
	l.recheckAndAggregate(rt.AppID, rt, nil, map[string]*serviceHealthCheck{}, time.Now(), nil)
	if rt.GetStatus() != app.StatusStopping {
		t.Fatal("health probe resurrected a stopping project")
	}
	if got := healthTestSnapshot(t, l, rt, "svc"); got.Health != "unknown" || got.LastChecked != "" {
		t.Fatal("late probe result persisted during stop")
	}
	for _, state := range []string{app.StatusStopped, app.StatusFailed, app.StatusStopping} {
		rt.SetStatus(state)
		l.recheckAndAggregate(rt.AppID, rt, nil, map[string]*serviceHealthCheck{}, time.Now(), nil)
		if rt.GetStatus() != state {
			t.Fatal("terminal state overwritten", state)
		}
	}
}
