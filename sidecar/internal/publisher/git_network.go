package publisher

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/launcher-sidecar/internal/netproxy"
)

type networkReadyRunner interface {
	CheckNetwork(context.Context, string, string) (string, error)
}

func (execRunner) CheckNetwork(ctx context.Context, repo, remote string) (string, error) {
	_, notice, err := prepareGitNetwork(ctx, repo, []string{"-C", repo, "push", remote}, true)
	return notice, err
}

func gitNetworkCommand(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "-C" || args[i] == "-c" {
			i++
			continue
		}
		return contains([]string{"fetch", "ls-remote", "push"}, args[i])
	}
	return false
}

type gitNetworkRead func(context.Context, ...string) (string, error)

func prepareGitNetwork(ctx context.Context, repo string, args []string, forceProbe bool) ([]string, string, error) {
	read := func(ctx context.Context, argv ...string) (string, error) {
		return runCommand(ctx, repo, "git", nil, argv...)
	}
	return prepareGitNetworkWith(ctx, repo, args, forceProbe, read, netproxy.SystemCandidates)
}

func prepareGitNetworkWith(ctx context.Context, repo string, args []string, forceProbe bool, read gitNetworkRead, system func(string) []string) ([]string, string, error) {
	if len(args) < 4 || args[0] != "-C" {
		return args, "", nil
	}
	operation := args[2]
	index := 3
	for index < len(args) && strings.HasPrefix(args[index], "-") {
		if !contains([]string{"--tags", "--heads", "--refs", "--symref", "--exit-code", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--refmap="}, args[index]) {
			return args, "", nil
		}
		index++
	}
	if index == len(args) {
		return args, "", nil
	}
	remote := args[index]
	remoteURL := remote
	if u, err := url.Parse(remote); err != nil || u.Scheme == "" {
		lookup := []string{"-C", repo, "remote", "get-url", "--all"}
		if operation == "push" {
			lookup = append(lookup, "--push")
		}
		lookup = append(lookup, remote)
		value, err := read(ctx, lookup...)
		if err != nil {
			return args, "", nil
		}
		urls := strings.Fields(value)
		// Multiple push destinations retain their original transport policy.
		if len(urls) != 1 {
			return args, "", nil
		}
		remoteURL = urls[0]
	}
	u, err := url.Parse(remoteURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return args, "", nil
	}
	environment, _, bypass := netproxy.CurrentEnvironment(remoteURL)
	if bypass {
		return args, "", nil
	}
	lookup := func(keys ...string) (string, bool) {
		v, e := read(ctx, append([]string{"-C", repo, "config"}, keys...)...)
		return strings.TrimSpace(v), e == nil
	}
	current, present := lookup("--get", "remote."+remote+".proxy")
	if !present {
		current, present = lookup("--get-urlmatch", "http.proxy", remoteURL)
	}
	disabled := present && current == ""
	if disabled && !forceProbe {
		return args, "", nil
	} // Explicitly disabled proxying.
	if !present {
		current = environment
	}
	candidates := system(remoteURL)
	useSystem := !present && current == "" && len(candidates) > 0
	if useSystem {
		current = candidates[0]
	}
	if !disabled && environment != "" {
		candidates = append(candidates, environment)
	}
	if global, ok := lookup("--global", "--get-urlmatch", "http.proxy", remoteURL); !disabled && ok && global != "" {
		candidates = append(candidates, global)
	}
	override := func(proxy string, command []string) []string {
		config := []string{"-c", "http." + remoteURL + ".proxy=" + proxy}
		if remote != remoteURL {
			config = append(config, "-c", "remote."+remote+".proxy="+proxy)
		}
		return append(config, command...)
	}
	probe := func(ctx context.Context, proxy string) error {
		query := []string{"-C", repo, "ls-remote", remoteURL, "HEAD"}
		if proxy != "" || disabled {
			query = override(proxy, query)
		}
		out, e := read(ctx, query...)
		if e != nil {
			if strings.TrimSpace(out) == "" {
				out = e.Error()
			}
			return fmt.Errorf("连接代码仓库失败：%s: %w", redact(strings.TrimSpace(out)), e)
		}
		return nil
	}
	selected, changed, err := netproxy.Resolve(ctx, current, candidates, probe)
	if err != nil {
		return nil, "", err
	}
	if (forceProbe || useSystem) && !changed {
		if err := probe(ctx, selected); err != nil {
			return nil, "", err
		}
	}
	if changed || useSystem {
		return override(selected, args), fmt.Sprintf("已检查并使用当前本机代理 %s；仅本次连接生效，不修改项目或全局代理设置。", netproxy.Endpoint(selected)), nil
	}
	return args, "", nil
}
