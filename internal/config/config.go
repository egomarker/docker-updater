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
const defaultZipPath = "/usr/bin/zip"
const defaultScriptTimeoutSeconds = 600
const defaultNtfyPriority = 3
const defaultNtfyTimeoutSeconds = 5
const defaultNtfyMaxLogBytes = 262144

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
	cfg.Executables.Zip = expandHomeOnly(cfg.Executables.Zip)
	if cfg.Executables.Zip == "" && anyBackupConfigured(cfg) {
		cfg.Executables.Zip = defaultZipPath
	}

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

		if project.Backup != nil {
			backup := *project.Backup
			for i, src := range backup.Sources {
				backup.Sources[i] = resolvePath(baseDir, src)
			}
			backup.Destination = resolvePath(baseDir, backup.Destination)
			project.Backup = &backup
		}

		cfg.Projects[projectID] = project
	}

	for scriptName, script := range cfg.Scripts {
		script.Runner = resolveExecutablePath(baseDir, script.Runner)
		script.Path = resolvePath(baseDir, script.Path)
		if script.Cwd == "" && script.Path != "" {
			script.Cwd = filepath.Dir(script.Path)
		}
		script.Cwd = resolvePath(baseDir, script.Cwd)
		if script.TimeoutSeconds <= 0 {
			script.TimeoutSeconds = defaultScriptTimeoutSeconds
		}
		cfg.Scripts[scriptName] = script
	}

	if cfg.Notify != nil && cfg.Notify.Ntfy != nil {
		ntfy := *cfg.Notify.Ntfy
		if ntfy.Priority <= 0 {
			ntfy.Priority = defaultNtfyPriority
		}
		if ntfy.TimeoutSeconds <= 0 {
			ntfy.TimeoutSeconds = defaultNtfyTimeoutSeconds
		}
		if ntfy.MaxLogBytes <= 0 {
			ntfy.MaxLogBytes = defaultNtfyMaxLogBytes
		}
		cfg.Notify.Ntfy = &ntfy
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
	if cfg.Scripts != nil && len(cfg.Scripts) == 0 {
		return fmt.Errorf("config.scripts must not be empty")
	}

	for projectID, project := range cfg.Projects {
		if strings.TrimSpace(projectID) == "" {
			return fmt.Errorf("config.projects contains an empty project id")
		}
		if projectID == "scripts" {
			return fmt.Errorf("config.projects.%s is reserved", projectID)
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

		if project.Backup != nil {
			if err := validateBackup(projectID, project.Backup); err != nil {
				return err
			}
		}

		cfg.Projects[projectID] = project
	}

	for scriptName, script := range cfg.Scripts {
		if !isValidScriptName(scriptName) {
			return fmt.Errorf("config.scripts.%s invalid (must match ^[a-z0-9._-]+$)", scriptName)
		}
		if strings.TrimSpace(script.Runner) == "" {
			return fmt.Errorf("config.scripts.%s.runner is required", scriptName)
		}
		if err := validateExecutablePath(script.Runner); err != nil {
			return fmt.Errorf("config.scripts.%s.runner: %w", scriptName, err)
		}
		if strings.TrimSpace(script.Path) == "" {
			return fmt.Errorf("config.scripts.%s.path is required", scriptName)
		}
		if strings.TrimSpace(script.Cwd) == "" {
			return fmt.Errorf("config.scripts.%s.cwd is required", scriptName)
		}
		if script.TimeoutSeconds <= 0 {
			return fmt.Errorf("config.scripts.%s.timeout_seconds must be positive", scriptName)
		}
		cfg.Scripts[scriptName] = script
	}

	if err := validateNotify(cfg.Notify); err != nil {
		return err
	}

	if err := validateExecutablePath(cfg.Executables.Git); err != nil {
		return fmt.Errorf("config.executables.git: %w", err)
	}
	if err := validateExecutablePath(cfg.Executables.Docker); err != nil {
		return fmt.Errorf("config.executables.docker: %w", err)
	}

	if anyBackupConfigured(cfg) {
		zipPath := strings.TrimSpace(cfg.Executables.Zip)
		if zipPath == "" {
			zipPath = defaultZipPath
		}
		if err := validateExecutablePath(zipPath); err != nil {
			return fmt.Errorf("config.executables.zip: %w", err)
		}
	}

	return nil
}

func validateBackup(projectID string, backup *model.BackupConfig) error {
	if backup == nil {
		return nil
	}
	if len(backup.Sources) == 0 {
		return fmt.Errorf("config.projects.%s.backup.sources must not be empty", projectID)
	}

	seenSources := make(map[string]struct{}, len(backup.Sources))
	seenBaseNames := make(map[string]string, len(backup.Sources))
	cleanSources := make([]string, 0, len(backup.Sources))
	for _, src := range backup.Sources {
		cleanSrc := filepath.Clean(strings.TrimSpace(src))
		if cleanSrc == "." || cleanSrc == "" {
			return fmt.Errorf("config.projects.%s.backup.sources contains an empty path", projectID)
		}
		if _, exists := seenSources[cleanSrc]; exists {
			return fmt.Errorf("config.projects.%s.backup.sources contains duplicate path %q", projectID, cleanSrc)
		}
		seenSources[cleanSrc] = struct{}{}
		baseName := filepath.Base(cleanSrc)
		if previous, exists := seenBaseNames[baseName]; exists {
			return fmt.Errorf("config.projects.%s.backup.sources contain duplicate basename %q (%q and %q)", projectID, baseName, previous, cleanSrc)
		}
		seenBaseNames[baseName] = cleanSrc
		cleanSources = append(cleanSources, cleanSrc)
	}

	cleanDestination := filepath.Clean(strings.TrimSpace(backup.Destination))
	if cleanDestination == "." || cleanDestination == "" {
		return fmt.Errorf("config.projects.%s.backup.destination is required", projectID)
	}
	for _, src := range cleanSources {
		if isSameOrDescendant(cleanDestination, src) {
			return fmt.Errorf("config.projects.%s.backup.destination must not be inside backup source %q", projectID, src)
		}
	}
	for _, entry := range backup.Exclude {
		if strings.TrimSpace(entry) == "" {
			return fmt.Errorf("config.projects.%s.backup.exclude contains an empty entry", projectID)
		}
	}
	switch symlinks := strings.TrimSpace(backup.Symlinks); symlinks {
	case "", "store", "follow":
	default:
		return fmt.Errorf("config.projects.%s.backup.symlinks must be \"store\" or \"follow\"", projectID)
	}
	if backup.Retain < 0 {
		return fmt.Errorf("config.projects.%s.backup.retain must be non-negative", projectID)
	}
	return nil
}

func isSameOrDescendant(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)

	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func validateNotify(notify *model.NotifyConfig) error {
	if notify == nil {
		return nil
	}
	if notify.Ntfy == nil {
		return nil
	}
	ntfy := notify.Ntfy
	if strings.TrimSpace(ntfy.BaseURL) == "" {
		return fmt.Errorf("config.notify.ntfy.base_url is required")
	}
	if !isValidNtfyBaseURL(ntfy.BaseURL) {
		return fmt.Errorf("config.notify.ntfy.base_url must start with http:// or https://")
	}
	if strings.TrimSpace(ntfy.Topic) == "" {
		return fmt.Errorf("config.notify.ntfy.topic is required")
	}
	if !isValidNtfyTopic(ntfy.Topic) {
		return fmt.Errorf("config.notify.ntfy.topic must match ^[a-zA-Z0-9_-]{1,64}$")
	}
	if ntfy.Priority != 0 && (ntfy.Priority < 1 || ntfy.Priority > 5) {
		return fmt.Errorf("config.notify.ntfy.priority must be between 1 and 5")
	}
	if ntfy.TimeoutSeconds != 0 && ntfy.TimeoutSeconds < 1 {
		return fmt.Errorf("config.notify.ntfy.timeout_seconds must be positive")
	}
	if ntfy.MaxLogBytes != 0 && ntfy.MaxLogBytes < 1 {
		return fmt.Errorf("config.notify.ntfy.max_log_bytes must be positive")
	}
	return nil
}

func isValidNtfyBaseURL(value string) bool {
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

func isValidNtfyTopic(topic string) bool {
	matched, err := regexp.MatchString("^[a-zA-Z0-9_-]{1,64}$", topic)
	return err == nil && matched
}

func isValidScriptName(name string) bool {
	matched, err := regexp.MatchString("^[a-z0-9._-]+$", name)
	return err == nil && matched
}

func anyBackupConfigured(cfg *model.Config) bool {
	for _, project := range cfg.Projects {
		if project.Backup != nil {
			return true
		}
	}
	return false
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

func resolveExecutablePath(baseDir, value string) string {
	value = expandHomeOnly(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	if strings.ContainsRune(value, filepath.Separator) {
		return filepath.Clean(filepath.Join(baseDir, value))
	}
	return value
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
