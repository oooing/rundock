package publisher

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Read raw Git blobs instead of checking them out through repository filters,
// export attributes or working-tree encodings. The checked bytes are the bytes
// in the accepted tree, including version and lock files.
func materializeCandidate(ctx context.Context, cand *releaseCandidate) error {
	raw, err := gitOutput(ctx, cand.RepoRoot, "ls-tree", "-rz", "--full-tree", cand.HeadSHA)
	if err != nil {
		return err
	}
	type entry struct{ mode, sha, path string }
	entries := []entry{}
	var input strings.Builder
	for _, record := range strings.Split(raw, "\x00") {
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\t", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid Git tree entry")
		}
		fields := strings.Fields(parts[0])
		if len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			return &Error{Code: "candidate_unsupported", Message: "候选暂不支持子模块或符号链接"}
		}
		rel, err := secureCandidateRel(parts[1])
		if err != nil {
			return err
		}
		entries = append(entries, entry{fields[0], fields[2], rel})
		input.WriteString(fields[2] + "\n")
	}
	cand.FileModes = map[string]string{}
	cmd := exec.CommandContext(ctx, "git", "-C", cand.RepoRoot, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(input.String())
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	reader := bufio.NewReader(pipe)
	for _, item := range entries {
		header, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != item.sha || fields[1] != "blob" {
			return fmt.Errorf("invalid Git blob response")
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 {
			return fmt.Errorf("invalid Git blob size")
		}
		path := filepath.Join(cand.Work, filepath.FromSlash(item.path))
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if item.mode == "100755" {
			mode = 0755
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(f, reader, size)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		separator, err := reader.ReadByte()
		if err != nil || separator != '\n' {
			return fmt.Errorf("invalid Git blob delimiter")
		}
		cand.FileModes[item.path] = item.mode
	}
	err = cmd.Wait()
	done = true
	return err
}

func stageCandidateBlob(cand *releaseCandidate, rel string) error {
	path, err := secureProjectPath(cand.Work, rel, false)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if isLFSPointer(raw) {
		return &Error{Code: "lfs_unsupported", Message: "候选暂不支持 Git LFS：" + rel}
	}
	cmd := exec.Command("git", "-C", cand.RepoRoot, "hash-object", "-w", "--no-filters", "--stdin")
	cmd.Stdin = bytes.NewReader(raw)
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	mode := cand.FileModes[rel]
	if mode == "" {
		mode = "100644"
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0111 != 0 {
		mode = "100755"
	}
	_, err = isolatedGit(context.Background(), cand, "update-index", "--add", "--cacheinfo", mode, strings.TrimSpace(string(out)), rel)
	return err
}
