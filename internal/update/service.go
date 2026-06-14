package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	projectlock "github.com/egomarker/docker-updater/internal/lock"
	"github.com/egomarker/docker-updater/internal/jobs"
	"github.com/egomarker/docker-updater/internal/model"
	"github.com/egomarker/docker-updater/internal/runner"
	"github.com/egomarker/docker-updater/internal/util"
)

type ErrorKind string

const (
	ErrorKindBadRequest   ErrorKind = "bad_request"
	ErrorKindNotFound     ErrorKind = "not_found"
	ErrorKindConflict     ErrorKind = "conflict"
	ErrorKindUnauthorized ErrorKind = "unauthorized"
	ErrorKindInternal     ErrorKind = "internal"
)

type ServiceError struct {
	Kind        ErrorKind
	Message     string
	Err         error
	ActiveJobID string
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ServiceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Service struct {
	cfg    *model.Config
	store  *jobs.Store
	logger *slog.Logger

	mu     sync.Mutex
	active map[string]string
}

func NewService(cfg *model.Config, store *jobs.Store, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		cfg:    cfg,
		store:  store,
		logger: logger,
		active: make(map[string]string),
	}
}

func (s *Service) StartDeploy(ctx context.Context, projectID string, req model.DeployRequest) (*model.JobMeta, error) {
	project, err := s.project(projectID)
	if err != nil {
		return nil, err
	}

	req = req.WithDefaults()
	if strings.TrimSpace(req.Origin) == "" {
		return nil, badRequest("origin is required")
	}
	if strings.TrimSpace(req.Branch) == "" {
		return nil, badRequest("branch is required")
	}
	if !project.Git.AllowsOrigin(req.Origin) {
		return nil, badRequest("origin is not allowed for project")
	}
	if !project.Git.AllowsBranch(req.Branch) {
		return nil, badRequest("branch does not match allowlist")
	}

	jobID, err := util.NewULID()
	if err != nil {
		return nil, internalError("generate job id", err)
	}
	if err := s.reserveProject(projectID, jobID); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	meta := &model.JobMeta{
		JobID:     jobID,
		ProjectID: projectID,
		Kind:      model.JobKindDeploy,
		Status:    model.JobStatusRunning,
		Phase:     model.JobPhasePreflight,
		CreatedAt: now,
		StartedAt: now,
		Request: &model.JobRequest{
			Origin:   strings.TrimSpace(req.Origin),
			Branch:   strings.TrimSpace(req.Branch),
			PullBase: req.PullBaseValue(),
			UseCache: req.UseCacheValue(),
		},
		Repo: model.JobRepoState{
			TargetOrigin: model.StringPtr(strings.TrimSpace(req.Origin)),
			TargetBranch: model.StringPtr(strings.TrimSpace(req.Branch)),
		},
		Image: model.JobImageState{
			LatestTag: project.Build.LatestTag,
		},
	}

	if err := s.store.CreateJob(meta); err != nil {
		s.releaseProject(projectID, jobID)
		return nil, internalError("create job", err)
	}

	go func(job *model.JobMeta, projectCfg model.ProjectConfig, request model.DeployRequest) {
		defer s.releaseProject(projectID, job.JobID)
		s.runDeploy(job, projectCfg, request)
	}(meta, project, req)

	return cloneMeta(meta), nil
}

func (s *Service) StartRestart(ctx context.Context, projectID string) (*model.JobMeta, error) {
	project, err := s.project(projectID)
	if err != nil {
		return nil, err
	}

	jobID, err := util.NewULID()
	if err != nil {
		return nil, internalError("generate job id", err)
	}
	if err := s.reserveProject(projectID, jobID); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	meta := &model.JobMeta{
		JobID:     jobID,
		ProjectID: projectID,
		Kind:      model.JobKindRestart,
		Status:    model.JobStatusRunning,
		Phase:     model.JobPhaseRestart,
		CreatedAt: now,
		StartedAt: now,
		Image: model.JobImageState{
			LatestTag: project.Build.LatestTag,
		},
	}

	if err := s.store.CreateJob(meta); err != nil {
		s.releaseProject(projectID, jobID)
		return nil, internalError("create job", err)
	}

	go func(job *model.JobMeta, projectCfg model.ProjectConfig) {
		defer s.releaseProject(projectID, job.JobID)
		s.runRestart(job, projectCfg)
	}(meta, project)

	return cloneMeta(meta), nil
}

