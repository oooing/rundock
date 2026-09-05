package publisher

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestShortPaths(t *testing.T) {
	repo := t.TempDir()
	ptr, err := windows.UTF16PtrFromString(repo)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	length, err := windows.GetShortPathName(ptr, &buffer[0], uint32(len(buffer)))
	if err != nil {
		t.Fatal(err)
	}
	alias := windows.UTF16ToString(buffer[:length])
	if alias == repo {
		t.Skip("8.3 names are disabled on this volume")
	}
	assertSameRepositoryLock(t, repo, alias)
	// Match hosted Windows runners whose TEMP/TMP uses an 8.3 parent path.
	// These are the exact release tests that failed in the first cloud run.
	t.Setenv("TMP", alias)
	t.Setenv("TEMP", alias)
	t.Run("plan", TestPlanTargetsValidatesSavedManifest)
	t.Run("lock", TestStartBlocksDuplicateTagAndConcurrentRelease)
	t.Run("snapshot", TestExecutionPlanSnapshotDoesNotFollowLaterConfigEdits)
}
