package jobs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egomarker/docker-updater/internal/model"
	"github.com/egomarker/docker-updater/internal/util"
)

type Store struct {
	jobsRoot string
	mu       sync.Mutex
}

func NewStore(jobsRoot string) (*Store, error) {
	jobsRoot = filepath.Clean(jobsRoot)
	if err := os.MkdirAll(jobsRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create jobs root: %w", err)
	}
	return &Store{jobsRoot: jobsRoot}, nil
}

func (s *Store) CreateJob(meta *model.JobMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	projectDir := s.projectDir(meta.ProjectID)
	jobDir := s.jobDir(meta.ProjectID, meta.JobID)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return fmt.Errorf("create project jobs dir: %w", err)
	}
	if err := os.Mkdir(jobDir, 0o755); err != nil {
		return fmt.Errorf("create job dir: %w", err)
	}
	if err := os.WriteFile(s.logPath(meta.ProjectID, meta.JobID), nil, 0o644); err != nil {
		return fmt.Errorf("create job log: %w", err)
	}
	if err := s.writeMetaLocked(meta); err != nil {
		return err
	}
	if err := util.WriteFileAtomic(s.latestPath(meta.ProjectID), []byte(meta.JobID+"\n"), 0o644); err != nil {
		return fmt.Errorf("write latest pointer: %w", err)
	}
	return nil
}

func (s *Store) UpdateMeta(meta *model.JobMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeMetaLocked(meta)
}

func (s *Store) GetJob(projectID, jobID string) (*model.JobMeta, error) {
	data, err := os.ReadFile(s.metaPath(projectID, jobID))
	if err != nil {
		return nil, err
	}
	var meta model.JobMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("decode job meta: %w", err)
	}
	return &meta, nil
}

func (s *Store) GetLatestJob(projectID string) (*model.JobMeta, error) {
	data, err := os.ReadFile(s.latestPath(projectID))
	if err != nil {
		return nil, err
	}
	jobID := strings.TrimSpace(string(data))
	if jobID == "" {
		return nil, os.ErrNotExist
	}
	return s.GetJob(projectID, jobID)
}

func (s *Store) AppendLog(projectID, jobID, message string) error {
	message = strings.ReplaceAll(message, "\r\n", "\n")
	message = strings.TrimRight(message, "\n")
	if message == "" {
		return nil
	}

	lines := strings.Split(message, "\n")

	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := os.OpenFile(s.logPath(projectID, jobID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open job log: %w", err)
	}
	defer file.Close()

	for _, line := range lines {
		stamp := time.Now().UTC().Format(time.RFC3339)
		if _, err := fmt.Fprintf(file, "[%s] %s\n", stamp, line); err != nil {
			return fmt.Errorf("append job log: %w", err)
		}
	}
	return nil
}

func (s *Store) ReadLog(projectID, jobID string, tail *int) (string, error) {
	data, err := os.ReadFile(s.logPath(projectID, jobID))
	if err != nil {
		return "", err
	}
	if tail == nil {
		return string(data), nil
	}
	if *tail <= 0 {
		return "", fmt.Errorf("tail must be positive")
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return "", nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > *tail {
		lines = lines[len(lines)-*tail:]
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func (s *Store) ListRunningJobs() ([]*model.JobMeta, error) {
	projectEntries, err := os.ReadDir(s.jobsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var running []*model.JobMeta
	for _, projectEntry := range projectEntries {
		if !projectEntry.IsDir() {
			continue
		}
		projectID := projectEntry.Name()
		jobEntries, err := os.ReadDir(s.projectDir(projectID))
		if err != nil {
			return nil, err
		}
		for _, jobEntry := range jobEntries {
			if !jobEntry.IsDir() {
				continue
			}
			meta, err := s.GetJob(projectID, jobEntry.Name())
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			if meta.Status == model.JobStatusRunning {
				running = append(running, meta)
			}
		}
	}

	return running, nil
}

func (s *Store) projectDir(projectID string) string {
	return filepath.Join(s.jobsRoot, projectID)
}

func (s *Store) jobDir(projectID, jobID string) string {
	return filepath.Join(s.projectDir(projectID), jobID)
}

func (s *Store) metaPath(projectID, jobID string) string {
	return filepath.Join(s.jobDir(projectID, jobID), "meta.json")
}

func (s *Store) logPath(projectID, jobID string) string {
	return filepath.Join(s.jobDir(projectID, jobID), "log.txt")
}

func (s *Store) latestPath(projectID string) string {
	return filepath.Join(s.projectDir(projectID), "latest")
}

func (s *Store) writeMetaLocked(meta *model.JobMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encode job meta: %w", err)
	}
	data = append(data, '\n')
	if err := util.WriteFileAtomic(s.metaPath(meta.ProjectID, meta.JobID), data, 0o644); err != nil {
		return fmt.Errorf("write job meta: %w", err)
	}
	return nil
}
