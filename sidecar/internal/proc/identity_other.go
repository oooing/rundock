//go:build !windows

package proc

func (h *Handle) IdentityCreated() string { return "" }
