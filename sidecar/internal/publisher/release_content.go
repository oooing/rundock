package publisher

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Compare with release tags, not the remote branch: pushed code may still be unreleased.
func (s *Service) compareReleaseContent(ctx context.Context, pf *Preflight, remote map[string]string) {
	pf.CommitsSinceTags = map[string]int{}
	tags := map[string]bool{pf.LatestTag: true, "": true}
	for _, tag := range pf.LatestGroupTags {
		tags[tag] = true
	}
	ctx, cancel := commandContext(ctx, 10*time.Second)
	defer cancel()
	for tag := range tags {
		revision := pf.HeadSHA
		if tag != "" {
			base := "refs/tags/" + tag
			if sha := remote[tag]; sha != "" {
				base = sha
			}
			revision = base + ".." + pf.HeadSHA
		}
		count, err := s.git(ctx, pf.RepoRoot, "rev-list", "--count", revision, "--")
		if err != nil {
			continue
		} // Unknown must not be represented as zero.
		n, err := strconv.Atoi(strings.TrimSpace(count))
		if err == nil && n >= 0 {
			pf.CommitsSinceTags[tag] = n
		}
	}
}

func parseTagRevisions(raw string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "refs/tags/") {
			continue
		}
		tag := strings.TrimPrefix(parts[1], "refs/tags/")
		if !strings.HasSuffix(tag, "^{}") {
			result[tag] = parts[0]
		}
	}
	// Prefer the peeled commit of an annotated tag, regardless of line order.
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "refs/tags/") || !strings.HasSuffix(parts[1], "^{}") {
			continue
		}
		result[strings.TrimSuffix(strings.TrimPrefix(parts[1], "refs/tags/"), "^{}")] = parts[0]
	}
	return result
}
