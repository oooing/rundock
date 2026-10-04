package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/launcher-sidecar/internal/delivery"
)

type remoteAsset struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Size    int    `json:"size"`
	State   string `json:"state"`
	Digest  string `json:"digest"`
	Content []byte `json:"-"`
}
type remoteRelease struct {
	ID         int64  `json:"id"`
	Tag        string `json:"tag_name"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	URL        string `json:"html_url"`
}
type deliveryRemote struct {
	mu                                                sync.Mutex
	repo, account                                     string
	release                                           *remoteRelease
	assets                                            map[string]remoteAsset
	attempts, uploaded, creates, publishes, downloads int
	failUploadAt                                      int
	loseCreate, loseUpload, losePublish, omitDigest   bool
	tagConflict                                       bool
}

func (g *deliveryRemote) Identity(context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.account, nil
}
func remoteResult(value, out any) error {
	if out == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
func (g *deliveryRemote) JSON(ctx context.Context, method, path string, body, out any) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	base := "repos/fixture/project"
	if method == "GET" && path == base {
		return remoteResult(map[string]any{"full_name": "fixture/project", "default_branch": "main", "permissions": map[string]bool{"push": true}}, out)
	}
	if strings.Contains(path, "/contents/.github/workflows") {
		return &delivery.HTTPError{Status: 404}
	}
	if strings.Contains(path, "/git/ref/tags/") {
		tag, _ := url.PathUnescape(strings.Split(path, "/git/ref/tags/")[1])
		raw, err := exec.CommandContext(ctx, "git", "-C", g.repo, "rev-parse", "refs/tags/"+tag+"^{}").Output()
		if err != nil {
			return &delivery.HTTPError{Status: 404}
		}
		sha := strings.TrimSpace(string(raw))
		if g.tagConflict {
			sha = strings.Repeat("a", 40)
		}
		return remoteResult(map[string]any{"object": map[string]string{"type": "commit", "sha": sha}}, out)
	}
	if method == "GET" && strings.Contains(path, "/releases/tags/") {
		if g.release == nil {
			return &delivery.HTTPError{Status: 404}
		}
		return remoteResult(g.release, out)
	}
	if method == "GET" && strings.HasPrefix(path, base+"/releases?per_page=") {
		list := []*remoteRelease{}
		if g.release != nil {
			list = append(list, g.release)
		}
		return remoteResult(list, out)
	}
	if method == "POST" && path == base+"/releases" {
		if g.release != nil {
			return &delivery.HTTPError{Status: 422}
		}
		value := body.(map[string]any)
		g.creates++
		g.release = &remoteRelease{ID: 1, Tag: value["tag_name"].(string), Body: value["body"].(string), Draft: true, Prerelease: value["prerelease"].(bool), URL: "https://github.com/fixture/project/releases/tag/" + value["tag_name"].(string)}
		if g.loseCreate {
			return fmt.Errorf("simulated lost draft response")
		}
		return remoteResult(g.release, out)
	}
	if strings.Contains(path, "/releases/1/assets?") {
		list := []remoteAsset{}
		for _, asset := range g.assets {
			list = append(list, asset)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		return remoteResult(list, out)
	}
	if method == "DELETE" && strings.Contains(path, "/releases/assets/") {
		for name, a := range g.assets {
			if strings.HasSuffix(path, fmt.Sprint(a.ID)) {
				delete(g.assets, name)
			}
		}
		return nil
	}
	if path == base+"/releases/1" && g.release != nil {
		if method == "PATCH" {
			g.release.Draft = false
			g.publishes++
			if g.losePublish {
				return fmt.Errorf("simulated lost publication response")
			}
		}
		return remoteResult(g.release, out)
	}
	return fmt.Errorf("unexpected remote boundary: %s %s", method, path)
}
func (g *deliveryRemote) Upload(ctx context.Context, repo string, id int64, path, name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	g.attempts++
	if g.failUploadAt == g.attempts {
		return fmt.Errorf("simulated network interruption")
	}
	if _, exists := g.assets[name]; exists {
		return &delivery.HTTPError{Status: 422}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if g.omitDigest {
		digest = ""
	}
	g.uploaded++
	g.assets[name] = remoteAsset{ID: int64(g.uploaded), Name: name, Size: len(raw), State: "uploaded", Digest: digest, Content: raw}
	if g.loseUpload {
		return fmt.Errorf("simulated lost upload response")
	}
	return nil
}
func (g *deliveryRemote) Digest(ctx context.Context, path string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.downloads++
	for _, a := range g.assets {
		if strings.HasSuffix(path, fmt.Sprint(a.ID)) {
			sum := sha256.Sum256(a.Content)
			return hex.EncodeToString(sum[:]), nil
		}
	}
	return "", &delivery.HTTPError{Status: 404}
}
