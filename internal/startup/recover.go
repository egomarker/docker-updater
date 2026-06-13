package startup

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egomarker/docker-updater/internal/jobs"
	"github.com/egomarker/docker-updater/internal/model"
)

func RecoverRunningJobs(store *jobs.Store, runtimeRoot string) error {
	running, err := store.ListRunningJobs()
	if err != nil {
		return fmt.Errorf("list running jobs: %w", err)
	}

	for _, meta := range running {
		now := time.Now().UTC()
		meta.Status = model.JobStatusInterrupted
		meta.Phase = model.JobPhaseDone
		meta.FinishedAt = &now
		if err := store.AppendLog(meta.ProjectID, meta.JobID, "JOB interrupted during startup recovery"); err != nil {
			return fmt.Errorf("append interrupted log for %s/%s: %w", meta.ProjectID, meta.JobID, err)
		}
		if err := store.UpdateMeta(meta); err != nil {
			return fmt.Errorf("update interrupted meta for %s/%s: %w", meta.ProjectID, meta.JobID, err)
		}
	}

	locksDir := filepath.Join(runtimeRoot, "locks")
	if err := os.RemoveAll(locksDir); err != nil {
		return fmt.Errorf("remove stale lock dir: %w", err)
	}
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		return fmt.Errorf("recreate lock dir: %w", err)
	}

	return nil
}
