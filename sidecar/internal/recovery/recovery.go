// Package recovery diagnoses port conflicts. Log text is evidence, never authority to kill.
package recovery

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Process struct {
	PID         int    `json:"pid"`
	ParentPID   int    `json:"parentPid"`
	Created     string `json:"created"`
	Executable  string `json:"executable"`
	CommandLine string `json:"commandLine"`
}
type Conflict struct {
	Port int    `json:"port"`
	PID  int    `json:"pid"`
	Name string `json:"name"`
	Safe bool   `json:"safe"`
}
type Issue struct {
	Code        string     `json:"code"`
	RunID       string     `json:"runId"`
	Ports       []int      `json:"ports"`
	Conflicts   []Conflict `json:"conflicts"`
	CanRecover  bool       `json:"canRecover"`
	Reason      string     `json:"reason"`
	Fingerprint string     `json:"fingerprint"`
}

var conflictText = regexp.MustCompile(`(?i)(EADDRINUSE|address already in use|port\s+\d+\s+.*(?:occupied|in use)|端口\s*\d+.*(?:占用|已有)|only one usage of each socket)`)
var portNumber = regexp.MustCompile(`(?i)(?:port[\s:=]+|端口\s*|:)(\d{2,5})\b`)

func PortsFromLogs(lines []string) []int {
	found := map[int]bool{}
	for _, line := range lines {
		if !conflictText.MatchString(line) {
			continue
		}
		for _, m := range portNumber.FindAllStringSubmatch(line, -1) {
			p, _ := strconv.Atoi(m[1])
			if p > 0 && p <= 65535 {
				found[p] = true
			}
		}
	}
	out := []int{}
	for p := range found {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func Inside(root, path string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(strings.ToLower(filepath.Clean(root)), strings.ToLower(filepath.Clean(path)))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Require an executable or an actual command-line path inside the project, not a PID or port guess.
func Belongs(root string, p Process) bool {
	if p.PID <= 4 || p.Created == "" || p.Executable == "" {
		return false
	}
	if filepath.Clean(root) == filepath.Dir(filepath.Clean(root)) {
		return false
	}
	if Inside(root, p.Executable) {
		return true
	}
	for _, arg := range commandArgs(p.CommandLine) {
		if Inside(root, arg) {
			return true
		}
	}
	return false
}

func Fingerprint(value any) string {
	b, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
