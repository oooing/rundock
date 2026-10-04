package recovery

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/store"
)

type PortRange struct {
	Start, End int
	Family     string
}
type PortBinding struct {
	Port          int
	Network, Host string
}

type BindFailure struct {
	Binding      PortBinding
	Occupied     bool
	AccessDenied bool
	Err          error
}

func (e *BindFailure) Error() string {
	return fmt.Sprintf("端口 %d 当前无法绑定，请重新检查占用程序；未关闭任何程序", e.Binding.Port)
}
func (e *BindFailure) Unwrap() error { return e.Err }

func BindingExcluded(b PortBinding, ranges []PortRange) bool {
	for _, r := range ranges {
		if (r.Family == "" && b.Network == "tcp4" || r.Family == b.Network) && b.Port >= r.Start && b.Port <= r.End {
			return true
		}
	}
	return false
}

var endpointDeclaration = regexp.MustCompile(`(?im)^\s*(?:rem\s+|::\s*|#\s*)rundock:(?:ready|open)\s+(https?://[^\s]+)`)

func RequiredBindings(a *store.App) []PortBinding {
	byPort := map[int][]PortBinding{}
	add := func(link string) {
		u, err := url.Parse(link)
		if err != nil {
			return
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil || port <= 0 || port > 65535 {
			return
		}
		host := u.Hostname()
		network := "tcp4"
		if host == "localhost" {
			host = "127.0.0.1"
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return
		}
		if ip.To4() == nil {
			network = "tcp6"
		}
		binding := PortBinding{port, network, host}
		for _, old := range byPort[port] {
			if old == binding {
				return
			}
		}
		byPort[port] = append(byPort[port], binding)
	}
	if raw, err := os.ReadFile(a.EntryScript); err == nil && len(raw) <= 1024*1024 {
		for _, m := range endpointDeclaration.FindAllStringSubmatch(string(raw), -1) {
			add(m[1])
		}
	}
	add(a.HealthURL)
	result := []PortBinding{}
	for _, port := range RequiredPorts(a) {
		bindings := byPort[port]
		if len(bindings) == 0 {
			bindings = []PortBinding{{Port: port, Network: "tcp4", Host: "0.0.0.0"}}
		}
		result = append(result, bindings...)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Port == result[j].Port {
			if result[i].Network == result[j].Network {
				return result[i].Host < result[j].Host
			}
			return result[i].Network < result[j].Network
		}
		return result[i].Port < result[j].Port
	})
	return result
}

func ListenerConflicts(b PortBinding, l probe.PortListener) bool {
	if b.Port != l.Port {
		return false
	}
	if l.Addr == "" {
		return b.Network == "tcp4"
	}
	host, _, err := net.SplitHostPort(l.Addr)
	if err != nil {
		return false
	}
	address := net.ParseIP(strings.Trim(host, "[]"))
	if address == nil {
		return false
	}
	if (address.To4() == nil) != (b.Network == "tcp6") {
		return false
	}
	return address.IsUnspecified() || b.Host == "0.0.0.0" || b.Host == "::" || address.Equal(net.ParseIP(b.Host))
}

// IPv6 wildcard sockets may be dual-stack or IPv6-only. Their address alone
// cannot authorize closing the owner of an IPv4 project port.
func PotentialDualStack(b PortBinding, l probe.PortListener) bool {
	if b.Network != "tcp4" || b.Port != l.Port {
		return false
	}
	host, _, err := net.SplitHostPort(l.Addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() == nil && ip.IsUnspecified()
}

func BindingsConflict(bindings []PortBinding, l probe.PortListener) bool {
	for _, b := range bindings {
		if ListenerConflicts(b, l) {
			return true
		}
	}
	return false
}

// ProbeBindings is a bounded pre-start check, never a port reservation.
// A listener may appear after it returns; the real project's bind remains final.
func ProbeBindings(bindings []PortBinding) ([]int, error) {
	reserved := []int{}
	seen := map[int]bool{}
	for _, binding := range bindings {
		port := binding.Port
		ln, err := net.Listen(binding.Network, net.JoinHostPort(binding.Host, strconv.Itoa(port)))
		if err == nil {
			_ = ln.Close()
			continue
		}
		if socketUnsupported(err) {
			return reserved, fmt.Errorf("项目端口 %d 所需的本机地址不可用，请检查项目地址配置", port)
		}
		if socketAccessDenied(err) {
			if !seen[port] {
				reserved = append(reserved, port)
				seen[port] = true
			}
			return reserved, &BindFailure{Binding: binding, AccessDenied: true, Err: err}
		}
		return reserved, &BindFailure{Binding: binding, Occupied: socketInUse(err), Err: err}
	}
	return reserved, nil
}
