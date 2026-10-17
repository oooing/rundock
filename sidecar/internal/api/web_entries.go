package api

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/store"
)

type webEntryCache struct {
	mu    sync.Mutex
	items map[string]*webEntryCheck
	gate  chan struct{}
}
type webEntryCheck struct {
	result  probe.WebEntry
	expires time.Time
	busy    bool
}

func newWebEntryCache() *webEntryCache {
	return &webEntryCache{items: map[string]*webEntryCheck{}, gate: make(chan struct{}, 4)}
}

// Rendering never performs network I/O. Bounded, deduplicated background checks
// expire, retry slow startups and are scoped to the app/run/process/candidate.
func (c *webEntryCache) inspect(key, raw string, changed func()) probe.WebEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	entry := c.items[key]
	if entry == nil {
		for k, v := range c.items {
			if !v.busy && now.Sub(v.expires) > time.Minute {
				delete(c.items, k)
			}
		}
		if len(c.items) >= 1024 {
			return probe.WebEntry{State: "checking"}
		}
		entry = &webEntryCheck{result: probe.WebEntry{State: "checking"}}
		c.items[key] = entry
	}
	if !entry.busy && !now.Before(entry.expires) {
		select {
		case c.gate <- struct{}{}:
			entry.busy = true
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				result := probe.CheckWebEntry(ctx, raw)
				cancel()
				c.mu.Lock()
				different := entry.result != result
				entry.result, entry.expires, entry.busy = result, time.Now().Add(10*time.Second), false
				c.mu.Unlock()
				<-c.gate
				if different && changed != nil {
					changed()
				}
			}()
		default: // The next runtime refresh retries; no waiting goroutine per port.
		}
	}
	// Never advertise a long-expired success while the worker pool is busy.
	if !entry.expires.IsZero() && now.Sub(entry.expires) > 5*time.Second {
		return probe.WebEntry{State: "checking"}
	}
	return entry.result
}

// Add browser-entry evidence to response copies only. Historical URLs and
// manually assigned roles are candidates, never proof that a page exists.
func (s *Server) applyWebEntries(a *store.App, row map[string]any) {
	if s.webEntries == nil {
		return
	}
	status, _ := row["status"].(string)
	if status != "running" && status != "starting" && status != "degraded" {
		// A stopped or uncertain runtime must not resurrect a historical page.
		row["lastUrl"] = ""
		return
	}
	services, _ := row["services"].([]*store.AppService)
	copies := make([]*store.AppService, 0, len(services))
	for _, svc := range services {
		copy := *svc
		copy.WebURL, copy.WebState, copy.WebReason = "", "", ""
		copies = append(copies, &copy)
	}
	services = copies
	row["services"] = services
	preferred, _ := row["lastUrl"].(string)
	row["lastUrl"] = ""
	key := fmt.Sprintf("%s/%v/%v", a.ID, row["runId"], row["pid"])
	check := func(raw string) probe.WebEntry {
		return s.webEntries.inspect(key+"/"+raw, raw, func() {
			if s.Hub != nil {
				s.Hub.BroadcastStatus(a.ID, "", "", "")
			}
		})
	}
	ordered := append([]*store.AppService(nil), services...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if (ordered[i].Role == store.RoleFrontend) != (ordered[j].Role == store.RoleFrontend) {
			return ordered[i].Role == store.RoleFrontend
		}
		return ordered[i].Port < ordered[j].Port
	})
	for _, svc := range ordered {
		if svc.Role == store.RoleDatabase {
			continue
		}
		raw := svc.URL
		if raw == "" {
			raw = probe.JoinHostPort("127.0.0.1", svc.Port)
		}
		// Keep an explicitly configured path instead of guessing the root.
		if u, err := url.Parse(preferred); err == nil && probe.LocalWebURL(preferred) && u.Port() == strconv.Itoa(svc.Port) {
			raw = preferred
		}
		result := check(raw)
		// Logs can contain a real app path (e.g. /library) even when the
		// discovered service URL is just its origin. Verify it, never invent it.
		if result.State != "available" && a.LastURL != raw {
			if u, err := url.Parse(a.LastURL); err == nil && probe.LocalWebURL(a.LastURL) && u.Port() == strconv.Itoa(svc.Port) && u.Path != "" && u.Path != "/" {
				if candidate := check(a.LastURL); candidate.State == "available" {
					raw, result = a.LastURL, candidate
				}
			}
		}
		svc.WebState, svc.WebReason = result.State, result.Reason
		if result.State == "available" {
			svc.WebURL = raw
			if svc.StatusScope != "auxiliary" && (row["lastUrl"] == "" || raw == preferred) {
				row["lastUrl"] = raw
			}
		}
	}
}
