package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	projectlock "github.com/egomarker/docker-updater/internal/lock"
	"github.com/egomarker/docker-updater/internal/model"
)

func (s *Service) runDeploy(meta *model.JobMeta, project model.ProjectConfig, request model.DeployRequest) {
	ctx := context.Background()
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

	s.appendLogf(meta, "STEP %s", model.JobPhasePreflight)
	if err := s.preflightDeploy(project, request); err != nil {
		s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhasePreflight, "preflight failed", err))
		return
	}

	s.setPhase(meta, model.JobPhaseSnapshot)
	preBranch, err := s.gitCurrentBranch(ctx, meta, project)
	if err != nil {
		s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhaseSnapshot, "snapshot branch failed", err))
		return
	}
	preCommit, err := s.gitCurrentCommitShort(ctx, meta, project, model.JobPhaseSnapshot)
	if err != nil {
		s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhaseSnapshot, "snapshot commit failed", err))
		return
	}
	meta.Repo.PreBranch = model.StringPtr(preBranch)
	meta.Repo.PreCommit = model.StringPtr(preCommit)
	s.updateMeta(meta)

	imageID, err := s.currentPrimaryServiceImageID(ctx, meta, project)
	if err != nil {
		s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhaseSnapshot, "snapshot runtime image failed", err))
		return
	}
	if imageID != "" {
		rollbackTag := rollbackTagFor(project.Build.LatestTag, meta.JobID)
		if err := s.tagRollbackImage(ctx, meta, project, imageID, rollbackTag); err != nil {
			s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhaseSnapshot, "tag rollback image failed", err))
			return
		}
		meta.Image.PredeployImageID = model.StringPtr(imageID)
		meta.Image.RollbackTag = model.StringPtr(rollbackTag)
		s.updateMeta(meta)
	} else {
		s.appendLog(meta, "No current runtime image found; rollback snapshot unavailable")
	}

	s.setPhase(meta, model.JobPhaseGitFetch)
	if err := s.gitFetch(ctx, meta, project, request.Origin); err != nil {
		s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhaseGitFetch, "git fetch failed", err))
		return
	}

	s.setPhase(meta, model.JobPhaseGitCheckout)
	if err := s.gitCheckout(ctx, meta, project, request.Origin, request.Branch); err != nil {
		s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhaseGitCheckout, "git checkout failed", err))
		return
	}

	s.setPhase(meta, model.JobPhaseGitPull)
	if err := s.gitPull(ctx, meta, project, request.Origin, request.Branch); err != nil {
		s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhaseGitPull, "git pull failed", err))
		return
	}

	postCommit, err := s.gitCurrentCommitShort(ctx, meta, project, model.JobPhaseGitPull)
	if err != nil {
		s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhaseGitPull, "read post-pull commit failed", err))
		return
	}
	candidateTag := project.Build.CommitTagPrefix + postCommit
	meta.Repo.PostCommit = model.StringPtr(postCommit)
	meta.Image.CandidateCommitTag = model.StringPtr(candidateTag)
	s.updateMeta(meta)

	s.setPhase(meta, model.JobPhaseBuild)
	if err := s.buildImage(ctx, meta, project, request, candidateTag); err != nil {
		s.finish(meta, model.JobStatusFailed, s.normalizeJobError(model.JobPhaseBuild, "docker build failed", err))
		return
	}

	s.setPhase(meta, model.JobPhaseDeploy)
	if err := s.composeUp(ctx, meta, project, model.JobPhaseDeploy); err != nil {
		originalErr := s.normalizeJobError(model.JobPhaseDeploy, "docker compose up failed", err)
		if meta.Image.RollbackTag == nil || *meta.Image.RollbackTag == "" {
			s.finish(meta, model.JobStatusFailed, originalErr)
			return
		}

		s.setPhase(meta, model.JobPhaseRollback)
		if err := s.retagRollbackToLatest(ctx, meta, project, *meta.Image.RollbackTag); err != nil {
			s.finish(meta, model.JobStatusRollbackFailed, s.normalizeJobError(model.JobPhaseRollback, "rollback retag failed", err))
			return
		}
		if err := s.composeUp(ctx, meta, project, model.JobPhaseRollback); err != nil {
			s.finish(meta, model.JobStatusRollbackFailed, s.normalizeJobError(model.JobPhaseRollback, "rollback compose up failed", err))
			return
		}
		meta.Image.ActiveTag = model.StringPtr(project.Build.LatestTag)
		s.finish(meta, model.JobStatusRolledBack, originalErr)
		return
	}

	meta.Image.ActiveTag = model.StringPtr(candidateTag)
	s.finish(meta, model.JobStatusSuccess, nil)
}

func (s *Service) preflightDeploy(project model.ProjectConfig, request model.DeployRequest) error {
	if !project.Git.AllowsOrigin(request.Origin) {
		return s.stepError(model.JobPhasePreflight, "origin is not allowed for project", nil)
	}
	if !project.Git.AllowsBranch(request.Branch) {
		return s.stepError(model.JobPhasePreflight, "branch does not match allowlist", nil)
	}
	if err := requireDir(project.RepoDir); err != nil {
		return s.stepError(model.JobPhasePreflight, "repo dir missing", err)
	}
	if err := requireDir(project.Build.Cwd); err != nil {
		return s.stepError(model.JobPhasePreflight, "build cwd missing", err)
	}
	if err := requireDir(project.Compose.Cwd); err != nil {
		return s.stepError(model.JobPhasePreflight, "compose cwd missing", err)
	}
	if err := requireDir(project.Build.ContextDir); err != nil {
		return s.stepError(model.JobPhasePreflight, "build context dir missing", err)
	}
	if strings.TrimSpace(project.Build.Dockerfile) != "" {
		if err := requireFile(project.Build.Dockerfile); err != nil {
			return s.stepError(model.JobPhasePreflight, "dockerfile missing", err)
		}
	}
	for _, composeFile := range project.Compose.Files {
		if err := requireFile(composeFile); err != nil {
			return s.stepError(model.JobPhasePreflight, fmt.Sprintf("compose file missing: %s", composeFile), err)
		}
	}
	if err := requireExecutable(s.cfg.Executables.Git); err != nil {
		return s.stepError(model.JobPhasePreflight, "git executable missing", err)
	}
	if err := requireExecutable(s.cfg.Executables.Docker); err != nil {
		return s.stepError(model.JobPhasePreflight, "docker executable missing", err)
	}
	return nil
}

func requireDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

func requireFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	return nil
}

func requireExecutable(path string) error {
	if filepath.IsAbs(path) {
		return requireFile(path)
	}
	_, err := exec.LookPath(path)
	return err
}

func rollbackTagFor(latestTag, jobID string) string {
	base := latestTag
	lastSlash := strings.LastIndex(base, "/")
	lastColon := strings.LastIndex(base, ":")
	if lastColon > lastSlash {
		base = base[:lastColon]
	}
	return base + ":rollback-" + jobID
}
