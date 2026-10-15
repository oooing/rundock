package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Bound disk concurrency and memory, even for thousands of large local files.
// Missing-file markers and SHA-256 bytes match the existing snapshot format.
func fileHashesContext(ctx context.Context, repo string, changes []FileChange, excluded map[string]bool) (map[string]string, error) {
	results := make([]string, len(changes))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			buffer := make([]byte, 64*1024)
			for index := range jobs {
				if ctx.Err() != nil {
					continue
				}
				file, err := os.Open(filepath.Join(repo, filepath.FromSlash(changes[index].Path)))
				if err != nil {
					results[index] = "<missing>"
					continue
				}
				hash := sha256.New()
				_, err = io.CopyBuffer(hash, contextHashReader{ctx, file}, buffer)
				_ = file.Close()
				if err != nil {
					results[index] = "<missing>"
					continue
				}
				results[index] = hex.EncodeToString(hash.Sum(nil))
			}
		}()
	}
dispatch:
	for index, change := range changes {
		if excluded[filepath.ToSlash(change.Path)] {
			continue
		}
		select {
		case jobs <- index:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(changes))
	for index, change := range changes {
		if !excluded[filepath.ToSlash(change.Path)] {
			out[filepath.ToSlash(change.Path)] = results[index]
		}
	}
	return out, nil
}

type contextHashReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextHashReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
