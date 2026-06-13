package update

import (
	"context"
	"strings"

	"github.com/egomarker/docker-updater/internal/model"
)

func (s *Service) currentPrimaryServiceImageID(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig) (string, error) {
	psArgs := s.composeArgs(project)
	psArgs = append(psArgs, "ps", "-q", project.Compose.PrimaryService)
	psResult, err := s.captureLogged(ctx, meta, model.JobPhaseSnapshot, project.Compose.Cwd, psArgs)
	if err != nil {
		return "", s.commandError(model.JobPhaseSnapshot, "docker compose ps failed", psArgs, err)
	}
	containerID := strings.TrimSpace(psResult.Output)
	if containerID == "" {
		return "", nil
	}

	inspectArgs := []string{s.cfg.Executables.Docker, "inspect", "--format={{.Image}}", containerID}
	inspectResult, err := s.captureLogged(ctx, meta, model.JobPhaseSnapshot, project.Compose.Cwd, inspectArgs)
	if err != nil {
		return "", s.commandError(model.JobPhaseSnapshot, "docker inspect current image failed", inspectArgs, err)
	}
	return strings.TrimSpace(inspectResult.Output), nil
}

func (s *Service) tagRollbackImage(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig, sourceImageID, rollbackTag string) error {
	argv := []string{s.cfg.Executables.Docker, "tag", sourceImageID, rollbackTag}
	_, err := s.runLogged(ctx, meta, model.JobPhaseSnapshot, project.Compose.Cwd, argv)
	if err != nil {
		return s.commandError(model.JobPhaseSnapshot, "docker tag rollback image failed", argv, err)
	}
	return nil
}

func (s *Service) buildImage(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig, request model.DeployRequest, commitTag string) error {
	argv := []string{s.cfg.Executables.Docker, "build"}
	if request.PullBaseValue() {
		argv = append(argv, "--pull")
	}
	if !request.UseCacheValue() {
		argv = append(argv, "--no-cache")
	}
	if strings.TrimSpace(project.Build.Dockerfile) != "" {
		argv = append(argv, "-f", project.Build.Dockerfile)
	}
	argv = append(argv,
		"-t", project.Build.LatestTag,
		"-t", commitTag,
		project.Build.ContextDir,
	)

	_, err := s.runLogged(ctx, meta, model.JobPhaseBuild, project.Build.Cwd, argv)
	if err != nil {
		return s.commandError(model.JobPhaseBuild, "docker build failed", argv, err)
	}
	return nil
}

func (s *Service) composeUp(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig, step model.JobPhase) error {
	argv := s.composeArgs(project)
	argv = append(argv, "up", "-d", "--force-recreate")
	argv = append(argv, project.Compose.Services...)

	_, err := s.runLogged(ctx, meta, step, project.Compose.Cwd, argv)
	if err != nil {
		return s.commandError(step, "docker compose up failed", argv, err)
	}
	return nil
}

func (s *Service) composeRestart(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig) error {
	argv := s.composeArgs(project)
	argv = append(argv, "restart")
	argv = append(argv, project.Compose.Services...)

	_, err := s.runLogged(ctx, meta, model.JobPhaseRestart, project.Compose.Cwd, argv)
	if err != nil {
		return s.commandError(model.JobPhaseRestart, "docker compose restart failed", argv, err)
	}
	return nil
}

func (s *Service) retagRollbackToLatest(ctx context.Context, meta *model.JobMeta, project model.ProjectConfig, rollbackTag string) error {
	argv := []string{s.cfg.Executables.Docker, "tag", rollbackTag, project.Build.LatestTag}
	_, err := s.runLogged(ctx, meta, model.JobPhaseRollback, project.Compose.Cwd, argv)
	if err != nil {
		return s.commandError(model.JobPhaseRollback, "docker tag rollback-to-latest failed", argv, err)
	}
	return nil
}

func (s *Service) composeArgs(project model.ProjectConfig) []string {
	argv := []string{s.cfg.Executables.Docker, "compose"}
	for _, file := range project.Compose.Files {
		argv = append(argv, "-f", file)
	}
	return argv
}
