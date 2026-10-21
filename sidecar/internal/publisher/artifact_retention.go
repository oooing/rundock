package publisher

import (
	"context"
	"time"
)

func (s *Service) pruneSavedArtifacts(ctx context.Context, now time.Time) error {
	candidates, err := s.delivery.RetentionCandidates(ctx, now)
	if err != nil {
		return err
	}
	var cleanupError error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !s.reserve(candidate.RepoRoot) {
			continue
		}
		cleaned, err := s.delivery.PrunePublishedArtifacts(ctx, candidate, now)
		s.release(candidate.RepoRoot)
		if err != nil {
			// One occupied or damaged batch must not starve other old copies.
			// Leave this batch resumable for a later maintenance pass.
			if cleanupError == nil {
				cleanupError = err
			}
			continue
		}
		if cleaned {
			s.log(candidate.RunID, "info", "已自动清理超过 30 天且不在最近 3 次成功发布内的本地安装包（"+candidate.GroupID+"）；发布记录、核对信息和 GitHub 下载地址仍保留。")
		}
	}
	return cleanupError
}
