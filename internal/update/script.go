package update

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/egomarker/docker-updater/internal/model"
	"github.com/egomarker/docker-updater/internal/util"
)

type ScriptInfo struct {
	Name           string `json:"name"`
	Runner         string `json:"runner"`
	Path           string `json:"path"`
	Cwd            string `json:"cwd"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func (s *Service) ListScripts(ctx context.Context) []ScriptInfo {
	infos := make([]ScriptInfo, 0, len(s.cfg.Scripts))
	for name, script := range s.cfg.Scripts {
		infos = append(infos, ScriptInfo{
			Name:           name,
			Runner:         script.Runner,
			Path:           script.Path,
			Cwd:            script.Cwd,
			TimeoutSeconds: script.TimeoutSeconds,
		})
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})
	return infos
}

func (s *Service) StartScript(ctx context.Context, name string) (*model.JobMeta, error) {
	name = strings.TrimSpace(name)
	scriptCfg, err := s.script(name)
	if err != nil {
		return nil, err
	}

	jobID, err := util.NewULID()
	if err != nil {
		return nil, internalError("generate job id", err)
	}

	now := time.Now().UTC()
	meta := &model.JobMeta{
		JobID:     jobID,
		ProjectID: name,
		Kind:      model.JobKindScript,
		Status:    model.JobStatusRunning,
		Phase:     model.JobPhasePreflight,
		CreatedAt: now,
		StartedAt: now,
		Script: &model.JobScriptState{
			Name:   name,
			Runner: scriptCfg.Runner,
			Path:   scriptCfg.Path,
			Cwd:    scriptCfg.Cwd,
		},
	}

	if err := s.scriptStore.CreateJob(meta); err != nil {
		return nil, internalError("create job", err)
	}

	go s.runScript(meta, scriptCfg)

	return cloneMeta(meta), nil
}

func (s *Service) GetScriptJob(ctx context.Context, name, jobID string) (*model.JobMeta, error) {
	name = strings.TrimSpace(name)
	if _, err := s.script(name); err != nil {
		return nil, err
	}
	meta, err := s.scriptStore.GetJob(name, jobID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, notFound("job not found")
		}
		return nil, internalError("read job", err)
	}
	return meta, nil
}

func (s *Service) GetLatestScriptJob(ctx context.Context, name string) (*model.JobMeta, error) {
	name = strings.TrimSpace(name)
	if _, err := s.script(name); err != nil {
		return nil, err
	}
	meta, err := s.scriptStore.GetLatestJob(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, notFound("latest job not found")
		}
		return nil, internalError("read latest job", err)
	}
	return meta, nil
}

func (s *Service) GetScriptJobLog(ctx context.Context, name, jobID string, tail *int) (string, error) {
	name = strings.TrimSpace(name)
	if _, err := s.script(name); err != nil {
		return "", err
	}
	logText, err := s.scriptStore.ReadLog(name, jobID, tail)
	if err != nil {
		if os.IsNotExist(err) {
			return "", notFound("job log not found")
		}
		return "", internalError("read job log", err)
	}
	return logText, nil
}

func (s *Service) GetLatestScriptJobLog(ctx context.Context, name string, tail *int) (string, error) {
	meta, err := s.GetLatestScriptJob(ctx, name)
	if err != nil {
		return "", err
	}
	return s.GetScriptJobLog(ctx, name, meta.JobID, tail)
}

func (s *Service) runScript(meta *model.JobMeta, scriptCfg model.ScriptConfig) {
	ctx := context.Background()
	s.appendLogf(meta, "JOB running kind=%s script=%s", meta.Kind, meta.ProjectID)

	s.appendLogf(meta, "STEP %s", model.JobPhasePreflight)
	if err := requireExecutable(scriptCfg.Runner); err != nil {
		s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhasePreflight, "script runner missing", err))
		return
	}
	if err := requireFile(scriptCfg.Path); err != nil {
		s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhasePreflight, "script file missing", err))
		return
	}
	if err := requireDir(scriptCfg.Cwd); err != nil {
		s.finish(meta, model.JobStatusFailed, s.stepError(model.JobPhasePreflight, "script cwd missing", err))
		return
	}

	s.setPhase(meta, model.JobPhaseScript)
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(scriptCfg.TimeoutSeconds)*time.Second)
	defer cancel()

	argv := []string{scriptCfg.Runner, scriptCfg.Path}
	_, runErr := s.runLogged(timeoutCtx, meta, model.JobPhaseScript, scriptCfg.Cwd, argv)
	if timeoutCtx.Err() == context.DeadlineExceeded {
		s.finish(meta, model.JobStatusFailed, &model.JobError{
			Step:    string(model.JobPhaseScript),
			Message: fmt.Sprintf("script timed out after %ds", scriptCfg.TimeoutSeconds),
			Command: append([]string(nil), argv...),
			At:      time.Now().UTC(),
		})
		return
	}
	if runErr != nil {
		s.finish(meta, model.JobStatusFailed, s.commandError(model.JobPhaseScript, "script failed", argv, runErr))
		return
	}

	s.finish(meta, model.JobStatusSuccess, nil)
}

func (s *Service) script(name string) (model.ScriptConfig, error) {
	name = strings.TrimSpace(name)
	script, ok := s.cfg.Scripts[name]
	if !ok {
		return model.ScriptConfig{}, notFound("script not found")
	}
	return script, nil
}
