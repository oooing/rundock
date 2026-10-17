package recovery

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/store"
)

func TestRuntimeOwnershipProjectBoundaries(t *testing.T) {
	base := t.TempDir()
	dock := &store.App{ID: "dock", Cwd: filepath.Join(base, "dock")}
	duck := &store.App{ID: "duck", Cwd: filepath.Join(base, "duck")}
	worker := filepath.Join(base, "tools", "node.exe")
	snap := RuntimeSnapshot{
		Projects: []*store.App{dock, duck}, LauncherPID: 100,
		Processes: []Process{
			{PID: 100, Created: "1000", Executable: filepath.Join(dock.Cwd, "sidecar.exe")},
			{PID: 101, ParentPID: 100, Created: "2000", Executable: worker},
			{PID: 102, ParentPID: 101, Created: "3000", Executable: worker, CommandLine: `node "` + filepath.Join(duck.Cwd, "vite.js") + `"`},
			{PID: 103, ParentPID: 101, Created: "3000", Executable: worker},
			{PID: 104, ParentPID: 100, Created: "2000", Executable: worker, CommandLine: `node "` + filepath.Join(dock.Cwd, "vite.js") + `"`},
			{PID: 105, ParentPID: 100, Created: "2000", Executable: worker},
		},
		ManagedRoots: []ProcessOwner{{AppID: "duck", PID: 101, Created: "2000"}},
		Listeners:    []probe.PortListener{{PID: 100, Port: 17655}, {PID: 104, Port: 17656}, {PID: 102, Port: 11784}, {PID: 103, Port: 11785}, {PID: 105, Port: 4318}},
	}
	ports := func(a *store.App, s RuntimeSnapshot) []int {
		var got []int
		for _, service := range InspectRuntime(a, nil, s).Services {
			got = append(got, service.Port)
		}
		return got
	}
	t.Run("launcher owns only its own services", func(t *testing.T) {
		if got := ports(dock, snap); !reflect.DeepEqual(got, []int{17655, 17656}) {
			t.Fatal(got)
		}
		if got := ports(duck, snap); !reflect.DeepEqual(got, []int{11784, 11785}) {
			t.Fatal(got)
		}
	})
	t.Run("managed identity overrides shared tools under launcher directory", func(t *testing.T) {
		s := snap
		s.Processes = append([]Process(nil), snap.Processes...)
		s.Processes[3].Executable = filepath.Join(dock.Cwd, "tools", "worker.exe")
		if got := ports(dock, s); !reflect.DeepEqual(got, []int{17655, 17656}) {
			t.Fatal(got)
		}
		if got := ports(duck, s); !reflect.DeepEqual(got, []int{11784, 11785}) {
			t.Fatal(got)
		}
	})
	t.Run("external project direct evidence stops traversal to launcher", func(t *testing.T) {
		s := snap
		s.ManagedRoots = nil
		s.LauncherPID = 0
		// Even without launcher identity, a known foreign project is a boundary.
		s.Listeners = []probe.PortListener{{PID: 102, Port: 11784}}
		if got := ports(dock, s); len(got) != 0 {
			t.Fatal(got)
		}
		if got := ports(duck, s); !reflect.DeepEqual(got, []int{11784}) {
			t.Fatal(got)
		}
	})
	t.Run("reused managed PID does not inherit old project identity", func(t *testing.T) {
		s := snap
		s.ManagedRoots = []ProcessOwner{{AppID: "duck", PID: 101, Created: "999"}}
		if got := ports(duck, s); !reflect.DeepEqual(got, []int{11784}) {
			t.Fatal(got)
		}
		if got := ports(dock, s); !reflect.DeepEqual(got, []int{17655, 17656}) {
			t.Fatal(got)
		}
	})
	t.Run("nested projects use most specific path", func(t *testing.T) {
		parent := &store.App{ID: "parent", Cwd: base}
		s := snap
		s.Projects = append([]*store.App{parent}, snap.Projects...)
		if got := ports(parent, s); len(got) != 1 || got[0] != 4318 {
			t.Fatal(got)
		}
		if got := ports(dock, s); !reflect.DeepEqual(got, []int{17655, 17656}) {
			t.Fatal(got)
		}
	})
	t.Run("outer native job cannot absorb nested project services", func(t *testing.T) {
		s := snap
		s.LauncherPID = 999
		s.Processes = append([]Process(nil), snap.Processes...)
		s.Processes[3].ParentPID = 102
		s.ManagedRoots = nil
		for _, p := range s.Processes {
			s.ManagedRoots = append(s.ManagedRoots, ProcessOwner{AppID: dock.ID, PID: p.PID, Created: p.Created, Inherited: p.PID != 100})
		}
		if got := ports(duck, s); !reflect.DeepEqual(got, []int{11784, 11785}) {
			t.Fatal("outer job hid nested project", got)
		}
		for _, port := range ports(dock, s) {
			if port == 11784 || port == 11785 {
				t.Fatal("outer job stole nested project port", port)
			}
		}
	})
}
