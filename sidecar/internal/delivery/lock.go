package delivery

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Lock is released by the OS on process exit; stale lock files have no meaning.
func Lock(root, key string) (func(), error) {
	dir := filepath.Join(root, "locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%x.lock", sha256.Sum256([]byte(key)))
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { f.Close() }) }, nil
}
