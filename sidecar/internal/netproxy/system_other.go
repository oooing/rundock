//go:build !windows

package netproxy

func SystemCandidates(string) []string { return nil }
