package publisher

// Scratch registrations live outside the disposable directory. An OS lock on
// each registration is held for its entire lifetime, including deletion. This
// works across sidecars and is released even if the owner crashes.
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type scratchRecord struct {
	Schema int    `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
}

type scratchWorkspace struct {
	Dir          string
	registration string
	lease        *os.File
	mu           sync.Mutex
}

func scratchPrefix(kind string) string {
	switch kind {
	case "candidate":
		return "rundock-candidate-"
	case "snapshot":
		return "rundock-local-build-"
	case "command":
		return "rd-t-"
	}
	return ""
}

func newScratch(kind string) (*scratchWorkspace, error) {
	return newScratchAt(os.TempDir(), kind)
}

func newScratchAt(temp, kind string) (*scratchWorkspace, error) {
	prefix := scratchPrefix(kind)
	if prefix == "" {
		return nil, errors.New("invalid scratch kind")
	}
	registry := filepath.Join(temp, ".rundock-scratch-v1")
	if err := os.MkdirAll(registry, 0700); err != nil {
		return nil, err
	}
	unpin, err := pinScratchDirectory(registry)
	if err != nil {
		return nil, err
	}
	defer unpin()
	var token [12]byte
	if _, err = rand.Read(token[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(token[:])
	registration := filepath.Join(registry, id+".json")
	lease, err := os.OpenFile(registration, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockScratchFile(lease); err != nil {
		lease.Close()
		return nil, err
	}
	w := &scratchWorkspace{Dir: filepath.Join(temp, prefix+id), registration: registration, lease: lease}
	record := scratchRecord{Schema: 1, Name: prefix + id, Kind: kind}
	// Persist provenance before making the directory. A crash at any subsequent
	// instruction is recoverable, without guessing ownership from a name/age.
	if err = json.NewEncoder(lease).Encode(record); err == nil {
		err = lease.Sync()
	}
	if err == nil {
		err = os.Mkdir(w.Dir, 0700)
	}
	if err != nil {
		lease.Close()
		os.Remove(registration)
		return nil, err
	}
	return w, nil
}

func (w *scratchWorkspace) cleanup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return w.cleanupContext(ctx)
}

func (w *scratchWorkspace) cleanupContext(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lease == nil {
		return nil
	}
	unpin, err := pinScratchDirectory(filepath.Dir(w.registration))
	if err == nil {
		defer unpin()
		err = removeScratchTree(ctx, w.Dir)
	}
	w.lease.Close()
	w.lease = nil
	if err == nil {
		err = os.Remove(w.registration)
	}
	// Keep the registration on failure, so a later sweep retries locked files.
	return err
}

// removeScratchTree never follows a link, including a junction created by a
// package manager. Pin each parent against replacement while removing children.
func removeScratchTree(ctx context.Context, path string) error {
	unpin, err := pinScratchDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer unpin()
	return removeScratchEntry(ctx, path)
}

func removeScratchEntry(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if before.IsDir() && !isPathLink(before) {
		unpin, err := pinScratchDirectory(path)
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(path)
		if err == nil {
			var failures []error
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					failures = append(failures, err)
					break
				}
				if err := removeScratchEntry(ctx, filepath.Join(path, entry.Name())); err != nil {
					// One busy file must not retain every other build output.
					failures = append(failures, err)
				}
			}
			err = errors.Join(failures...)
		}
		unpin()
		if err != nil {
			return err
		}
	}
	latest, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !os.SameFile(before, latest) {
		return errors.New("temporary entry changed during cleanup")
	}
	if !isPathLink(latest) && latest.Mode().IsRegular() && latest.Mode().Perm()&0200 == 0 {
		if err := os.Chmod(path, latest.Mode().Perm()|0200); err != nil {
			return err
		}
	}
	return os.Remove(path)
}

func recoverScratch(ctx context.Context, temp string) (int, error) {
	registry := filepath.Join(temp, ".rundock-scratch-v1")
	if _, err := os.Lstat(registry); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	unpin, err := pinScratchDirectory(registry)
	if err != nil {
		return 0, err
	}
	defer unpin()
	entries, err := os.ReadDir(registry)
	if err != nil {
		return 0, err
	}
	removed := 0
	var failures []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if len(id) != 24 || entry.Name() != id+".json" {
			continue
		}
		if _, err := hex.DecodeString(id); err != nil {
			continue
		}
		path := filepath.Join(registry, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || isPathLink(info) || info.Size() > 1024 {
			continue
		}
		lease, err := os.OpenFile(path, os.O_RDWR, 0600)
		if err != nil {
			continue
		}
		opened, statErr := lease.Stat()
		if statErr != nil || !os.SameFile(info, opened) || lockScratchFile(lease) != nil {
			lease.Close()
			continue
		}
		var record scratchRecord
		if err = json.NewDecoder(lease).Decode(&record); err != nil || record.Schema != 1 || scratchPrefix(record.Kind) == "" || record.Name != scratchPrefix(record.Kind)+id {
			lease.Close()
			continue // Unknown/malformed records never authorize directory deletion.
		}
		w := &scratchWorkspace{Dir: filepath.Join(temp, record.Name), registration: path, lease: lease}
		if err = w.cleanupContext(ctx); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", record.Kind, err))
		} else {
			removed++
		}
	}
	return removed, errors.Join(failures...)
}

// Only process-scoped TEMP changes: shared download caches, SDKs, project files
// and the user's system environment remain reusable and untouched.
func scratchEnvironment(env []string, dir string) []string {
	out := make([]string, 0, len(env)+3)
	for _, entry := range env {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if key != "TEMP" && key != "TMP" && key != "TMPDIR" {
			out = append(out, entry)
		}
	}
	return append(out, "TEMP="+dir, "TMP="+dir, "TMPDIR="+dir)
}
