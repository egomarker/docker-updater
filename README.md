# docker-updater

Small host-side HTTP service for Git + Docker Compose deploy/restart jobs.

## Scope

v1 behavior:

- file-backed jobs and logs
- one updater-managed mutable image per project
- deploy via `git fetch` + `git checkout` + `git pull --ff-only` + `docker build` + `docker compose up -d --force-recreate`
- restart via `docker compose restart`
- runtime rollback only on deploy cutover failure
- no repo restore
- no app health checks
- no arbitrary shell execution

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
- `GET /v1/projects/{project}/jobs/{id}`
- `GET /v1/projects/{project}/jobs/{id}/log?tail=N`
- `GET /v1/projects/{project}/jobs/latest`
- `GET /v1/projects/{project}/jobs/latest/log?tail=N`

All endpoints except `/v1/healthz` require:

- `Authorization: Bearer <token>`

## Example config

Start from `configs/config.example.json` and adjust paths, token file, image tag, compose files, and project names.

## Example curl

```bash
curl http://127.0.0.1:8765/v1/healthz

curl -X POST \
  -H 'Authorization: Bearer YOUR_TOKEN' \
  -H 'Content-Type: application/json' \
  http://127.0.0.1:8765/v1/projects/piclaw/deploy \
  -d '{"origin":"fork","branch":"feature/telegram","pull_base":true,"use_cache":true}'

curl -H 'Authorization: Bearer YOUR_TOKEN' \
  http://127.0.0.1:8765/v1/projects/piclaw/jobs/latest

curl -H 'Authorization: Bearer YOUR_TOKEN' \
  'http://127.0.0.1:8765/v1/projects/piclaw/jobs/latest/log?tail=100'
```

## Build

```bash
make build
```

## launchd

Edit `launchd/com.example.host-updater.plist` with absolute paths, then load it with `launchctl` under the target macOS user.
