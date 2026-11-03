// Package accesslink describes saved browser shortcuts, not additional processes.
package accesslink

import (
	"fmt"
	"github.com/launcher-sidecar/internal/probe"
	"net/url"
	"strings"
)

type Link struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func Normalize(links []Link) ([]Link, error) {
	out := []Link{}
	seen := map[string]bool{}
	for _, link := range links {
		link.Name, link.URL = strings.TrimSpace(link.Name), strings.TrimSpace(link.URL)
		if link.Name == "" || !probe.LocalWebURL(link.URL) {
			return nil, fmt.Errorf("请填写入口名称和本机 http(s) 地址")
		}
		u, _ := url.Parse(link.URL)
		if u.Path == "" {
			u.Path = "/"
		}
		link.URL = u.String()
		if !seen[link.URL] {
			out = append(out, link)
			seen[link.URL] = true
		}
	}
	return out, nil
}
