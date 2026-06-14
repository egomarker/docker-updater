package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	projectlock "github.com/egomarker/docker-updater/internal/lock"
	"github.com/egomarker/docker-updater/internal/model"
)

// minimalEmptyZip is a valid empty zip archive: just the end-of-central-directory
// record (22 bytes). Used as a fallback when every source is empty or fully
// excluded, so the contract (a zip always exists after a successful backup) holds.
var minimalEmptyZip = []byte{
	0x50, 0x4b, 0x05, 0x06, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
}

func (s *Service) runBackup(meta *model.JobMeta, project model.ProjectConfig) {
	ctx := context.Background()
	backup := project.Backup
	if backup == nil {
		s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhasePreflight, "backup not configured for project", nil))
		return
	}
	s.appendLogf(meta, "JOB running kind=%s project=%s", meta.Kind, meta.ProjectID)

	lockHandle, err := projectlock.Acquire(s.lockPath(meta.ProjectID), meta.JobID)
	if err != nil {
		s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhasePreflight, "acquire project lock failed", err))
		return
	}
	defer func() {
		if releaseErr := lockHandle.Release(); releaseErr != nil {
			s.appendLogf(meta, "WARN project lock release failed: %v", releaseErr)
		}
	}()

	// Preflight
	s.appendLogf(meta, "STEP %s", model.JobPhasePreflight)
	if err := requireExecutable(s.cfg.Executables.Zip); err != nil {
		s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhasePreflight, "zip executable missing", err))
		return
	}
	for _, src := range backup.Sources {
		if err := requireDir(src); err != nil {
			s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhasePreflight, fmt.Sprintf("backup source missing: %s", src), err))
			return
		}
	}
	if err := os.MkdirAll(backup.Destination, 0o755); err != nil {
		s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhasePreflight, "create destination dir failed", err))
		return
	}

	// Zip
	s.setPhase(meta, model.JobPhaseZip)
	outputName := fmt.Sprintf("%s-backup-%sZ.zip", meta.ProjectID, time.Now().UTC().Format("20060102-150405"))
	finalPath := filepath.Join(backup.Destination, outputName)
	tempPath := filepath.Join(backup.Destination, "."+outputName+".tmp")
	_ = os.Remove(tempPath) // clear any stale temp from a crashed run

	globs := buildExcludeGlobs(backup.Exclude)
	baseArgs := []string{s.cfg.Executables.Zip, "-r"}
	if meta.Backup.Symlinks == "store" {
		baseArgs = append(baseArgs, "-y")
	}
	baseArgs = append(baseArgs, tempPath)

	for _, src := range backup.Sources {
		parent := filepath.Dir(src)
		base := filepath.Base(src)
		// Run from the source's parent dir using its base name so stored
		// entries are clean: <source>/... rather than absolute paths.
		argv := append([]string{}, baseArgs...)
		argv = append(argv, base)
		if len(globs) > 0 {
			argv = append(argv, "-x")
			argv = append(argv, globs...)
		}
		if err := s.runZipStep(ctx, meta, parent, argv); err != nil {
			_ = os.Remove(tempPath)
			s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhaseZip, "zip failed", err))
			return
		}
	}

	// If every source was empty or fully excluded, seed a valid empty archive so
	// a zip always exists after a successful job.
	if _, statErr := os.Stat(tempPath); statErr != nil {
		if !os.IsNotExist(statErr) {
			_ = os.Remove(tempPath)
			s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhaseZip, "stat backup archive failed", statErr))
			return
		}
		if err := os.WriteFile(tempPath, minimalEmptyZip, 0o644); err != nil {
			_ = os.Remove(tempPath)
			s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhaseZip, "create empty backup archive failed", err))
			return
		}
		s.appendLogf(meta, "No files to archive; created empty backup zip")
	}

	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhaseZip, "rename backup archive failed", err))
		return
	}

	var outputBytes int64
	if info, err := os.Stat(finalPath); err == nil {
		outputBytes = info.Size()
	}
	meta.Backup.OutputZip = outputName
	meta.Backup.OutputBytes = outputBytes
	s.updateMeta(meta)

	// Retention
	s.setPhase(meta, model.JobPhaseRetention)
	remaining, removed := s.applyRetention(meta, backup.Destination)
	meta.Backup.Remaining = remaining
	meta.Backup.Removed = removed
	s.updateMeta(meta)

	s.finish(meta, model.JobStatusSuccess, nil)
}

// runZipStep runs one zip invocation. Exit code 0 and 12 ("Nothing to do!")
// are both treated as success; any other outcome is a failure.
func (s *Service) runZipStep(ctx context.Context, meta *model.JobMeta, cwd string, argv []string) error {
	result, runErr := s.runLogged(ctx, meta, model.JobPhaseZip, cwd, argv)
	exitCode := -1
	if result != nil {
		exitCode = result.ExitCode
	}
	if exitCode == 0 || exitCode == 12 {
		return nil
	}
	err := runErr
	if err == nil {
		err = fmt.Errorf("unexpected zip exit code %d", exitCode)
	}
	return s.commandError(model.JobPhaseZip, "zip failed", argv, err)
}

// buildExcludeGlobs translates raw exclude entries into Info-ZIP glob patterns.
// Each entry `x` becomes two patterns — the folder entry and its contents — so
// the folder is dropped at any depth in any source tree.
func buildExcludeGlobs(entries []string) []string {
	var globs []string
	for _, entry := range entries {
		trimmed := strings.Trim(strings.TrimSpace(entry), "/")
		if trimmed == "" {
			continue
		}
		globs = append(globs, "*/"+trimmed)
		globs = append(globs, "*/"+trimmed+"/*")
	}
	return globs
}

// applyRetention prunes old backups in destination down to the configured retain
// count. Only files matching <project>-backup-*.zip are considered, so the
// destination may safely hold unrelated files. Deletion errors are logged but
// never fail the job. Returns the remaining count and the number removed.
func (s *Service) applyRetention(meta *model.JobMeta, destination string) (remaining, removed int) {
	retain := meta.Backup.Retain
	prefix := meta.ProjectID + "-backup-"

	entries, err := os.ReadDir(destination)
	if err != nil {
		s.appendLogf(meta, "WARN retention: read destination failed: %v", err)
		return 0, 0
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".zip") {
			names = append(names, name)
		}
	}

	if retain <= 0 {
		s.appendLogf(meta, "Retention: retain=0 (unlimited); %d backup(s) present", len(names))
		return len(names), 0
	}

	sort.Strings(names) // ascending => oldest first

	if len(names) <= retain {
		s.appendLogf(meta, "Retention: %d backup(s) <= retain=%d; nothing to remove", len(names), retain)
		return len(names), 0
	}

	toRemove := names[:len(names)-retain]
	s.appendLogf(meta, "Retention: %d backup(s) > retain=%d; removing %d oldest", len(names), retain, len(toRemove))
	for _, name := range toRemove {
		path := filepath.Join(destination, name)
		if err := os.Remove(path); err != nil {
			s.appendLogf(meta, "WARN retention: failed to delete %s: %v", name, err)
			continue
		}
		s.appendLogf(meta, "Retention removed old backup: %s", name)
		removed++
	}
	remaining = len(names) - removed
	return remaining, removed
}
