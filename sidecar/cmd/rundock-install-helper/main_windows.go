// The installer moves loaded executables aside, without touching process or Job
// handles. Windows can then install new files while the old programs keep running.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

var names = []string{"launcher-platform.exe", "launcher-sidecar.exe"}

const journalName = ".rundock-install-backup.json"

type backup struct {
	Name   string
	Saved  string
	Size   int64
	SHA256 string
}
type recovery struct {
	Version int
	Owner   string
	PID     uint32
	Created uint64
	Files   []backup
}

var savedName = regexp.MustCompile(`^launcher-(platform|sidecar)\.rundock-running-[0-9a-f]{32}\.exe$`)

func rootPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("installation directory must be absolute")
	}
	root := filepath.Clean(path)
	if root == filepath.VolumeName(root)+string(os.PathSeparator) {
		return "", errors.New("a drive root is not an installation directory")
	}
	if info, err := os.Lstat(root); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
		return "", errors.New("installation directory must be a real directory")
	}
	return root, nil
}

func move(from, to string, replace bool) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(src, dst, flags)
}

func unlocked(path string) bool {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}

func cleanup(root string) {
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if savedName.MatchString(entry.Name()) && entry.Type().IsRegular() {
			path := filepath.Join(root, entry.Name())
			if unlocked(path) {
				_ = os.Remove(path)
			}
		}
	}
}

func readJournal(root string) (*recovery, error) {
	path := filepath.Join(root, journalName)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, errors.New("invalid installer recovery record")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data recovery
	if err = json.Unmarshal(raw, &data); err != nil || data.Version != 1 || data.Owner == "" || data.PID == 0 || data.Created == 0 || len(data.Files) > len(names) {
		return nil, errors.New("invalid installer recovery record")
	}
	if pid, err := strconv.ParseUint(data.Owner, 10, 32); err == nil && uint32(pid) != data.PID {
		return nil, errors.New("installer recovery identity does not match owner")
	}
	records := data.Files
	seen := map[string]bool{}
	for _, record := range records {
		if (record.Name != names[0] && record.Name != names[1]) || seen[record.Name] || !savedName.MatchString(record.Saved) || !strings.HasPrefix(record.Saved, strings.TrimSuffix(record.Name, ".exe")+".") {
			return nil, errors.New("invalid installer recovery path")
		}
		if hash, err := hex.DecodeString(record.SHA256); err != nil || len(hash) != sha256.Size || record.Size < 0 {
			return nil, errors.New("invalid installer recovery checksum")
		}
		seen[record.Name] = true
	}
	return &data, nil
}

func fileDigest(path string) (int64, string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, "", err
	}
	if !info.Mode().IsRegular() {
		return 0, "", errors.New("installation file is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return n, hex.EncodeToString(h.Sum(nil)), err
}

func matchesBackup(path string, record backup) error {
	size, hash, err := fileDigest(path)
	if err != nil {
		return err
	}
	if size != record.Size || hash != record.SHA256 {
		return fmt.Errorf("recovery checksum mismatch: %s", record.Name)
	}
	return nil
}

// The journal describes every planned move before the first rename. Missing
// backups are safe only when the original bytes are already at the target.
func rollback(root string, data *recovery) error {
	for i := len(data.Files) - 1; i >= 0; i-- {
		record := data.Files[i]
		saved := filepath.Join(root, record.Saved)
		target := filepath.Join(root, record.Name)
		if _, err := os.Lstat(saved); os.IsNotExist(err) {
			if err := matchesBackup(target, record); err != nil {
				return err
			}
			continue
		}
		if err := matchesBackup(saved, record); err != nil {
			return err
		}
		if info, err := os.Lstat(target); err == nil && !info.Mode().IsRegular() {
			return errors.New("recovery target is not a regular file")
		}
		if err := move(saved, target, true); err != nil {
			return err
		}
	}
	return os.Remove(filepath.Join(root, journalName))
}

func saveJournal(root string, data *recovery) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(root, journalName+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return move(f.Name(), filepath.Join(root, journalName), true)
}

func prepare(root, owner string) error {
	identity, err := identifyOwner(owner)
	if err != nil {
		return err
	}
	if previous, err := readJournal(root); err == nil {
		// Opaque MSI owners and legacy/invalid records cannot establish that a
		// previous installation is gone. Never discard their recovery files.
		if _, err := strconv.ParseUint(previous.Owner, 10, 32); err != nil {
			return errors.New("MSI recovery requires the original installer; backups have been preserved")
		}
		alive, err := ownerAlive(previous)
		if err != nil {
			return err
		}
		if alive {
			return errors.New("another installer is still active; backups have been preserved")
		}
		if err := rollback(root, previous); err != nil {
			return fmt.Errorf("unfinished installation could not be restored: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	for _, name := range names {
		path := filepath.Join(root, name)
		size, hash, err := fileDigest(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		identity.Files = append(identity.Files, backup{Name: name, Saved: strings.TrimSuffix(name, ".exe") + ".rundock-running-" + hex.EncodeToString(token[:]) + ".exe", Size: size, SHA256: hash})
	}
	if err := saveJournal(root, identity); err != nil {
		return err
	}
	for _, record := range identity.Files {
		if err := move(filepath.Join(root, record.Name), filepath.Join(root, record.Saved), false); err != nil {
			return errors.Join(fmt.Errorf("cannot prepare %s without closing it: %w", record.Name, err), rollback(root, identity))
		}
	}
	return nil
}

func run(mode, path string, owners ...string) error {
	owner := strconv.Itoa(os.Getpid())
	if len(owners) > 0 {
		owner = owners[0]
	}
	if owner == "" || len(owner) > 128 {
		return errors.New("invalid installer identity")
	}
	root, err := rootPath(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(root); os.IsNotExist(err) && mode == "prepare" {
		return nil
	}
	unlock, err := lockDirectory(root)
	if err != nil {
		return err
	}
	defer unlock()
	switch mode {
	case "prepare":
		return prepare(root, owner)
	case "rollback", "commit":
		data, err := readJournal(root)
		if os.IsNotExist(err) {
			if mode == "commit" {
				return validatePayload(root)
			}
			return nil
		}
		if err != nil {
			return err
		}
		identity, err := identifyOwner(owner)
		if err != nil {
			return err
		}
		if data.Owner != owner || data.PID != identity.PID || data.Created != identity.Created {
			return errors.New("installer does not own this recovery record")
		}
		if mode == "rollback" {
			return rollback(root, data)
		}
		if err := validatePayload(root); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(root, journalName)); err != nil {
			return err
		}
		cleanup(root)
		return nil
	default:
		return errors.New("unknown installer operation")
	}
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: rundock-install-helper prepare|commit|rollback <absolute installation directory> <installer identity>")
		os.Exit(1)
	}
	if err := run(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
