package api

import (
	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
	"time"
)

// This small cache exists only for display between explicit diagnosis and the
// next card refresh. It grants no process control: every start and resolution
// obtains new system evidence and process handles. Changed config invalidates it.
func (s *Server) cachePortDiagnosis(a *store.App, result portResolution) {
	if s.runtimeMonitor == nil || result.State == "running" {
		return
	}
	observation := recovery.Observation{State: result.State, Message: result.Message, ReservedPorts: append([]int(nil), result.ReservedPorts...), Services: []*store.AppService{}}
	for _, c := range result.Conflicts {
		observation.Conflicts = append(observation.Conflicts, recovery.Conflict{Port: c.Port, PID: c.PID, Name: c.Name})
	}
	m := s.runtimeMonitor
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.diagnoses == nil {
		m.diagnoses = map[string]cachedPortDiagnosis{}
	}
	now := time.Now()
	for id, c := range m.diagnoses {
		if !c.expires.After(now) {
			delete(m.diagnoses, id)
		}
	}
	if len(m.diagnoses) >= 128 {
		return
	}
	m.diagnoses[a.ID] = cachedPortDiagnosis{config: launchFingerprint(a), expires: now.Add(10 * time.Second), observation: observation}
}
