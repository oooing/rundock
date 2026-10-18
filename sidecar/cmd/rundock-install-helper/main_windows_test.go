package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/windows"
)

func TestLockedFileRollsBackEarlierMoves(t *testing.T) {
	root := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	locked, _ := windows.UTF16PtrFromString(filepath.Join(root, names[1]))
	h, err := windows.CreateFile(locked, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if err := run("prepare", root); err == nil {
		t.Fatal("locked executable must fail preparation")
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("original file was not restored: %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, journalName)); !os.IsNotExist(err) {
		t.Fatal("successful rollback must clear the journal")
	}
}

func TestInstallerRecoveryAndConcurrentGuard(t *testing.T) {
	root := t.TempDir()
	for _, name := range names {
		os.WriteFile(filepath.Join(root, name), []byte("old"), 0600)
	}
	if err := run("prepare", root); err != nil {
		t.Fatal(err)
	}
	if err := run("prepare", root); err == nil {
		t.Fatal("overlapping installation must not discard backups")
	}
	if err := run("rollback", root, "another setup"); err == nil {
		t.Fatal("a competing installer must not roll back our staged files")
	}
	for _, name := range names {
		os.WriteFile(filepath.Join(root, name), []byte("partial new"), 0600)
	}
	if err := run("rollback", root); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(raw) != "old" {
			t.Fatal("old installer input was not restored")
		}
	}
	if err := run("prepare", root); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		writeTestFile(t, filepath.Join(root, name), testPE(t))
	}
	if err := run("commit", root); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Fatal("unloaded backup files were not cleaned")
	}
}

func writeTestFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
}

func testPE(t *testing.T) []byte {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func oldInstall(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		writeTestFile(t, filepath.Join(root, name), []byte("original "+name))
	}
	return root
}

func assertOriginals(t *testing.T, root string) {
	t.Helper()
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(raw) != "original "+name {
			t.Fatalf("original %s not restored: %v", name, err)
		}
	}
}

func TestCommitRequiresBothCompletePEExecutables(t *testing.T) {
	for _, fault := range []string{"missing", "text", "truncated", "wrong-machine", "dll", "entry-point"} {
		t.Run(fault, func(t *testing.T) {
			root := oldInstall(t)
			if err := run("prepare", root); err != nil {
				t.Fatal(err)
			}
			image := testPE(t)
			writeTestFile(t, filepath.Join(root, names[0]), image)
			peOffset := int(binary.LittleEndian.Uint32(image[0x3c:]))
			switch fault {
			case "text":
				image = []byte("not an executable")
			case "truncated":
				image = image[:len(image)/2]
			case "wrong-machine":
				binary.LittleEndian.PutUint16(image[peOffset+4:], pe.IMAGE_FILE_MACHINE_I386)
			case "dll":
				binary.LittleEndian.PutUint16(image[peOffset+22:], pe.IMAGE_FILE_EXECUTABLE_IMAGE|pe.IMAGE_FILE_DLL)
			case "entry-point":
				binary.LittleEndian.PutUint32(image[peOffset+40:], 0)
			}
			if fault != "missing" {
				writeTestFile(t, filepath.Join(root, names[1]), image)
			}
			if err := run("commit", root); err == nil {
				t.Fatal("invalid payload committed")
			}
			if _, err := readJournal(root); err != nil {
				t.Fatalf("commit discarded recovery: %v", err)
			}
			if err := run("rollback", root); err != nil {
				t.Fatal(err)
			}
			assertOriginals(t, root)
		})
	}
}

// This subprocess owns only a temporary fixture. Exiting without rollback
// exercises real process death and OS lock release, never a user installation.
func TestInstallerCrashProcess(t *testing.T) {
	root := os.Getenv("RUNDOCK_INSTALLER_CRASH_FIXTURE")
	if root == "" {
		return
	}
	if os.Getenv("RUNDOCK_INSTALLER_CRASH_MODE") == "lock" {
		if _, err := lockDirectory(root); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := run("prepare", root); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("RUNDOCK_INSTALLER_CRASH_MODE") == "partial" {
			writeTestFile(t, filepath.Join(root, names[0]), []byte("partial new installation"))
		}
	}
	os.Exit(0)
}

func crashFixture(t *testing.T, root, mode string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestInstallerCrashProcess$")
	cmd.Env = append(os.Environ(), "RUNDOCK_INSTALLER_CRASH_FIXTURE="+root, "RUNDOCK_INSTALLER_CRASH_MODE="+mode)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture failed: %v %s", err, output)
	}
}

func TestCrashedInstallerRestoresBeforeRetry(t *testing.T) {
	for _, mode := range []string{"prepared", "partial", "lock"} {
		t.Run(mode, func(t *testing.T) {
			root := oldInstall(t)
			crashFixture(t, root, mode)
			if err := run("prepare", root); err != nil {
				t.Fatalf("retry after process death: %v", err)
			}
			if err := run("rollback", root); err != nil {
				t.Fatal(err)
			}
			assertOriginals(t, root)
		})
	}
}

