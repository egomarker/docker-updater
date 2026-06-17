package update

import (
	"bytes"
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

	summary := buildNotification(meta, status, errObj, s.defaultPriority())
	var attachment *notify.Attachment
	if errObj != nil && s.attachLogOnFailure() {
		attachment = s.buildLogAttachment(meta)
	}

	go func() {
		if err := s.sendNotification(summary); err != nil {
			s.logger.Error("ntfy notify failed", "job_id", meta.JobID, "project_id", meta.ProjectID, "error", err)
			return
		}
		if attachment != nil {
			attachmentNote := notify.Notification{
				Title:      summary.Title + " (log)",
				Tags:       append([]string(nil), summary.Tags...),
				Priority:   summary.Priority,
				Attachment: attachment,
			}
			if err := s.sendNotification(attachmentNote); err != nil {
				s.logger.Error("ntfy log attachment notify failed", "job_id", meta.JobID, "project_id", meta.ProjectID, "error", err)
			}
		}
	}()
}

func (s *Service) NotifyStartup(version string) {
	if s.sender == nil || !s.sender.Configured() {
		return
	}
	version = strings.TrimSpace(version)
	if version == "" {
		version = "unknown"
	}
	go func() {
		if err := s.sendNotification(notify.Notification{
			Title:    "host-updater restarted",
			Message:  fmt.Sprintf("host restarted, version %s", version),
			Tags:     []string{"arrows_counterclockwise"},
			Priority: s.defaultPriority(),
		}); err != nil {
			s.logger.Error("ntfy startup notify failed", "version", version, "error", err)
		}
	}()
}

// TestNotify publishes a fixed test message using the current notify config.
func (s *Service) TestNotify(ctx context.Context) error {
	if s.sender == nil || !s.sender.Configured() {
		return badRequest("notify.ntfy is not configured")
	}
	if err := s.sender.Send(ctx, notify.Notification{
		Title:    "host-updater test",
		Message:  "This is a test notification from host-updater.",
		Tags:     []string{"bell"},
		Priority: s.defaultPriority(),
	}); err != nil {
		return upstreamError(fmt.Sprintf("notify test failed: %v", err), err)
	}
	attachment := &notify.Attachment{
		Filename:    "host-updater-notify-test.txt",
		ContentType: "text/plain; charset=utf-8",
		Data: []byte("host-updater notification test\n\nThis attachment verifies raw file upload notifications.\n"),
	}
	if err := s.sender.Send(ctx, notify.Notification{
		Title:      "host-updater test (attachment)",
		Tags:       []string{"bell", "paperclip"},
		Priority:   s.defaultPriority(),
		Attachment: attachment,
	}); err != nil {
		return upstreamError(fmt.Sprintf("notify test attachment failed: %v", err), err)
	}
	return nil
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
	data, err := store.ReadLogTailBytes(meta.ProjectID, meta.JobID, s.maxLogBytes())
	if err != nil {
		s.logger.Error("read job log for ntfy attachment failed", "job_id", meta.JobID, "error", err)
		return nil
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}

	return &notify.Attachment{
		Filename:    fmt.Sprintf("%s-%s-%s.log", meta.Kind, meta.ProjectID, meta.JobID),
		ContentType: "text/plain; charset=utf-8",
		Data:        data,
	}
}

func (s *Service) sendNotification(n notify.Notification) error {
	timeout := s.notifyTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.sender.Send(ctx, n)
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
