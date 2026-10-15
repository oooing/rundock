package publisher

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestScratchRecoversOnlyAbandonedRegistrations(t *testing.T) {
	temp := t.TempDir()
	active, err := newScratchAt(temp, "candidate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { active.cleanup() })
	dead, err := newScratchAt(temp, "command")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dead.Dir, "nested", "object.bin"), "temporary")
	// Mimic OS lock release after a crash. No graceful cleanup is performed.
	dead.lease.Close()
	dead.lease = nil
	foreign := filepath.Join(temp, "rundock-candidate-not-owned")
	writeTestFile(t, filepath.Join(foreign, "keep.txt"), "user content")
	n, err := recoverScratch(context.Background(), temp)
	if err != nil || n != 1 {
		t.Fatalf("recovery: %d %v", n, err)
	}
	for _, path := range []string{active.Dir, foreign} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("live/foreign removed: %s", path)
		}
	}
	for _, path := range []string{dead.Dir, dead.registration} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("orphan retained: %s %v", path, err)
		}
	}
	n, err = recoverScratch(context.Background(), temp)
	if err != nil || n != 0 {
		t.Fatalf("idempotence: %d %v", n, err)
	}
}

func TestScratchUntrustedRegistrationCannotDeleteOutside(t *testing.T) {
	temp := t.TempDir()
	w, err := newScratchAt(temp, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(w.Dir)
	w.lease.Close()
	w.lease = nil
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "keep.txt"), "safe")
	data, _ := json.Marshal(scratchRecord{1, outside, "snapshot"})
	if err := os.WriteFile(w.registration, data, 0600); err != nil {
		t.Fatal(err)
	}
	n, err := recoverScratch(context.Background(), temp)
	if err != nil || n != 0 {
		t.Fatalf("unexpected cleanup: %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatal("outside file lost")
	}
}

func TestScratchLockedFileIsRetried(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows delete sharing")
	}
	temp := t.TempDir()
	w, err := newScratchAt(temp, "command")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.Dir, "locked.bin")
	writeTestFile(t, path, "held open")
	unlocked := filepath.Join(w.Dir, "z-unlocked.bin")
	writeTestFile(t, unlocked, "remove even when the preceding file is busy")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.cleanup(); err == nil {
		f.Close()
		t.Fatal("expected locked cleanup to be pending")
	}
	if _, err := os.Stat(w.registration); err != nil {
		t.Fatal("retry provenance lost")
	}
	if _, err := os.Stat(unlocked); !os.IsNotExist(err) {
		f.Close()
		t.Fatal("one locked file retained unrelated temporary files")
	}
	f.Close()
	if n, err := recoverScratch(context.Background(), temp); err != nil || n != 1 {
		t.Fatalf("retry: %d %v", n, err)
	}
}

func TestScratchRemovesLinksWithoutTouchingTarget(t *testing.T) {
	temp := t.TempDir()
	w, err := newScratchAt(temp, "command")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.cleanup() })
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "keep.txt"), "safe")
	link := filepath.Join(w.Dir, "external")
	if runtime.GOOS == "windows" {
		out, err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, outside).CombinedOutput()
		if err != nil {
			t.Fatalf("junction fixture: %s %v", out, err)
		}
	} else if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := w.cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatal("link target lost")
	}
}

func TestScratchProcessCrash(t *testing.T) {
	if temp := os.Getenv("RUNDOCK_SCRATCH_CRASH_FIXTURE"); temp != "" {
		w, err := newScratchAt(temp, "candidate")
		if err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile(filepath.Join(w.Dir, "large-object"), make([]byte, 8<<20), 0600); err != nil {
			os.Exit(4)
		}
		os.Exit(23) // no defer, just like an abruptly killed owner
	}
	temp := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestScratchProcessCrash$")
	cmd.Env = append(os.Environ(), "RUNDOCK_SCRATCH_CRASH_FIXTURE="+temp)
	if err := cmd.Run(); err == nil || cmd.ProcessState.ExitCode() != 23 {
		t.Fatalf("crash fixture: %v", err)
	}
	if n, err := recoverScratch(context.Background(), temp); err != nil || n != 1 {
		t.Fatalf("crash recovery: %d %v", n, err)
	}
}

func TestBuildCommandCleansTempOnSuccessFailureAndCancel(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node required")
	}
	for _, mode := range []string{"success", "failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, filepath.Join(dir, "build.cjs"), `const fs=require('fs'),os=require('os'),path=require('path');fs.writeFileSync('temp-path.txt',os.tmpdir());fs.writeFileSync(path.join(os.tmpdir(),'object.bin'),Buffer.alloc(1024*1024));fs.writeFileSync('installer.bin','keep');if(process.argv[2]==='cancel')setInterval(()=>{},1000);else if(process.argv[2]==='failure')process.exit(1);`)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode == "cancel" {
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
				defer cancel()
			}
			out, err := runBuildCommand(ctx, dir, "node build.cjs "+mode)
			if (mode == "success") != (err == nil) {
				t.Fatalf("result %s %v", out, err)
			}
			raw, readErr := os.ReadFile(filepath.Join(dir, "temp-path.txt"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			path := string(raw)
			if !strings.HasPrefix(filepath.Base(path), "rd-t-") {
				t.Fatalf("not managed: %s", path)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("scratch remains: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "installer.bin")); err != nil {
				t.Fatal("output removed with temp")
			}
		})
	}
}