func (s *Service) StartBackup(ctx context.Context, projectID string) (*model.JobMeta, error) {
	project, err := s.project(projectID)
	if err != nil {
		return nil, err
	}
	if project.Backup == nil {
		return nil, badRequest("backup not configured for project")
	}

	jobID, err := util.NewULID()
	if err != nil {
		return nil, internalError("generate job id", err)
	}
	if err := s.reserveProject(projectID, jobID); err != nil {
		return nil, err
	}

	backupCfg := *project.Backup
	symlinks := backupCfg.Symlinks
	if symlinks == "" {
		symlinks = "store"
	}
	now := time.Now().UTC()
	meta := &model.JobMeta{
		JobID:     jobID,
		ProjectID: projectID,
		Kind:      model.JobKindBackup,
		Status:    model.JobStatusRunning,
		Phase:     model.JobPhasePreflight,
		CreatedAt: now,
		StartedAt: now,
		Backup: &model.JobBackupState{
			Sources:     backupCfg.Sources,
			Destination: backupCfg.Destination,
			Exclude:     backupCfg.Exclude,
			Symlinks:    symlinks,
			Retain:      backupCfg.Retain,
		},
	}

	if err := s.store.CreateJob(meta); err != nil {
		s.releaseProject(projectID, jobID)
		return nil, internalError("create job", err)
	}

	go func(job *model.JobMeta, projectCfg model.ProjectConfig) {
		defer s.releaseProject(projectID, job.JobID)
		s.runBackup(job, projectCfg)
	}(meta, project)

	return cloneMeta(meta), nil
}

func (s *Service) GetJob(ctx context.Context, projectID, jobID string) (*model.JobMeta, error) {
	if _, err := s.project(projectID); err != nil {
		return nil, err
	}
	meta, err := s.store.GetJob(projectID, jobID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, notFound("job not found")
		}
		return nil, internalError("read job", err)
	}
	return meta, nil
}

func (s *Service) GetLatestJob(ctx context.Context, projectID string) (*model.JobMeta, error) {
	if _, err := s.project(projectID); err != nil {
		return nil, err
	}
	meta, err := s.store.GetLatestJob(projectID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, notFound("latest job not found")
		}
		return nil, internalError("read latest job", err)
	}
	return meta, nil
}

func (s *Service) GetJobLog(ctx context.Context, projectID, jobID string, tail *int) (string, error) {
	if _, err := s.project(projectID); err != nil {
		return "", err
	}
	logText, err := s.store.ReadLog(projectID, jobID, tail)
	if err != nil {
		if os.IsNotExist(err) {
			return "", notFound("job log not found")
		}
		return "", internalError("read job log", err)
	}
	return logText, nil
}

func (s *Service) GetLatestJobLog(ctx context.Context, projectID string, tail *int) (string, error) {
	meta, err := s.GetLatestJob(ctx, projectID)
	if err != nil {
		return "", err
	}
	return s.GetJobLog(ctx, projectID, meta.JobID, tail)
}

func (s *Service) project(projectID string) (model.ProjectConfig, error) {
	projectID = strings.TrimSpace(projectID)
	project, ok := s.cfg.Projects[projectID]
	if !ok {
		return model.ProjectConfig{}, notFound("project not found")
	}
	return project, nil
}

func (s *Service) reserveProject(projectID, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if activeJobID := s.active[projectID]; activeJobID != "" {
		return conflict(activeJobID)
	}
	lockedJobID, err := projectlock.ReadJobID(s.lockPath(projectID))
	if err != nil {
		return internalError("read project lock", err)
	}
	if lockedJobID != "" {
		return conflict(lockedJobID)
	}
	if existing := s.active[projectID]; existing != "" {
		return conflict(existing)
	}
	s.active[projectID] = jobID
	return nil
}

func (s *Service) releaseProject(projectID, jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[projectID] == jobID {
		delete(s.active, projectID)
	}
}

func (s *Service) lockPath(projectID string) string {
	return filepath.Join(s.cfg.Paths.RuntimeRoot, "locks", projectID+".lock")
}

func (s *Service) appendLog(meta *model.JobMeta, message string) {
	if err := s.store.AppendLog(meta.ProjectID, meta.JobID, message); err != nil {
		s.logger.Error("append job log failed", "project_id", meta.ProjectID, "job_id", meta.JobID, "error", err)
	}
}

func (s *Service) appendLogf(meta *model.JobMeta, format string, args ...any) {
	s.appendLog(meta, fmt.Sprintf(format, args...))
}

func (s *Service) updateMeta(meta *model.JobMeta) {
	if err := s.store.UpdateMeta(meta); err != nil {
		s.logger.Error("update job meta failed", "project_id", meta.ProjectID, "job_id", meta.JobID, "error", err)
	}
}

