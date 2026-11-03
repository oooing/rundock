package launcher

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/probe"
)

func TestDiscoveredServiceScope(t *testing.T) {
	for _, tc := range []struct {
		name               string
		internal, explicit bool
		readiness          *startupReadiness
		want               string
	}{
		{"unknown backend remains required", false, false, nil, "required"},
		{"private worker", true, false, nil, "auxiliary"},
		{"explicit worker endpoint", true, true, nil, "required"},
		{"declared worker readiness", true, false, &startupReadiness{urls: map[int]string{8788: "http://localhost:8788/api/health"}}, "required"},
		{"other declared endpoint", false, false, &startupReadiness{urls: map[int]string{17653: "http://localhost:17653"}}, "auxiliary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := discoveredServiceScope(tc.readiness, 8788, tc.internal, tc.explicit); got != tc.want {
				t.Fatalf("scope=%s want %s", got, tc.want)
			}
		})
	}
}

func TestWorkerHotReloadRetiresOnlyAuxiliaryAndStillAlertsBackendFailure(t *testing.T) {
	server := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	}
	frontend, backend, worker := server(), server(), server()
	defer frontend.Close()
	defer backend.Close()
	defer worker.Close()
	port := func(s *httptest.Server) int { return s.Listener.Addr().(*net.TCPAddr).Port }
	frontPort, backPort, oldPort := port(frontend), port(backend), port(worker)
	l, rt := healthTestLauncher(t)
	rt.RootPID = os.Getpid()
	l.AuxiliaryServiceProcesses = func(map[int]uint64) map[int]bool { return map[int]bool{os.Getpid(): true} }
	hints := map[int]bool{frontPort: true, backPort: true}
	discover := func() { l.discoverServices(rt.AppID, rt, nil, nil, nil, hints, map[int]int{}, nil, 1, nil) }
	checks := map[string]*serviceHealthCheck{}
	tick := func(at time.Time) { l.recheckAndAggregate(rt.AppID, rt, nil, checks, at, nil) }
	discover()
	tick(time.Now())
	if rt.GetStatus() != app.StatusRunning {
		t.Fatalf("initial=%s", rt.GetStatus())
	}
	services, _ := l.Store.ListServicesByRun(rt.RunID)
	for _, s := range services {
		if s.Port == oldPort && s.StatusScope != "auxiliary" {
			t.Fatal("private worker counted as required")
		}
	}
	// A real new port appears while the old worker listener closes.
	replacement := server()
	defer replacement.Close()
	worker.Close()
	discover()
	tick(time.Now().Add(25 * time.Second))
	if l.Store.HasService(rt.RunID, oldPort) || !l.Store.HasService(rt.RunID, port(replacement)) {
		t.Fatal("reload did not replace auxiliary listener")
	}
	if rt.GetStatus() != app.StatusRunning {
		t.Fatal("retired worker degraded project")
	}
	// Failure of the stable backend must survive listener retirement and alert.
	backend.Close()
	discover()
	if !l.Store.HasService(rt.RunID, backPort) {
		t.Fatal("required backend was removed")
	}
	tick(time.Now().Add(50 * time.Second))
	tick(time.Now().Add(55 * time.Second))
	if rt.GetStatus() != app.StatusDegraded {
		t.Fatal("backend failure hidden", rt.GetStatus())
	}
	// Absence of any listeners may retire auxiliary rows, never required rows.
	l.retireAuxiliaryServices(rt.RunID, []probe.PortListener{}, nil)
	if !l.Store.HasService(rt.RunID, backPort) || !l.Store.HasService(rt.RunID, frontPort) {
		t.Fatal("required service history erased")
	}
}