func TestPIDReuseDoesNotKeepDeadTransactionLocked(t *testing.T) {
	root := oldInstall(t)
	if err := run("prepare", root); err != nil {
		t.Fatal(err)
	}
	data, err := readJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	data.Created-- // Same live PID, but not the original process birth time.
	if err := saveJournal(root, data); err != nil {
		t.Fatal(err)
	}
	if err := run("commit", root); err == nil {
		t.Fatal("reused PID accepted as owner")
	}
	if err := run("prepare", root); err != nil {
		t.Fatal(err)
	}
	if err := run("rollback", root); err != nil {
		t.Fatal(err)
	}
	assertOriginals(t, root)
}

func TestOperationLockDoesNotDependOnJournalExistence(t *testing.T) {
	root := oldInstall(t)
	unlock, err := lockDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := run("prepare", root); err == nil {
		t.Fatal("concurrent operation acquired lock")
	}
	assertOriginals(t, root)
	unlock()
	if err := run("prepare", root); err != nil {
		t.Fatal(err)
	}
	if err := run("rollback", root); err != nil {
		t.Fatal(err)
	}
}

func TestAtomicJournalFailurePreservesPreviousRecord(t *testing.T) {
	root := oldInstall(t)
	data, err := identifyOwner(strconv.Itoa(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	if err := saveJournal(root, data); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, journalName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	data.Owner = "changed"
	err = saveJournal(root, data)
	windows.CloseHandle(h)
	if err == nil {
		t.Fatal("journal replace should fail under delete-denying lock")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed atomic save damaged previous journal")
	}
	writeTestFile(t, filepath.Join(root, journalName+".tmp-crashed"), []byte("{partial"))
	if _, err := readJournal(root); err != nil {
		t.Fatal("incomplete temporary file affected committed journal", err)
	}
}

func TestUnknownAndLegacyOwnersPreserveRecovery(t *testing.T) {
	for _, kind := range []string{"msi", "legacy", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			root := oldInstall(t)
			owner := strconv.Itoa(os.Getpid())
			if kind == "msi" {
				owner = "{66666666-7777-8888-9999-AAAAAAAAAAAA}"
			}
			if err := run("prepare", root, owner); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, journalName)
			if kind == "legacy" {
				data, err := readJournal(root)
				if err != nil {
					t.Fatal(err)
				}
				data.Version = 0
				raw, _ := json.Marshal(data)
				writeTestFile(t, path, raw)
			} else if kind == "invalid" {
				writeTestFile(t, path, []byte("{partial"))
			}
			before, _ := os.ReadFile(path)
			if err := run("prepare", root); err == nil {
				t.Fatal("uncertain transaction was adopted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("uncertain recovery record was changed")
			}
			backups, _ := filepath.Glob(filepath.Join(root, "*.rundock-running-*.exe"))
			if len(backups) != 2 {
				t.Fatal("uncertain backups removed")
			}
			if kind == "msi" {
				if err := run("rollback", root, owner); err != nil {
					t.Fatal("original MSI owner cannot roll back", err)
				}
				assertOriginals(t, root)
			}
		})
	}
}

func TestRecoveryRequiresVerifiedOriginalWhenBackupMissing(t *testing.T) {
	root := oldInstall(t)
	crashFixture(t, root, "partial")
	data, err := readJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an interrupted rollback: one original has already been restored.
	if err := move(filepath.Join(root, data.Files[1].Saved), filepath.Join(root, data.Files[1].Name), true); err != nil {
		t.Fatal(err)
	}
	if err := run("prepare", root); err != nil {
		t.Fatal(err)
	}
	if err := run("rollback", root); err != nil {
		t.Fatal(err)
	}
	assertOriginals(t, root)
	crashFixture(t, root, "partial")
	data, err = readJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, data.Files[0].Saved)); err != nil {
		t.Fatal(err)
	}
	if err := run("prepare", root); err == nil {
		t.Fatal("missing original backup was silently ignored")
	}
	if _, err := os.Stat(filepath.Join(root, journalName)); err != nil {
		t.Fatal("uncertain journal removed")
	}
}

func TestRecoveryRejectsPathsOutsideItsTwoExecutables(t *testing.T) {
	root := t.TempDir()
	if err := run("prepare", filepath.VolumeName(root)+string(os.PathSeparator)); err == nil {
		t.Fatal("drive root allowed")
	}
	if err := run("prepare", "relative"); err == nil {
		t.Fatal("relative install directory allowed")
	}
	other := filepath.Join(root, "project.json")
	os.WriteFile(other, []byte("keep"), 0600)
	os.WriteFile(filepath.Join(root, journalName), []byte(`{"Owner":"test","Files":[{"Name":"project.json","Saved":"../project.json"}]}`), 0600)
	if err := run("rollback", root); err == nil {
		t.Fatal("untrusted recovery path allowed")
	}
	raw, _ := os.ReadFile(other)
	if string(raw) != "keep" {
		t.Fatal("project data changed")
	}
}
