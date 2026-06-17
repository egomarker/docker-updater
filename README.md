# docker-updater

Small host-side HTTP service for Git + Docker Compose deploy/restart/backup/script jobs.

## Scope

v1 behavior:

- file-backed jobs and logs
- one updater-managed mutable image per project
- deploy via `git fetch` + `git checkout` + `git pull --ff-only` + `docker build` + `docker compose up -d --force-recreate`
- restart via `docker compose restart`
- backup via timestamped zip with optional folder exclusions and retention
- run predefined host scripts via config-defined runners, paths, cwd, and timeouts
- runtime rollback only on deploy cutover failure
- no repo restore
- no app health checks
- no arbitrary request-time shell execution

## Layout

- binary entrypoint: `cmd/host-updater/main.go`
- example config: `configs/config.example.json`
- launchd sample: `launchd/com.example.host-updater.plist`
- setup guide: `docs/SETUP.md`

## Default config path

- `~/Library/Application Support/host-updater/config.json`

## API

- `GET /v1/healthz`
- `POST /v1/projects/{project}/deploy`
- `POST /v1/projects/{project}/restart`
- `POST /v1/projects/{project}/backup`
- `GET /v1/projects/{project}/jobs/{id}`
- `GET /v1/projects/{project}/jobs/{id}/log?tail=N`
- `GET /v1/projects/{project}/jobs/latest`
- `GET /v1/projects/{project}/jobs/latest/log?tail=N`
- `GET /v1/scripts`
- `POST /v1/scripts/{name}`
- `GET /v1/scripts/{name}/jobs/{id}`
- `GET /v1/scripts/{name}/jobs/{id}/log?tail=N`
- `GET /v1/scripts/{name}/jobs/latest`
- `GET /v1/scripts/{name}/jobs/latest/log?tail=N`
- `POST /v1/notify/test`

All endpoints except `/v1/healthz` require:

- `Authorization: Bearer <token>`

## Example config

Start from `configs/config.example.json` and adjust paths, token file, image tag, compose files, and project names.

Backup config rules:

- `backup.destination` must be outside every `backup.sources` tree
- `backup.sources` must have unique basenames in v1 because archive entries are stored as `<source>/...`

Script config rules:

- top-level `scripts` is optional, but if present must not be empty
- script names must match `^[a-z0-9._-]+$`
- project id `scripts` is reserved
- script `cwd` defaults to the directory containing `path`
- script `timeout_seconds` defaults to `600`

Notify (ntfy) config rules:

- top-level `notify` is optional
- `notify.ntfy.base_url` is required and must start with `http://` or `https://`
- `notify.ntfy.topic` is required and must match `^[a-zA-Z0-9_-]{1,64}$`
- `notify.ntfy.token` is optional (Bearer token for self-hosted ntfy auth)
- `notify.ntfy.priority` defaults to `3`; failures always notify at priority `5`
- `notify.ntfy.timeout_seconds` defaults to `5`
- `notify.ntfy.attach_log_on_failure` defaults to `true`
- `notify.ntfy.max_log_bytes` defaults to `262144` (256 KiB); larger logs are tailed to the last N bytes
- notifications are best-effort: a failed send never affects the job status

## Example curl

```bash
curl http://127.0.0.1:8765/v1/healthz

curl -X POST \
  -H 'Authorization: Bearer YOUR_TOKEN' \
  -H 'Content-Type: application/json' \
  http://127.0.0.1:8765/v1/projects/{project}/deploy \
  -d '{"origin":"fork","branch":"feature/telegram","pull_base":true,"use_cache":true}'

curl -H 'Authorization: Bearer YOUR_TOKEN' \
  http://127.0.0.1:8765/v1/projects/{project}/jobs/latest

curl -H 'Authorization: Bearer YOUR_TOKEN' \
  'http://127.0.0.1:8765/v1/projects/{project}/jobs/latest/log?tail=100'

curl -H 'Authorization: Bearer YOUR_TOKEN' \
  http://127.0.0.1:8765/v1/scripts
```

The scripts list endpoint returns configured script names only:

```json
{"scripts":["rotate-logs","db-cleanup"]}
```

```bash
curl -X POST \
  -H 'Authorization: Bearer YOUR_TOKEN' \
  http://127.0.0.1:8765/v1/scripts/rotate-logs

curl -H 'Authorization: Bearer YOUR_TOKEN' \
  http://127.0.0.1:8765/v1/scripts/rotate-logs/jobs/latest
```

## Build

```bash
make build
```

## launchd

Edit `launchd/com.example.host-updater.plist` with absolute paths, then load it with `launchctl` under the target macOS user.
