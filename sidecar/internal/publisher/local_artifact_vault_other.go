//go:build !windows

package publisher

import "os"

func openLocalArtifactReadOnly(path string) (*os.File, error) { return os.Open(path) }

func lockLocalArtifactDirectory(path string) (func(), error) {
	return lockRecoveryDirectory(path)
}