func (s *Service) setPhase(meta *model.JobMeta, phase model.JobPhase) {
	meta.Phase = phase
	s.updateMeta(meta)
}

func (s *Service) finish(meta *model.JobMeta, status model.JobStatus, errObj *model.JobError) {
	now := time.Now().UTC()
	meta.Status = status
	meta.Phase = model.JobPhaseDone
	meta.FinishedAt = &now
	meta.Error = errObj
	s.updateMeta(meta)
	s.appendLogf(meta, "JOB finished status=%s", status)
}

func (s *Service) normalizeJobError(step model.JobPhase, message string, err error) *model.JobError {
	var jobErr *model.JobError
	if errors.As(err, &jobErr) {
		return jobErr
	}
	return s.stepError(step, message, err)
}

func (s *Service) commandError(step model.JobPhase, message string, argv []string, err error) *model.JobError {
	at := time.Now().UTC()
	var exitCode *int
	code := runner.ExitCode(err)
	if code >= 0 {
		exitCode = &code
	}
	command := append([]string(nil), argv...)
	return &model.JobError{
		Step:     string(step),
		Message:  message,
		ExitCode: exitCode,
		Command:  command,
		At:       at,
	}
}

func (s *Service) stepError(step model.JobPhase, message string, err error) *model.JobError {
	at := time.Now().UTC()
	if err != nil {
		message = message + ": " + err.Error()
	}
	return &model.JobError{
		Step:    string(step),
		Message: message,
		At:      at,
	}
}

func (s *Service) runLogged(ctx context.Context, meta *model.JobMeta, step model.JobPhase, cwd string, argv []string) (*runner.Result, error) {
	s.appendLogf(meta, "STEP %s CMD %s", step, runner.FormatArgv(argv))
	return runner.Run(ctx, cwd, argv, func(line string) {
		s.appendLog(meta, line)
	})
}

func (s *Service) captureLogged(ctx context.Context, meta *model.JobMeta, step model.JobPhase, cwd string, argv []string) (*runner.Result, error) {
	s.appendLogf(meta, "STEP %s CMD %s", step, runner.FormatArgv(argv))
	return runner.Capture(ctx, cwd, argv, func(line string) {
		s.appendLog(meta, line)
	})
}

func cloneMeta(meta *model.JobMeta) *model.JobMeta {
	if meta == nil {
		return nil
	}
	clone := *meta
	if meta.Request != nil {
		request := *meta.Request
		clone.Request = &request
	}
	clone.Repo = model.JobRepoState{
		PreBranch:    copyStringPtr(meta.Repo.PreBranch),
		PreCommit:    copyStringPtr(meta.Repo.PreCommit),
		TargetOrigin: copyStringPtr(meta.Repo.TargetOrigin),
		TargetBranch: copyStringPtr(meta.Repo.TargetBranch),
		PostCommit:   copyStringPtr(meta.Repo.PostCommit),
	}
	clone.Image = model.JobImageState{
		LatestTag:          meta.Image.LatestTag,
		CandidateCommitTag: copyStringPtr(meta.Image.CandidateCommitTag),
		PredeployImageID:   copyStringPtr(meta.Image.PredeployImageID),
		RollbackTag:        copyStringPtr(meta.Image.RollbackTag),
		ActiveTag:          copyStringPtr(meta.Image.ActiveTag),
	}
	if meta.Backup != nil {
		backup := *meta.Backup
		backup.Sources = append([]string(nil), meta.Backup.Sources...)
		backup.Exclude = append([]string(nil), meta.Backup.Exclude...)
		clone.Backup = &backup
	}
	if meta.FinishedAt != nil {
		finishedAt := *meta.FinishedAt
		clone.FinishedAt = &finishedAt
	}
	if meta.Error != nil {
		errCopy := *meta.Error
		errCopy.Command = append([]string(nil), meta.Error.Command...)
		clone.Error = &errCopy
	}
	return &clone
}

func copyStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func badRequest(message string) error {
	return &ServiceError{Kind: ErrorKindBadRequest, Message: message}
}

func notFound(message string) error {
	return &ServiceError{Kind: ErrorKindNotFound, Message: message}
}

func conflict(activeJobID string) error {
	return &ServiceError{Kind: ErrorKindConflict, Message: "active job already running", ActiveJobID: activeJobID}
}

func internalError(message string, err error) error {
	return &ServiceError{Kind: ErrorKindInternal, Message: message, Err: err}
}

func AsServiceError(err error) (*ServiceError, bool) {
	var serviceErr *ServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr, true
	}
	return nil, false
}
