package recovery

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/launcher-sidecar/internal/store"
)

func TestNativeGroupServiceBoundaries(t *testing.T) {
	base := t.TempDir()
	dock := &store.App{ID: "dock", Cwd: filepath.Join(base, "dock")}
	duck := &store.App{ID: "duck", Cwd: filepath.Join(base, "duck")}
	nested := &store.App{ID: "nested", Cwd: filepath.Join(dock.Cwd, "nested")}
	node := filepath.Join(base, "tools", "node.exe")
	s := RuntimeSnapshot{Projects: []*store.App{dock, duck, nested}, Processes: []Process{
		{PID: 100, Created: "1000", Executable: filepath.Join(dock.Cwd, "sidecar.exe")},
		{PID: 101, ParentPID: 100, Created: "2000", Executable: node, CommandLine: `node "` + filepath.Join(dock.Cwd, "vite.js") + `"`},
		{PID: 102, ParentPID: 100, Created: "2000", Executable: node, CommandLine: `node "` + filepath.Join(duck.Cwd, "vite.js") + `"`},
		{PID: 103, ParentPID: 102, Created: "3000", Executable: node},
		{PID: 104, ParentPID: 100, Created: "2000", Executable: node, CommandLine: `node "` + filepath.Join(nested.Cwd, "server.js") + `"`},
		// A detached worker remains owned even with no surviving parent/path.
		{PID: 105, ParentPID: 999, Created: "3000", Executable: node},
		// PID reused since the snapshot: never grant service ownership.
		{PID: 106, ParentPID: 100, Created: "2000", Executable: node},
	}}
	members := map[int]uint64{100: 1000, 101: 2000, 102: 2000, 103: 3000, 104: 2000, 105: 3000, 106: 4000, 107: 5000}
	if got := FilterServiceProcesses(dock, s, members); !reflect.DeepEqual(got, map[int]uint64{100: 1000, 101: 2000, 105: 3000}) {
		t.Fatal(got)
	}
	// An old foreign parent's reused PID cannot exclude an owned orphan.
	s.Processes[2].Created = "4000"
	if got := FilterServiceProcesses(dock, s, members); got[103] != 3000 {
		t.Fatal("reused parent changed ownership", got)
	}
}
