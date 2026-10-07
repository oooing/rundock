package recovery

import (
	"os"
	"path/filepath"
	"strings"
)

// Console hosts close themselves when their clients exit. They are not project
// workers and must not be force-terminated (other clients may share a console).
func IsConsoleHost(p Process) bool {
	return samePath(p.Executable, filepath.Join(os.Getenv("SystemRoot"), "System32", "conhost.exe"))
}

// A folder mentioned by an editor is not a launch identity. Only a dedicated
// script interpreter executing the exact registered entry may own a restart.
func IsEntryProcess(p Process, entry string) bool {
	if !hasEntry(p, entry) {
		return false
	}
	if samePath(p.Executable, entry) {
		return true
	}
	name := strings.ToLower(filepath.Base(p.Executable))
	args := commandArgs(p.CommandLine)
	for i, arg := range args {
		if i+1 >= len(args) || !samePath(args[i+1], entry) {
			continue
		}
		switch name {
		case "cmd.exe":
			if strings.EqualFold(arg, "/c") {
				for _, extra := range args[i+2:] {
					if strings.ContainsAny(extra, "&|<>\r\n") {
						return false
					}
				}
				return true
			}
		case "powershell.exe", "pwsh.exe":
			if strings.EqualFold(arg, "-file") {
				return true
			}
		}
	}
	return false
}

// Shared GUI applications opened by a startup script must never be included in
// its restart. Unknown external executables require manual handling, not a guess.
func IsProjectWorker(p Process, root string) bool {
	if Inside(root, p.Executable) {
		return true
	}
	switch strings.ToLower(filepath.Base(p.Executable)) {
	case "cmd.exe", "powershell.exe", "pwsh.exe", "node.exe", "python.exe", "pythonw.exe", "java.exe", "dotnet.exe", "go.exe", "cargo.exe", "rustc.exe", "conhost.exe":
		return true
	}
	return false
}
