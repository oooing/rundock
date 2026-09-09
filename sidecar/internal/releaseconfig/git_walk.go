package releaseconfig

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Git is the authority for tracked and non-ignored files, including nested
// .gitignore rules, negations and .git/info/exclude. Do not approximate them.
// A Git failure must not silently widen discovery to ignored local directories.
func discoveryWalk(ctx context.Context, root string, repoFound bool, visit func(string, fs.DirEntry) error) []string {
	if !repoFound {
		return safeWalk(root, visit)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output := &scanOutput{}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		return []string{"无法读取 Git 文件范围，已停止自动识别；请检查 Git 或使用显式发布配置"}
	}
	paths := map[string]bool{}
	for _, path := range strings.Split(output.String(), "\x00") {
		if path != "" {
			paths[path] = true
		}
	}
	if len(paths) > 100000 {
		return []string{"Git 文件超过自动扫描上限，请使用显式发布配置"}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	warnings := []string{}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return []string{"无法解析项目目录，已停止自动识别"}
	}
	for _, relativePath := range ordered {
		if ctx.Err() != nil {
			warnings = append(warnings, "自动扫描超时，请检查结果或使用显式发布配置")
			break
		}
		if validateRelative(relativePath) != nil {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(relativePath))
		entry, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		} // Deleted tracked files are not candidates.
		if err != nil {
			warnings = append(warnings, "无法扫描 "+relativePath+"："+err.Error())
			continue
		}
		if !entry.Mode().IsRegular() {
			continue
		} // No submodules or file symlinks.
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(canonicalRoot, resolved)
		if err != nil || validateRelative(rel) != nil {
			continue
		} // No junction escape.
		if err := visit(path, fs.FileInfoToDirEntry(entry)); err != nil {
			warnings = append(warnings, "无法扫描 "+relativePath+"："+err.Error())
		}
	}
	return sortedUnique(warnings)
}

// Bound subprocess output even for unusually large repositories.
type scanOutput struct{ bytes.Buffer }

func (b *scanOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 16*1024*1024 {
		return 0, fmt.Errorf("Git scan output limit reached")
	}
	return b.Buffer.Write(p)
}
