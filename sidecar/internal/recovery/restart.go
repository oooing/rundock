package recovery

import (
	"os"
	"path/filepath"
	"strings"
)

// DirectWorkerEntry is deliberately narrower than observation ownership. A
// project path passed as data to an editor, shell, eval or shared tool cannot
// authorize closing that process. Only an explicit worker script is accepted.
func DirectWorkerEntry(p Process, root string) string {
	entry := directWorkerScript(p)
	if entry == "" || !Inside(root, entry) || SameProject(root, filepath.Dir(root)) {
		return ""
	}
	return entry
}

func directWorkerScript(p Process) string {
	if p.PID <= 4 || p.Created == "" || p.Executable == "" {
		return ""
	}
	args := commandArgs(p.CommandLine)
	if len(args) < 2 {
		return ""
	}
	i := 1
	switch strings.ToLower(filepath.Base(p.Executable)) {
	case "node.exe":
		// Flags before the script can inject loaders/eval; leave those to the
		// original launcher instead of guessing which argument executes.
		if strings.HasPrefix(args[i], "-") {
			return ""
		}
		if ext := strings.ToLower(filepath.Ext(args[i])); ext != ".js" && ext != ".mjs" && ext != ".cjs" {
			return ""
		}
	case "python.exe", "pythonw.exe":
		if args[i] == "-u" {
			i++
		}
		if i >= len(args) || strings.ToLower(filepath.Ext(args[i])) != ".py" {
			return ""
		}
	default:
		return ""
	}
	if !filepath.IsAbs(args[i]) {
		return ""
	}
	return args[i]
}

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
	// A known script outside the project is not made safe by ancestry alone,
	// even when its project was never registered in RunDock. Shared/global
	// tools without local script evidence must be stopped by their launcher.
	if entry := directWorkerScript(p); entry != "" && !Inside(root, entry) {
		return false
	}
	if Inside(root, p.Executable) {
		return true
	}
	switch strings.ToLower(filepath.Base(p.Executable)) {
	case "cmd.exe", "powershell.exe", "pwsh.exe", "node.exe", "python.exe", "pythonw.exe", "java.exe", "dotnet.exe", "go.exe", "cargo.exe", "rustc.exe", "conhost.exe":
		return true
	}
	return false
}
