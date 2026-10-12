//go:build windows

package netproxy

import (
	"net/url"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func SystemCandidates(target string) []string {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return nil
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil {
		return nil
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil
	}
	bypass, _, _ := key.GetStringValue("ProxyOverride")
	if Bypass(u, strings.ReplaceAll(bypass, ";", ",")) {
		return nil
	}
	return parseSystemProxy(server, u.Scheme)
}
