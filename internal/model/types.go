package model

import (
	"regexp"
	"strings"
	"time"
)

type Config struct {
	Server      ServerConfig            `json:"server"`
	Paths       PathsConfig             `json:"paths"`
	Executables ExecutablesConfig       `json:"executables"`
	Limits      LimitsConfig            `json:"limits"`
	Projects    map[string]ProjectConfig `json:"projects"`
}

type ServerConfig struct {
	ListenAddress   string `json:"listen_address"`
	BearerTokenFile string `json:"bearer_token_file"`
}

type PathsConfig struct {
	JobsRoot    string `json:"jobs_root"`
	RuntimeRoot string `json:"runtime_root"`
}

type ExecutablesConfig struct {
	Git    string `json:"git"`
	Docker string `json:"docker"`
}

type LimitsConfig struct {
	MaxTailLines int `json:"max_tail_lines"`
}

type ProjectConfig struct {
	RepoDir string           `json:"repo_dir"`
	Git     GitProjectConfig `json:"git"`
	Build   BuildConfig      `json:"build"`
	Compose ComposeConfig    `json:"compose"`
}

type GitProjectConfig struct {
	AllowedOrigins       []string         `json:"allowed_origins"`
	AllowedBranchRegexes []string         `json:"allowed_branch_regexes"`
	CompiledBranchRegexes []*regexp.Regexp `json:"-"`
}

type BuildConfig struct {
	Cwd             string `json:"cwd"`
	ContextDir      string `json:"context_dir"`
	Dockerfile      string `json:"dockerfile"`
	LatestTag       string `json:"latest_tag"`
	CommitTagPrefix string `json:"commit_tag_prefix"`
}

type ComposeConfig struct {
	Cwd            string   `json:"cwd"`
	Files          []string `json:"files"`
	PrimaryService string   `json:"primary_service"`
	Services       []string `json:"services"`
}

func (g GitProjectConfig) AllowsOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	for _, candidate := range g.AllowedOrigins {
		if origin == candidate {
			return true
		}
	}
	return false
}

func (g GitProjectConfig) AllowsBranch(branch string) bool {
	branch = strings.TrimSpace(branch)
	for _, compiled := range g.CompiledBranchRegexes {
		if compiled.MatchString(branch) {
			return true
		}
	}
	return false
}

type DeployRequest struct {
	Origin   string `json:"origin"`
	Branch   string `json:"branch"`
	PullBase *bool  `json:"pull_base"`
	UseCache *bool  `json:"use_cache"`
}

func (r DeployRequest) WithDefaults() DeployRequest {
	out := r
	if out.PullBase == nil {
		value := true
		out.PullBase = &value
	}
	if out.UseCache == nil {
		value := true
		out.UseCache = &value
	}
	return out
}

func (r DeployRequest) PullBaseValue() bool {
	return r.PullBase == nil || *r.PullBase
}

func (r DeployRequest) UseCacheValue() bool {
	return r.UseCache == nil || *r.UseCache
}

type RestartRequest struct{}

type JobKind string

type JobStatus string

type JobPhase string

const (
	JobKindDeploy  JobKind = "deploy"
	JobKindRestart JobKind = "restart"
)

const (
	JobStatusRunning        JobStatus = "running"
	JobStatusSuccess        JobStatus = "success"
	JobStatusFailed         JobStatus = "failed"
	JobStatusRolledBack     JobStatus = "rolled_back"
	JobStatusRollbackFailed JobStatus = "rollback_failed"
	JobStatusInterrupted    JobStatus = "interrupted"
)

const (
	JobPhasePreflight  JobPhase = "preflight"
	JobPhaseSnapshot   JobPhase = "snapshot"
	JobPhaseGitFetch   JobPhase = "git_fetch"
	JobPhaseGitCheckout JobPhase = "git_checkout"
	JobPhaseGitPull    JobPhase = "git_pull"
	JobPhaseBuild      JobPhase = "build"
	JobPhaseDeploy     JobPhase = "deploy"
	JobPhaseRestart    JobPhase = "restart"
	JobPhaseRollback   JobPhase = "rollback"
	JobPhaseDone       JobPhase = "done"
)

type JobMeta struct {
	JobID      string      `json:"job_id"`
	ProjectID  string      `json:"project_id"`
	Kind       JobKind     `json:"kind"`
	Status     JobStatus   `json:"status"`
	Phase      JobPhase    `json:"phase"`
	CreatedAt  time.Time   `json:"created_at"`
	StartedAt  time.Time   `json:"started_at"`
	FinishedAt *time.Time  `json:"finished_at"`
	Request    *JobRequest `json:"request,omitempty"`
	Repo       JobRepoState `json:"repo"`
	Image      JobImageState `json:"image"`
	Error      *JobError   `json:"error"`
}

type JobRequest struct {
	Origin   string `json:"origin"`
	Branch   string `json:"branch"`
	PullBase bool   `json:"pull_base"`
	UseCache bool   `json:"use_cache"`
}

type JobRepoState struct {
	PreBranch    *string `json:"pre_branch"`
	PreCommit    *string `json:"pre_commit"`
	TargetOrigin *string `json:"target_origin"`
	TargetBranch *string `json:"target_branch"`
	PostCommit   *string `json:"post_commit"`
}

type JobImageState struct {
	LatestTag          string  `json:"latest_tag"`
	CandidateCommitTag *string `json:"candidate_commit_tag"`
	PredeployImageID   *string `json:"predeploy_image_id"`
	RollbackTag        *string `json:"rollback_tag"`
	ActiveTag          *string `json:"active_tag"`
}

type JobError struct {
	Step     string    `json:"step"`
	Message  string    `json:"message"`
	ExitCode *int      `json:"exit_code"`
	Command  []string  `json:"command"`
	At       time.Time `json:"at"`
}

func (e *JobError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func StringPtr(value string) *string {
	if value == "" {
		return nil
	}
	copyValue := value
	return &copyValue
}
