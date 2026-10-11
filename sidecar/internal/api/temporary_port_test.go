package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/portoverride"
	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
)

func temporaryFixture(t *testing.T) (*Server, *store.App, net.Listener) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, logbus.NewHub(), adapter.NewRegistry())
	t.Cleanup(func() { s.Diagnostics.Close(); st.Close() })
	owner, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	port := owner.Addr().(*net.TCPAddr).Port
	root := t.TempDir()
	entry := filepath.Join(root, "package.json")
	data, _ := json.Marshal(map[string]any{"scripts": map[string]string{"dev": "vite"}, "rundock": map[string]any{"temporaryPort": map[string]any{"version": 1, "adapter": "vite", "script": "dev", "port": port, "host": "127.0.0.1", "interfaces": "same-origin"}}})
	if err := os.WriteFile(entry, data, 0600); err != nil {
		t.Fatal(err)
	}
	hash, _ := hashOf(entry)
	a := &store.App{ID: "target", Name: "B", Cwd: root, EntryScript: entry, AdapterType: "npm", Cmd: "npm.cmd", Args: []string{"run", "dev"}, Env: map[string]string{}, ScriptHash: hash, HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port)}
	if err := st.CreateApp(a); err != nil {
		t.Fatal(err)
	}
	s.runtimeMonitor = &runtimeMonitor{gate: make(chan struct{}, 1), read: func() (recovery.RuntimeSnapshot, error) {
		return recovery.RuntimeSnapshot{Listeners: []probe.PortListener{{PID: 900000, Port: port, Addr: owner.Addr().String()}}}, nil
	}}
	return s, a, owner
}

func requestTemporary(t *testing.T, s *Server, id, endpoint, token string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"confirmationToken":%q}`, token)
	r := httptest.NewRequest("POST", "http://localhost/api/apps/"+id+"/"+endpoint, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, r)
	return w
}

func TestTemporaryOfferAndTakenPortNeverStopOwnerOrMutateConfig(t *testing.T) {
	s, a, owner := temporaryFixture(t)
	persistedBefore, _ := s.Store.GetApp(a.ID)
	before := launchFingerprint(persistedBefore)
	fileBefore, _ := os.ReadFile(a.EntryScript)
	w := requestTemporary(t, s, a.ID, "port-resolution", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result portResolution
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.State != "conflict" || result.TemporaryPort == nil || !result.TemporaryPort.Supported || result.TemporaryPort.ConfirmationToken == "" {
		t.Fatal(w.Body.String())
	}
	d, _ := portoverride.Inspect(a)
	// Authoritative bindings for the copied launch exclude the occupied old port.
	copy, _ := portoverride.Apply(a, d, result.TemporaryPort.Port)
	bindings := recovery.RequiredBindings(copy)
	if len(bindings) != 1 || bindings[0].Port == owner.Addr().(*net.TCPAddr).Port {
		t.Fatal(bindings)
	}
	stolen, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", result.TemporaryPort.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer stolen.Close()
	w = requestTemporary(t, s, a.ID, "temporary-port-start", result.TemporaryPort.ConfirmationToken)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "临时端口已不可用") {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(s.Manager.Registry.All()) != 0 {
		t.Fatal("unexpected process was launched")
	}
	latest, _ := s.Store.GetApp(a.ID)
	after := launchFingerprint(latest)
	fileAfter, _ := os.ReadFile(a.EntryScript)
	if before != after || !bytes.Equal(fileBefore, fileAfter) {
		t.Fatal("configuration changed")
	}
	if connection, err := net.DialTimeout("tcp4", owner.Addr().String(), time.Second); err != nil {
		t.Fatal("A no longer listening", err)
	} else {
		connection.Close()
	}
	w = requestTemporary(t, s, a.ID, "temporary-port-start", result.TemporaryPort.ConfirmationToken)
	if w.Code != 409 {
		t.Fatal("token reused", w.Code)
	}
}

func TestTemporaryPlansRejectExpiredWrongProjectAndChangedConfig(t *testing.T) {
	for _, mode := range []string{"expired", "project", "configuration", "script"} {
		t.Run(mode, func(t *testing.T) {
			s, a, _ := temporaryFixture(t)
			w := requestTemporary(t, s, a.ID, "port-resolution", "")
			var result portResolution
			json.Unmarshal(w.Body.Bytes(), &result)
			token := result.TemporaryPort.ConfirmationToken
			id := a.ID
			switch mode {
			case "expired":
				p := s.temporaryPortPlans[token]
				p.Expires = time.Now().Add(-time.Second)
				s.temporaryPortPlans[token] = p
			case "project":
				id = "other"
			case "configuration":
				a.Env["EXTRA"] = "value"
				s.Store.UpdateApp(a)
			case "script":
				f, _ := os.OpenFile(a.EntryScript, os.O_APPEND|os.O_WRONLY, 0600)
				f.WriteString(" ")
				f.Close()
			}
			w = requestTemporary(t, s, id, "temporary-port-start", token)
			if w.Code != 409 || len(s.Manager.Registry.All()) != 0 {
				t.Fatal(mode, w.Code, w.Body.String())
			}
		})
	}
}
