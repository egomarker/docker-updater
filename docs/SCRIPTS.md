# run_script — Host Scripts Feature (v1)

Status: spec, not yet implemented.

## Goal

Add a `script` job kind that runs an arbitrary host shell/interpreter script that
is **predefined in config**. Scripts are **global/host-scoped**, not tied to a
project, so they can run concurrently with deploys/restarts/backups and with each
other. Fully config-defined: the only runtime input is the script name.

This is a 4th job kind alongside `deploy`, `restart`, `backup`.

## How it fits the existing model

Everything in docker-updater is a job: ULID + status + phase + JSON meta,
streamed logs via `runner.Run`, status flow `running → success|failed`, file-backed
under `jobs_root`. Scripts reuse all of that with two differences:

1. **No project association.** Scripts are global. They take no project lock and
   run independently of all project jobs.
2. **No rollback.** Rollback is deploy-only. Scripts just finish success/failed.

`jobs.Store` is already generic: it namespaces jobs by an opaque string key and
does not care that key is a "project". So scripts get a **second `*jobs.Store`**
rooted at `jobs_root/scripts/`. Script jobs are stored as:

```
<jobs_root>/scripts/<script-name>/<job-id>/meta.json
<jobs_root>/scripts/<script-name>/<job-id>/log.txt
<jobs_root>/scripts/<script-name>/latest        # pointer file (last-writer-wins)
```

Project jobs remain at `<jobs_root>/<project-id>/...`. Zero changes to `jobs.Store`
code. The project id **`scripts` is reserved** (project config validation rejects it).

## Config schema

New top-level, optional field on `Config`:

```go
type Config struct {
    // ... existing fields ...
    Scripts map[string]ScriptConfig `json:"scripts,omitempty"`
}

type ScriptConfig struct {
    Runner         string `json:"runner"`          // required
    Path           string `json:"path"`            // required
    Cwd            string `json:"cwd"`             // optional; default = dir(Path)
    TimeoutSeconds int    `json:"timeout_seconds"` // optional; default 600
}
```

Example:

```json
{
  "scripts": {
    "backup-photos": {
      "runner": "/bin/bash",
      "path": "/Users/you/scripts/backup-photos.sh",
      "cwd": "/Users/you",
      "timeout_seconds": 600
    },
    "rotate-logs": {
      "runner": "/bin/sh",
      "path": "/Users/you/scripts/rotate-logs.sh"
    },
    "db-cleanup": {
      "runner": "/usr/bin/python3",
      "path": "/Users/you/scripts/db_cleanup.py",
      "timeout_seconds": 120
    }
  }
}
```

### Field rules

| Field             | Required | Default                  | Notes |
|-------------------|----------|--------------------------|-------|
| `runner`          | yes      | —                        | Interpreter invoked as the single executable. Any absolute path or PATH-resolved name (`/bin/sh`, `/bin/zsh`, `/bin/bash`, `/usr/bin/python3`). Executed as `argv = [runner, path]`. Single string only — no extra runner flags in v1. |
| `path`            | yes      | —                        | Script file. Absolute, or resolved relative to the config dir (same `resolvePath` rule as other paths). |
| `cwd`             | no       | directory containing `path` | Working directory for the process. |
| `timeout_seconds` | no       | `600`                    | Hard timeout enforced via context. On expiry the process is killed and the job ends `failed` with a timeout message. Scripts-only for now; deploy/backup unchanged. |

`runner` is a single string → the child is spawned via `exec.Command(runner, path)`.
The script's own shebang and content decide everything else. To pass interpreter
flags, set them inside the script or pick a `runner` wrapper; v1 does not support a
`runner_args` array.

## Name rules

Script map keys must match `^[a-z0-9._-]+$`. Enforced in `validate()` and documented
in `docs/SETUP.md`. Names are used in: URLs, the job storage namespace, logs, and
meta. Empty / uppercase / slash / space names are rejected at config load.

The project id `scripts` is reserved and rejected by project validation to keep the
`jobs_root/scripts/` namespace unambiguous.

## Validation

Follows the existing `applyDefaults` → `validate` conventions.

**At config load (`config.go`):**
- `scripts` map may be absent (feature optional). If present and empty → error
  (`config.scripts must not be empty`) to match the "projects must not be empty" rule.
