package update

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egomarker/docker-updater/internal/model"
	"github.com/egomarker/docker-updater/internal/notify"
)

// notify sends a best-effort ntfy notification about a finished job.
// It never blocks finish() beyond building the message and never fails the job.
func (s *Service) notify(meta *model.JobMeta, status model.JobStatus, errObj *model.JobError) {
	if s.sender == nil || !s.sender.Configured() {
		return
	}

	n := buildNotification(meta, status, errObj, s.defaultPriority())
	if errObj != nil && s.attachLogOnFailure() {
		if att := s.buildLogAttachment(meta); att != nil {
			n.Attachment = att
		}
	}

	go func() {
		timeout := s.notifyTimeout()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := s.sender.Send(ctx, n); err != nil {
			s.logger.Error("ntfy notify failed", "job_id", meta.JobID, "project_id", meta.ProjectID, "error", err)
		}
	}()
}

// TestNotify publishes a fixed test message using the current notify config.
func (s *Service) TestNotify(ctx context.Context) error {
	if s.sender == nil || !s.sender.Configured() {
		return badRequest("notify.ntfy is not configured")
	}
	return s.sender.Send(ctx, notify.Notification{
		Title:    "host-updater test",
		Message:  "This is a test notification from host-updater.",
		Tags:     []string{"bell"},
		Priority: s.defaultPriority(),
	})
}

func (s *Service) defaultPriority() int {
	if s.cfg != nil && s.cfg.Notify != nil && s.cfg.Notify.Ntfy != nil && s.cfg.Notify.Ntfy.Priority > 0 {
		return s.cfg.Notify.Ntfy.Priority
	}
	return 3
}

func (s *Service) notifyTimeout() time.Duration {
	seconds := 5
	if s.cfg != nil && s.cfg.Notify != nil && s.cfg.Notify.Ntfy != nil && s.cfg.Notify.Ntfy.TimeoutSeconds > 0 {
		seconds = s.cfg.Notify.Ntfy.TimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

func (s *Service) attachLogOnFailure() bool {
	if s.cfg == nil || s.cfg.Notify == nil || s.cfg.Notify.Ntfy == nil {
		return false
	}
	return s.cfg.Notify.Ntfy.AttachLogOnFailureValue()
}

func (s *Service) maxLogBytes() int {
	if s.cfg != nil && s.cfg.Notify != nil && s.cfg.Notify.Ntfy != nil && s.cfg.Notify.Ntfy.MaxLogBytes > 0 {
		return s.cfg.Notify.Ntfy.MaxLogBytes
	}
	return 262144
}

func (s *Service) buildLogAttachment(meta *model.JobMeta) *notify.Attachment {
	store := s.storeForMeta(meta)
	logText, err := store.ReadLog(meta.ProjectID, meta.JobID, nil)
	if err != nil {
		s.logger.Error("read job log for ntfy attachment failed", "job_id", meta.JobID, "error", err)
		return nil
	}
	if strings.TrimSpace(logText) == "" {
		return nil
	}

	data := []byte(logText)
	if max := s.maxLogBytes(); len(data) > max {
		data = data[len(data)-max:]
	}

	return &notify.Attachment{
		Filename:    fmt.Sprintf("%s-%s-%s.log", meta.Kind, meta.ProjectID, meta.JobID),
		ContentType: "text/plain; charset=utf-8",
		Data:        data,
	}
}

func buildNotification(meta *model.JobMeta, status model.JobStatus, errObj *model.JobError, defaultPriority int) notify.Notification {
	name := meta.ProjectID
	kind := string(meta.Kind)
	failed := status != model.JobStatusSuccess

	tags := []string{"white_check_mark"}
	priority := defaultPriority
	if failed {
		tags = []string{"rotating_light"}
		priority = 5
	}

	title := fmt.Sprintf("%s %s: %s", name, kind, status)

	var body string
	switch meta.Kind {
	case model.JobKindDeploy:
		branch := ptrValue(meta.Repo.TargetBranch, "?")
		commit := shortCommit(ptrValue(meta.Repo.PostCommit, ptrValue(meta.Repo.PreCommit, "")))
		body = fmt.Sprintf("%s @ %s (%s)", branch, commit, jobDuration(meta))
	case model.JobKindRestart:
		body = fmt.Sprintf("restarted in %s", jobDuration(meta))
	case model.JobKindBackup:
		if meta.Backup != nil {
			switch {
			case meta.Backup.OutputBytes > 0 && meta.Backup.OutputZip != "":
				body = fmt.Sprintf("%s (%s)", filepath.Base(meta.Backup.OutputZip), humanBytes(meta.Backup.OutputBytes))
			case meta.Backup.OutputZip != "":
				body = filepath.Base(meta.Backup.OutputZip)
			default:
				body = fmt.Sprintf("archive created in %s", jobDuration(meta))
			}
		} else {
			body = fmt.Sprintf("finished in %s", jobDuration(meta))
		}
	case model.JobKindScript:
		body = fmt.Sprintf("finished in %s", jobDuration(meta))
	default:
		body = fmt.Sprintf("finished in %s", jobDuration(meta))
	}

	if failed && errObj != nil && errObj.Message != "" {
		body = fmt.Sprintf("step=%s: %s", errObj.Step, errObj.Message)
	}

	return notify.Notification{
		Title:    title,
		Message:  body,
		Tags:     tags,
		Priority: priority,
	}
}

func ptrValue(p *string, fallback string) string {
	if p == nil || *p == "" {
		return fallback
	}
	return *p
}

func shortCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) >= 8 {
		return commit[:8]
	}
	if commit == "" {
		return "?"
	}
	return commit
}

func jobDuration(meta *model.JobMeta) string {
	end := time.Now().UTC()
	if meta.FinishedAt != nil {
		end = *meta.FinishedAt
	}
	d := end.Sub(meta.StartedAt)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) - minutes*60
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
