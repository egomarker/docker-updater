package update

import (
	"context"

	projectlock "github.com/egomarker/docker-updater/internal/lock"
	"github.com/egomarker/docker-updater/internal/model"
)

func (s *Service) runRestart(meta *model.JobMeta, project model.ProjectConfig) {
	ctx := context.Background()
	s.appendLogf(meta, "JOB running kind=%s project=%s", meta.Kind, meta.ProjectID)

	lockHandle, err := projectlock.Acquire(s.lockPath(meta.ProjectID), meta.JobID)
	if err != nil {
		s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhaseRestart, "acquire project lock failed", err))
		return
	}
	defer func() {
		if releaseErr := lockHandle.Release(); releaseErr != nil {
			s.appendLogf(meta, "WARN project lock release failed: %v", releaseErr)
		}
	}()

	if err := s.composeRestart(ctx, meta, project); err != nil {
		s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhaseRestart, "docker compose restart failed", err))
		return
	}

	s.finish(meta, model.JobStatusSuccess, nil)
}