- For each entry:
  - name matches `^[a-z0-9._-]+$`, else
    `config.scripts.<name> invalid (must match ^[a-z0-9._-]+$)`;
  - `runner` non-empty and exists via `validateExecutablePath` (same helper used for
    git/docker), else `config.scripts.<name>.runner: <error>`;
  - `path` non-empty, resolved relative to config dir;
  - `cwd` resolved relative to config dir if relative (default applied in
    `applyDefaults`: `filepath.Dir(path)`);
  - `timeout_seconds` > 0 if set (default 600 applied in `applyDefaults`).
- Project validation rejects any project whose id is `scripts`.

**Script file existence:**
- NOT hard-validated at load (paths may be created later / mounted later).
- Hard-checked at run-time preflight (same pattern as backup sources). A missing
  `path` or `cwd` at run time → job created as `running`, set to `failed` in the
  preflight phase before exec, with a clear error in meta + log. (Creating the job
  first means the caller still gets a job id to poll.)

## Execution model

1. `POST /v1/scripts/{name}` → `Service.StartScript(name)`.
2. Resolve `ScriptConfig`. If name unknown → `404 not found`.
3. Create job meta (`kind=script`, `phase=preflight`, `status=running`) in the
   scripts store under namespace = name. Return `202` + accepted response
   immediately.
4. Goroutine runs `runScript(meta, cfg)`:
   - `phase=preflight`: stat `runner` executable, `path` file, `cwd` dir. Any
     missing → `finish(failed)` with a `JobError` (step `preflight`).
   - `phase=script`: build `argv = [runner, path]`, derive `timeout` context via
     `context.WithTimeout(ctx, timeoutSeconds)`, run via `s.runLogged(...)` (streams
     every stdout/stderr line to the job log, exactly like deploy).
   - On context deadline → process killed by `exec.CommandContext`; record a
     `JobError` with step `script`, message `script timed out after Ns`.
   - Exit code 0 → `finish(success)`; non-zero → `finish(failed)` with exit code
     captured in `JobError.ExitCode` and the failing argv in `JobError.Command`.
5. **No locking anywhere.** Scripts never touch `projectlock`, never create a lock
   file, never call `reserveProject`/`releaseProject`.

### Concurrency (fully independent)

- A script never blocks and is never blocked by any deploy/restart/backup.
- A script never blocks and is never blocked by another script — including a second
  concurrent run of the **same** script name.
- Each invocation gets its own job (ULID-unique job dir). Individual job dirs/logs
  never collide.
- The per-name `latest` pointer is written atomically on job creation. Under
  concurrent runs of the same script it is last-writer-wins. This is intentional and
  acceptable: every job is still reachable by id via `jobs/{id}`. (`latest` is a
  convenience pointer, not a correctness-critical record.)

## Job model

```go
const (
    // existing kinds ...
    JobKindScript JobKind = "script"
)

const (
    // existing phases ...
    JobPhaseScript JobPhase = "script"
)
```

New meta field (omitempty, script jobs only):

```go
type JobScriptState struct {
    Name   string `json:"name"`
    Runner string `json:"runner"`
    Path   string `json:"path"`
    Cwd    string `json:"cwd"`
}

type JobMeta struct {
    // ... existing fields ...
    Script *JobScriptState `json:"script,omitempty"`
}
```

`JobMeta.ProjectID` is reused as the storage namespace for script jobs and holds the
**script name**. `JobKind = "script"` disambiguates it from project jobs. (This keeps
the store generic; `cloneMeta` must copy the new `Script` field.)

Status flows: `running → success | failed`. No `rolled_back`/`rollback_failed`.

### What is recorded

- Console output only: full stdout + stderr, line-streamed into `log.txt` with
  timestamps (existing `AppendLog`).
- Meta carries the script identity (`Script`) and, on failure, a `JobError`
  (step, message, exit code, argv). No env, no args (none exist in v1), no extra
  structured output.

## API

Parallel to the project surface, same bearer auth as all non-healthz endpoints.

| Method | Path | Body | Notes |
|--------|------|------|-------|
| `POST` | `/v1/scripts/{name}` | none / empty | Start script. `202` + accepted response. Unknown name → `404`. |
| `GET`  | `/v1/scripts/{name}/jobs/latest` | — | Latest job meta for that script. |
| `GET`  | `/v1/scripts/{name}/jobs/latest/log?tail=N` | — | Latest job log. |
| `GET`  | `/v1/scripts/{name}/jobs/{id}` | — | Specific job meta. |
| `GET`  | `/v1/scripts/{name}/jobs/{id}/log?tail=N` | — | Specific job log. |

`tail` semantics and `max_tail_lines` cap are identical to the project endpoints.

