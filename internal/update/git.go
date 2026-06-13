package update

import (
	"context"
	"fmt"
	"strings"

	"github.com/egomarker/docker-updater/internal/model"
)

func (s *Service) gitCurrentBranch(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig) (string, error) {
	argv := []string{s.cfg.Executables.Git, "rev-parse", "--abbrev-ref", "HEAD"}
	result, err := s.captureLogged(ctx, meta, model.JobPhaseSnapshot, project.RepoDir, argv)
	if err != nil {
		return "", s.commandError(model.JobPhaseSnapshot, "git current branch failed", argv, err)
	}
	branch := strings.TrimSpace(result.Output)
	if branch == "" {
		return "", s.stepError(model.JobPhaseSnapshot, "git current branch returned empty output", nil)
	}
	return branch, nil
}

func (s *Service) gitCurrentCommitShort(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig, step model.JobPhase) (string, error) {
	argv := []string{s.cfg.Executables.Git, "rev-parse", "--short=8", "HEAD"}
	result, err := s.captureLogged(ctx, meta, step, project.RepoDir, argv)
	if err != nil {
		return "", s.commandError(step, "git current commit failed", argv, err)
	}
	commit := strings.TrimSpace(result.Output)
	if commit == "" {
		return "", s.stepError(step, "git current commit returned empty output", nil)
	}
	return commit, nil
}

func (s *Service) gitFetch(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig, origin string) error {
	argv := []string{s.cfg.Executables.Git, "fetch", origin, "--prune"}
	_, err := s.runLogged(ctx, meta, model.JobPhaseGitFetch, project.RepoDir, argv)
	if err != nil {
		return s.commandError(model.JobPhaseGitFetch, "git fetch failed", argv, err)
	}
	return nil
}

func (s *Service) gitCheckout(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig, origin, branch string) error {
	localExists, err := s.gitLocalBranchExists(ctx, meta, project, branch)
	if err != nil {
		return err
	}

	var argv []string
	if localExists {
		argv = []string{s.cfg.Executables.Git, "checkout", branch}
	} else {
		remoteExists, err := s.gitRemoteBranchExists(ctx, meta, project, origin, branch)
		if err != nil {
			return err
		}
		if !remoteExists {
			return s.stepError(model.JobPhaseGitCheckout, fmt.Sprintf("remote branch %s/%s does not exist", origin, branch), nil)
		}
		argv = []string{s.cfg.Executables.Git, "checkout", "-b", branch, "--track", origin + "/" + branch}
	}

	_, err = s.runLogged(ctx, meta, model.JobPhaseGitCheckout, project.RepoDir, argv)
	if err != nil {
		return s.commandError(model.JobPhaseGitCheckout, "git checkout failed", argv, err)
	}
	return nil
}

func (s *Service) gitPull(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig, origin, branch string) error {
	argv := []string{s.cfg.Executables.Git, "pull", "--ff-only", origin, branch}
	_, err := s.runLogged(ctx, meta, model.JobPhaseGitPull, project.RepoDir, argv)
	if err != nil {
		return s.commandError(model.JobPhaseGitPull, "git pull failed", argv, err)
	}
	return nil
}

func (s *Service) gitLocalBranchExists(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig, branch string) (bool, error) {
	argv := []string{s.cfg.Executables.Git, "show-ref", "--verify", "--quiet", "refs/heads/" + branch}
	result, err := s.captureLogged(ctx, meta, model.JobPhaseGitCheckout, project.RepoDir, argv)
	if err == nil {
		return true, nil
	}
	if result != nil && result.ExitCode == 1 {
		return false, nil
	}
	return false, s.commandError(model.JobPhaseGitCheckout, "git local branch probe failed", argv, err)
}

func (s *Service) gitRemoteBranchExists(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig, origin, branch string) (bool, error) {
	argv := []string{s.cfg.Executables.Git, "ls-remote", "--heads", origin, "refs/heads/" + branch}
	result, err := s.captureLogged(ctx, meta, model.JobPhaseGitCheckout, project.RepoDir, argv)
	if err != nil {
		return false, s.commandError(model.JobPhaseGitCheckout, "git remote branch probe failed", argv, err)
	}
	return strings.TrimSpace(result.Output) != "", nil
}
