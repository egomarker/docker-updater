package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egomarker/docker-updater/internal/model"
)

const defaultMaxTailLines = 10000

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "config.json"
	}
	return filepath.Join(home, "Library", "Application Support", "host-updater", "config.json")
}

func Load(path string) (*model.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg model.Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	configDir := filepath.Dir(path)
	applyDefaults(&cfg, configDir)
	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func ReadBearerToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read bearer token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("bearer token file is empty")
	}
	return token, nil
}

func applyDefaults(cfg *model.Config, baseDir string) {
	cfg.Server.BearerTokenFile = resolvePath(baseDir, cfg.Server.BearerTokenFile)
	cfg.Paths.JobsRoot = resolvePath(baseDir, cfg.Paths.JobsRoot)
	cfg.Paths.RuntimeRoot = resolvePath(baseDir, cfg.Paths.RuntimeRoot)
	cfg.Executables.Git = expandHomeOnly(cfg.Executables.Git)
	cfg.Executables.Docker = expandHomeOnly(cfg.Executables.Docker)

	if cfg.Limits.MaxTailLines <= 0 {
		cfg.Limits.MaxTailLines = defaultMaxTailLines
	}

	for projectID, project := range cfg.Projects {
		project.RepoDir = resolvePath(baseDir, project.RepoDir)
		if project.Build.Cwd == "" {
			project.Build.Cwd = project.RepoDir
		}
		if project.Build.ContextDir == "" {
			project.Build.ContextDir = project.RepoDir
		}
		if project.Compose.Cwd == "" {
			project.Compose.Cwd = project.RepoDir
		}
		if project.Build.Dockerfile == "" && project.RepoDir != "" {
			project.Build.Dockerfile = filepath.Join(project.RepoDir, "Dockerfile")
		}

		project.Build.Cwd = resolvePath(baseDir, project.Build.Cwd)
		project.Build.ContextDir = resolvePath(baseDir, project.Build.ContextDir)
		project.Build.Dockerfile = resolvePath(baseDir, project.Build.Dockerfile)
		project.Compose.Cwd = resolvePath(baseDir, project.Compose.Cwd)
		for i, file := range project.Compose.Files {
			project.Compose.Files[i] = resolvePath(baseDir, file)
		}
		if project.Compose.PrimaryService == "" && len(project.Compose.Services) == 1 {
			project.Compose.PrimaryService = project.Compose.Services[0]
		}

		cfg.Projects[projectID] = project
	}
}

func validate(cfg *model.Config) error {
	if strings.TrimSpace(cfg.Server.ListenAddress) == "" {
		return fmt.Errorf("config.server.listen_address is required")
	}
	if strings.TrimSpace(cfg.Server.BearerTokenFile) == "" {
		return fmt.Errorf("config.server.bearer_token_file is required")
	}
	if strings.TrimSpace(cfg.Paths.JobsRoot) == "" {
		return fmt.Errorf("config.paths.jobs_root is required")
	}
	if strings.TrimSpace(cfg.Paths.RuntimeRoot) == "" {
		return fmt.Errorf("config.paths.runtime_root is required")
	}
	if strings.TrimSpace(cfg.Executables.Git) == "" {
		return fmt.Errorf("config.executables.git is required")
	}
	if strings.TrimSpace(cfg.Executables.Docker) == "" {
		return fmt.Errorf("config.executables.docker is required")
	}
	if cfg.Limits.MaxTailLines <= 0 {
		return fmt.Errorf("config.limits.max_tail_lines must be positive")
	}
	if len(cfg.Projects) == 0 {
		return fmt.Errorf("config.projects must not be empty")
	}

	for projectID, project := range cfg.Projects {
		if strings.TrimSpace(projectID) == "" {
			return fmt.Errorf("config.projects contains an empty project id")
		}
		if strings.TrimSpace(project.RepoDir) == "" {
			return fmt.Errorf("config.projects.%s.repo_dir is required", projectID)
		}
		if len(project.Git.AllowedOrigins) == 0 {
			return fmt.Errorf("config.projects.%s.git.allowed_origins must not be empty", projectID)
		}
		if len(project.Git.AllowedBranchRegexes) == 0 {
			return fmt.Errorf("config.projects.%s.git.allowed_branch_regexes must not be empty", projectID)
		}
		compiled := make([]*regexp.Regexp, 0, len(project.Git.AllowedBranchRegexes))
		for _, pattern := range project.Git.AllowedBranchRegexes {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("config.projects.%s.git.allowed_branch_regexes invalid pattern %q: %w", projectID, pattern, err)
			}
			compiled = append(compiled, re)
		}
		project.Git.CompiledBranchRegexes = compiled

		if strings.TrimSpace(project.Build.Cwd) == "" {
			return fmt.Errorf("config.projects.%s.build.cwd is required", projectID)
		}
		if strings.TrimSpace(project.Build.ContextDir) == "" {
			return fmt.Errorf("config.projects.%s.build.context_dir is required", projectID)
		}
		if strings.TrimSpace(project.Build.LatestTag) == "" {
			return fmt.Errorf("config.projects.%s.build.latest_tag is required", projectID)
		}
		if strings.TrimSpace(project.Build.CommitTagPrefix) == "" {
			return fmt.Errorf("config.projects.%s.build.commit_tag_prefix is required", projectID)
		}
		if strings.TrimSpace(project.Compose.Cwd) == "" {
			return fmt.Errorf("config.projects.%s.compose.cwd is required", projectID)
		}
		if len(project.Compose.Files) == 0 {
			return fmt.Errorf("config.projects.%s.compose.files must not be empty", projectID)
		}
		if strings.TrimSpace(project.Compose.PrimaryService) == "" {
			return fmt.Errorf("config.projects.%s.compose.primary_service is required", projectID)
		}

		cfg.Projects[projectID] = project
	}

	if err := validateExecutablePath(cfg.Executables.Git); err != nil {
		return fmt.Errorf("config.executables.git: %w", err)
	}
	if err := validateExecutablePath(cfg.Executables.Docker); err != nil {
		return fmt.Errorf("config.executables.docker: %w", err)
	}

	return nil
}

func validateExecutablePath(value string) error {
	if filepath.IsAbs(value) {
		if _, err := os.Stat(value); err != nil {
			return err
		}
		return nil
	}
	_, err := exec.LookPath(value)
	return err
}

func resolvePath(baseDir, value string) string {
	value = expandHomeOnly(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(baseDir, value))
}

func expandHomeOnly(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			if value == "~" {
				return home
			}
			return filepath.Join(home, value[2:])
		}
	}
	return value
}
