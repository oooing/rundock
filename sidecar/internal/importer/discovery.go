package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type StartupOption struct {
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
	Kind         string `json:"kind"`
	Command      string `json:"command,omitempty"`
	Recommended  bool   `json:"recommended"`
	score        int
}
type Discovery struct {
	Root      string          `json:"root"`
	Options   []StartupOption `json:"options"`
	Truncated bool            `json:"truncated"`
}

// Discover reads a bounded part of a project. It never executes files, installs
// dependencies, traverses links/junctions, or writes into the project.
func Discover(ctx context.Context, path string) (*Discovery, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("请选择文件夹或填写完整路径")
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("找不到这个路径，请确认文件或文件夹仍然存在")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	result := &Discovery{Root: root, Options: []StartupOption{}}
	if !info.IsDir() {
		if !supportedScript(root) {
			return nil, fmt.Errorf("请选择 .bat、.cmd、.ps1 启动脚本，或项目文件夹")
		}
		result.Options = append(result.Options, StartupOption{Path: root, RelativePath: filepath.Base(root), Kind: "script", Recommended: true})
		return result, nil
	}
	excluded := map[string]bool{"node_modules": true, "vendor": true, "target": true, "dist": true, "build": true, "coverage": true, "venv": true, "__pycache__": true, "work": true}
	visited := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			result.Truncated = true
			return nil
		}
		visited++
		if visited > 2000 {
			result.Truncated = true
			return fs.SkipAll
		}
		rel, _ := filepath.Rel(root, p)
		depth := len(strings.Split(rel, string(filepath.Separator))) - 1
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		name := strings.ToLower(d.Name())
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || excluded[name] || depth >= 3) {
				return fs.SkipDir
			}
			return nil
		}
		stat, e := d.Info()
		if e != nil || !stat.Mode().IsRegular() || stat.Size() > 1024*1024 {
			return nil
		}
		option := StartupOption{Path: p, RelativePath: rel, Kind: "script", score: 10 - depth}
		if supportedScript(p) {
			stem := strings.TrimSuffix(name, filepath.Ext(name))
			words := strings.FieldsFunc(stem, func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == ' ' })
			for _, word := range words {
				if containsWord([]string{"install", "uninstall", "deploy", "release", "publish", "build", "clean", "delete", "remove", "reset", "stop", "kill", "test"}, word) {
					return nil
				}
			}
			if len(words) > 0 && containsWord([]string{"start", "run", "dev", "serve", "launch"}, words[0]) {
				option.score = 100 - depth*10
			}
		} else if name == "package.json" {
			runner, script, e := packageStartup(p)
			if e != nil || script == "" {
				return nil
			}
			option.Kind = "package"
			option.Command = runner + " run " + script
			option.score = 60 - depth*10
		} else {
			return nil
		}
		result.Options = append(result.Options, option)
		if len(result.Options) >= 80 {
			result.Truncated = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result.Options, func(i, j int) bool {
		a, b := result.Options[i], result.Options[j]
		if a.score != b.score {
			return a.score > b.score
		}
		return a.RelativePath < b.RelativePath
	})
	if len(result.Options) > 20 {
		result.Options = result.Options[:20]
		result.Truncated = true
	}
	if len(result.Options) > 0 && result.Options[0].score >= 40 {
		result.Options[0].Recommended = true
	}
	return result, nil
}
func supportedScript(path string) bool {
	return containsWord([]string{".bat", ".cmd", ".ps1"}, strings.ToLower(filepath.Ext(path)))
}
func containsWord(words []string, value string) bool {
	for _, word := range words {
		if word == value {
			return true
		}
	}
	return false
}

func packageStartup(path string) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	var pkg struct {
		Scripts        map[string]string `json:"scripts"`
		PackageManager string            `json:"packageManager"`
	}
	if err = json.NewDecoder(io.LimitReader(f, 1024*1024)).Decode(&pkg); err != nil {
		return "", "", err
	}
	runner := "npm"
	manager := strings.Split(pkg.PackageManager, "@")[0]
	if manager != "" {
		if !containsWord([]string{"npm", "pnpm", "yarn"}, manager) {
			return "", "", fmt.Errorf("unsupported package manager")
		}
		runner = manager
	} else if fileExists(filepath.Join(filepath.Dir(path), "pnpm-lock.yaml")) {
		runner = "pnpm"
	} else if fileExists(filepath.Join(filepath.Dir(path), "yarn.lock")) {
		runner = "yarn"
	}
	for _, key := range []string{"dev", "start", "serve"} {
		if strings.TrimSpace(pkg.Scripts[key]) != "" {
			return runner, key, nil
		}
	}
	return runner, "", nil
}