Request/response examples:

```http
POST /v1/scripts/backup-photos HTTP/1.1
Authorization: Bearer <token>

HTTP/1.1 202 Accepted
{
  "job_id": "01J...",
  "project_id": "backup-photos",
  "kind": "script",
  "status": "running",
  "phase": "preflight",
  "created_at": "2026-06-16T12:40:00Z",
  "started_at": "2026-06-16T12:40:00Z",
  "script": {
    "name": "backup-photos",
    "runner": "/bin/bash",
    "path": "/Users/you/scripts/backup-photos.sh",
    "cwd": "/Users/you"
  }
}
```

```http
GET /v1/scripts/backup-photos/jobs/latest HTTP/1.1

HTTP/1.1 200 OK
{
  "job_id": "01J...",
  "project_id": "backup-photos",
  "kind": "script",
  "status": "success",
  "phase": "done",
  "created_at": "2026-06-16T12:40:00Z",
  "started_at": "2026-06-16T12:40:00Z",
  "finished_at": "2026-06-16T12:40:12Z",
  "script": { "name": "backup-photos", "runner": "/bin/bash", "path": "...", "cwd": "..." }
}
```

Error cases (reuse `ServiceError` mapping):
- Unknown script name → `404 not found` ("script not found").
- `scripts` feature not configured (map absent) and a script requested → `404`.
- Missing `path`/`cwd` at preflight → `202` accepted, then job resolves to `failed`
  with a preflight `JobError` (poll `jobs/{id}` to see it).
- Timeout → `202` accepted, then job resolves to `failed` with `script timed out`.
- Malformed `tail` → `400` (existing handler).

## Startup recovery

`startup.RecoverRunningJobs` is store-generic. `main.go` will hold two stores:

```go
projectStore, _ := jobs.NewStore(filepath.Join(cfg.Paths.JobsRoot))       // existing
scriptStore,   _ := jobs.NewStore(filepath.Join(cfg.Paths.JobsRoot, "scripts"))
```

and call recovery on both:

```go
startup.RecoverRunningJobs(projectStore, cfg.Paths.RuntimeRoot)
startup.RecoverRunningJobs(scriptStore,   cfg.Paths.RuntimeRoot)
```

Both will mark any `running` jobs left from a crash as `interrupted` and append the
`JOB interrupted during startup recovery` line. The shared `runtime_root/locks`
cleanup in recovery is unaffected (scripts never write lock files).

## Implementation checklist

1. `internal/model/types.go`
   - `JobKindScript`, `JobPhaseScript`.
   - `ScriptConfig`, `JobScriptState`.
   - Add `Scripts map[string]ScriptConfig` to `Config`, `Script *JobScriptState` to `JobMeta`.
2. `internal/config/config.go`
   - `applyDefaults`: resolve `runner`/`path`/`cwd`, default `cwd = dir(path)`, default `timeout_seconds = 600`.
   - `validate`: name charset, required fields, `validateExecutablePath(runner)`, `timeout_seconds > 0`; reject project id `scripts`.
   - Reject empty `scripts` map if present.
3. `internal/update/service.go`
   - Hold `scriptStore *jobs.Store`.
   - `StartScript(ctx, name)`: resolve config, create job, spawn `runScript` goroutine (no lock).
   - `runScript`: preflight → `script` phase via `runLogged` with timeout context → `finish`.
   - `GetScriptJob` / `GetLatestScriptJob` / `GetScriptJobLog` / `GetLatestScriptJobLog` (delegate to scriptStore).
   - Extend `cloneMeta` to copy `Script`.
4. `internal/api/http.go`
   - Route `segments[1] == "scripts"` to the script endpoints (POST start; GET latest/latest-log/job/job-log).
   - No body parsing for the start endpoint.
5. `cmd/host-updater/main.go`
   - Create the second store, pass it to the service, run recovery on both.
6. `docs/SETUP.md` + `README.md`
   - Document `scripts`, the name charset, defaults, the reserved `scripts` project id, and the endpoints.
7. `configs/config.example.json`
   - Add a commented `scripts` example block.

## Out of scope (v1)

- Request-time args (`args`, `argv`). Scripts are fully config-defined.
- `runner_args` / interpreter flags. Single `runner` string only.
- Rollback for scripts.
- Timeouts applied to deploy/backup (script-only for now).
- A `GET /v1/scripts` index endpoint (list configured scripts).
- Env injection (`env` map). Can be added later if needed; today a script sets its
  own environment.
