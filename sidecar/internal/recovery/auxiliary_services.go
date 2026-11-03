package recovery

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Miniflare launches workerd behind its stable public proxy. Its private
// entry socket changes on reload; standalone workerd servers remain required.
func AuxiliaryServiceProcesses(snapshot RuntimeSnapshot, members map[int]uint64) map[int]bool {
	out := map[int]bool{}
	for _, p := range snapshot.Processes {
		if members[p.PID] == 0 || p.Created != strconv.FormatUint(members[p.PID], 10) {
			continue
		}
		name := strings.ToLower(filepath.Base(p.Executable))
		if name != "workerd.exe" && name != "workerd" {
			continue
		}
		var binary, control, loopback, ephemeralEntry bool
		for _, arg := range commandArgs(p.CommandLine) {
			binary = binary || arg == "--binary"
			control = control || arg == "--control-fd=3"
			loopback = loopback || strings.HasPrefix(arg, "--external-addr=loopback=")
			ephemeralEntry = ephemeralEntry || (strings.HasPrefix(arg, "--socket-addr=entry=") && strings.HasSuffix(arg, ":0"))
		}
		if binary && control && loopback && ephemeralEntry {
			out[p.PID] = true
		}
	}
	return out
}
