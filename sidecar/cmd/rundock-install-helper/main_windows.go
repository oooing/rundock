// The installer moves loaded executables aside, without touching process or Job
// handles. Windows can then install new files while the old programs keep running.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/windows"
)

var names = []string{"launcher-platform.exe", "launcher-sidecar.exe"}

const journalName = ".rundock-install-backup.json"

type backup struct {
	Name  string
	Saved string
}
type recovery struct {
	Owner string
	Files []backup
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
	var flags uint32
	if replace {
		flags = windows.MOVEFILE_REPLACE_EXISTING
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

func readJournal(root, owner string) ([]backup, error) {
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
	if err = json.Unmarshal(raw, &data); err != nil || data.Owner != owner || len(data.Files) > len(names) {
		return nil, errors.New("invalid installer recovery record")
	}
	records := data.Files
	seen := map[string]bool{}
	for _, record := range records {
		if (record.Name != names[0] && record.Name != names[1]) || seen[record.Name] || !savedName.MatchString(record.Saved) || !strings.HasPrefix(record.Saved, strings.TrimSuffix(record.Name, ".exe")+".") {
			return nil, errors.New("invalid installer recovery path")
		}
		seen[record.Name] = true
	}
	return records, nil
}

func rollback(root, owner string) error {
	records, err := readJournal(root, owner)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		saved := filepath.Join(root, record.Saved)
		if _, err := os.Lstat(saved); os.IsNotExist(err) {
			continue
		}
		if err := move(saved, filepath.Join(root, record.Name), true); err != nil {
			return err
		}
	}
	return os.Remove(filepath.Join(root, journalName))
}

func prepare(root, owner string) error {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}
	// Exclusive journal also prevents overlapping installers for this directory.
	journal, err := os.OpenFile(filepath.Join(root, journalName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("cannot prepare installation (another setup or recovery may be pending): %w", err)
	}
	defer journal.Close()
	cleanup(root)
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		_ = journal.Close()
		_ = os.Remove(journal.Name())
		return err
	}
	var records []backup
	save := func() error {
		raw, _ := json.Marshal(recovery{Owner: owner, Files: records})
		if err := journal.Truncate(0); err != nil {
			return err
		}
		if _, err := journal.Seek(0, 0); err != nil {
			return err
		}
		if _, err := journal.Write(raw); err != nil {
			return err
		}
		return journal.Sync()
	}
	if err := save(); err != nil {
		_ = journal.Close()
		_ = os.Remove(journal.Name())
		return err
	}
	fail := func(cause error) error {
		_ = journal.Close()
		restoreErr := rollback(root, owner)
		return errors.Join(cause, restoreErr)
	}
	for _, name := range names {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fail(err)
		}
		if !info.Mode().IsRegular() {
			return fail(errors.New("installation executable is not a regular file"))
		}
		record := backup{Name: name, Saved: strings.TrimSuffix(name, ".exe") + ".rundock-running-" + hex.EncodeToString(token[:]) + ".exe"}
		records = append(records, record)
		// Persist recovery paths before moving any file.
		if err := save(); err != nil {
			return fail(err)
		}
		if err := move(path, filepath.Join(root, record.Saved), false); err != nil {
			return fail(fmt.Errorf("cannot prepare %s without closing it; check file permissions or run setup as administrator: %w", name, err))
		}
	}
	return nil
}

func run(mode, path string, owners ...string) error {
	owner := "test"
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
	switch mode {
	case "prepare":
		return prepare(root, owner)
	case "rollback":
		return rollback(root, owner)
	case "commit":
		if _, err := readJournal(root, owner); os.IsNotExist(err) {
			return nil
		} else if err != nil {
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
