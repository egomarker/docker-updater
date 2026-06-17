# host-updater setup on macOS

This is the final build + setup procedure for running `host-updater` on your Mac and managing a Docker Compose project.

## Replace these placeholders first

Before running the commands below, replace these values with your real ones:

- `<project>` — updater project id, e.g. `myapp`
- `<project-dir>` — absolute path to the managed repo, e.g. `~/path/to/<project>`
- `<compose-service>` — Docker Compose service name to restart / redeploy
- `<image>` — mutable image tag prefix, e.g. `myapp`
- `<origin>` — git remote name to deploy from, e.g. `origin` or `fork`
- `<branch>` — git branch name to deploy
- `<launchd-label>` — launchd label, e.g. `com.example.host-updater`

Common assumptions in the examples below:

- updater source repo: `~/docker-updater`
- git binary: `/usr/bin/git`
- docker binary: `/usr/local/bin/docker`

---

## 1. Build the updater

```bash
mkdir -p "$HOME/bin"
cd "$HOME/docker-updater"

gofmt -w $(find . -name '*.go' -type f)
go build -o "$HOME/bin/host-updater" ./cmd/host-updater
```

Sanity check:

```bash
ls -l "$HOME/bin/host-updater"
"$HOME/bin/host-updater" -version
```

---

## 2. Create updater directories

```bash
mkdir -p \
  "$HOME/Library/Application Support/host-updater/jobs" \
  "$HOME/Library/Application Support/host-updater/runtime" \
  "$HOME/Library/Logs/host-updater" \
  "$HOME/Library/LaunchAgents"
```

---

## 3. Create bearer token file

```bash
openssl rand -base64 32 > "$HOME/Library/Application Support/host-updater/token.txt"
chmod 600 "$HOME/Library/Application Support/host-updater/token.txt"
```

Optional check:

```bash
ls -l "$HOME/Library/Application Support/host-updater/token.txt"
```

---

## 4. Write the updater config

Write this file:

`$HOME/Library/Application Support/host-updater/config.json`

```bash
cat > "$HOME/Library/Application Support/host-updater/config.json" <<'EOF'
{
  "server": {
    "listen_address": "0.0.0.0:8765",
    "bearer_token_file": "~/Library/Application Support/host-updater/token.txt"
  },
  "paths": {
    "jobs_root": "~/Library/Application Support/host-updater/jobs",
    "runtime_root": "~/Library/Application Support/host-updater/runtime"
  },
  "executables": {
    "git": "/usr/bin/git",
    "docker": "/usr/local/bin/docker",
    "zip": "/usr/bin/zip"
  },
  "limits": {
    "max_tail_lines": 10000
  },
  "projects": {
    "<project>": {
      "repo_dir": "<project-dir>",
      "git": {
        "allowed_origins": ["origin", "fork"],
        "allowed_branch_regexes": [
          "^[A-Za-z0-9._/-]+$"
        ]
      },
      "build": {
        "cwd": "<project-dir>",
        "context_dir": "<project-dir>",
        "dockerfile": "<project-dir>/Dockerfile",
        "latest_tag": "<image>:latest",
        "commit_tag_prefix": "<image>:git-"
      },
      "compose": {
        "cwd": "<project-dir>",
        "files": [
          "<project-dir>/docker-compose.yml"
        ],
        "primary_service": "<compose-service>",
        "services": ["<compose-service>"]
      }
    }
  },
  "scripts": {
    "rotate-logs": {
      "runner": "/bin/sh",
      "path": "/Users/you/scripts/rotate-logs.sh",
      "cwd": "/Users/you",
      "timeout_seconds": 600
    },
    "db-cleanup": {
      "runner": "/usr/bin/python3",
      "path": "/Users/you/scripts/db_cleanup.py",
      "timeout_seconds": 120
    }
  }
}
EOF
```

Notes:

- `allowed_origins` is just an example; adjust it to your actual remotes.
- If your compose file is not `docker-compose.yml`, replace that path too.
- If your project has multiple services sharing the same mutable image, you can list them all in `services`.
- Top-level `scripts` is optional, but if present it must not be empty.
- Script names must match `^[a-z0-9._-]+$`.
- Project id `scripts` is reserved.
- Script `cwd` defaults to the directory containing `path` if omitted.
- Script `timeout_seconds` defaults to `600` if omitted.

Sanity check:

```bash
cat "$HOME/Library/Application Support/host-updater/config.json"
```

---

## 5. Foreground smoke test first

Before launchd, run it directly once:

```bash
"$HOME/bin/host-updater" -config "$HOME/Library/Application Support/host-updater/config.json"
```

In another terminal:

```bash
curl http://127.0.0.1:8765/v1/healthz
```

Expected:

```json
{"status":"ok","version":"1.1.2"}
```

If that works, stop the foreground process with `Ctrl+C`.

If it does **not** work, fix that first before touching launchd.

---

## 6. Write the LaunchAgent plist

Important:

- this must use **absolute paths**
- `launchd` does **not** expand `$HOME` inside plist values
- do **not** use `sudo` for this LaunchAgent

Write the plist like this:

```bash
cat > "$HOME/Library/LaunchAgents/<launchd-label>.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key>
    <string><launchd-label></string>

    <key>ProgramArguments</key>
    <array>
      <string>$HOME/bin/host-updater</string>
      <string>-config</string>
      <string>$HOME/Library/Application Support/host-updater/config.json</string>
    </array>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <true/>

    <key>WorkingDirectory</key>
    <string>$HOME/Library/Application Support/host-updater</string>

    <key>StandardOutPath</key>
    <string>$HOME/Library/Logs/host-updater/service.out.log</string>

    <key>StandardErrorPath</key>
    <string>$HOME/Library/Logs/host-updater/service.err.log</string>
  </dict>
</plist>
EOF
```

Verify it contains **no literal `$HOME`**:

```bash
grep '\$HOME' "$HOME/Library/LaunchAgents/<launchd-label>.plist" && echo "BAD: still contains \$HOME" || echo "OK"
```

Expected:

- `OK`

Also inspect the file:

```bash
cat "$HOME/Library/LaunchAgents/<launchd-label>.plist"
```

You should see real paths like:

- `/Users/<you>/bin/host-updater`
- `/Users/<you>/Library/Application Support/host-updater/config.json`

---

## 7. Load the LaunchAgent

Do **not** use `sudo`.

```bash
launchctl bootout "gui/$(id -u)/<launchd-label>" 2>/dev/null || true
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/<launchd-label>.plist" 2>/dev/null || true

launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/<launchd-label>.plist"
launchctl kickstart -k "gui/$(id -u)/<launchd-label>"
```

Inspect status:

```bash
launchctl print "gui/$(id -u)/<launchd-label>"
```

What you want to see:

- absolute paths in `program` and `arguments`
- not `$HOME/...`
- ideally `state = running`
- no `EX_CONFIG`

---

## 8. Check logs

```bash
tail -100 \
  "$HOME/Library/Logs/host-updater/service.out.log" \
  "$HOME/Library/Logs/host-updater/service.err.log"
```

If startup fails, this is the first place to look.

---

## 9. Verify service is listening

From the Mac host:

```bash
curl http://127.0.0.1:8765/v1/healthz
```

Expected:

```json
{"status":"ok","version":"1.1.2"}
```

From inside a Docker container that should call the updater:

```bash
curl http://host.docker.internal:8765/v1/healthz
```

That confirms container → host connectivity.

---

## 10. Smoke test auth + updater API

On the Mac host:

```bash
TOKEN="$(tr -d '\n' < "$HOME/Library/Application Support/host-updater/token.txt")"
```

### Latest status

```bash
curl -sS \
  -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8765/v1/projects/<project>/jobs/latest
```

### Latest log tail

```bash
curl -sS \
  -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:8765/v1/projects/<project>/jobs/latest/log?tail=100"
```

---

## 11. Smoke test restart

```bash
curl -sS -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://127.0.0.1:8765/v1/projects/<project>/restart \
  -d '{}'
```

Then:

```bash
curl -sS \
  -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8765/v1/projects/<project>/jobs/latest
```

---

## 12. Smoke test deploy

```bash
curl -sS -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://127.0.0.1:8765/v1/projects/<project>/deploy \
  -d '{"origin":"<origin>","branch":"<branch>","pull_base":true,"use_cache":true}'
```

Then inspect:

```bash
curl -sS \
  -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8765/v1/projects/<project>/jobs/latest

curl -sS \
  -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:8765/v1/projects/<project>/jobs/latest/log?tail=200"
```

---

## 12b. Smoke test backup

Requires a `backup` block configured on the project (see `configs/config.example.json`).

Config rules:
- `backup.destination` must be outside every configured `backup.sources` tree
- `backup.sources` must have unique basenames in v1 because archive entries are stored as `<source>/...`

Start a backup:

```bash
curl -sS -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://127.0.0.1:8765/v1/projects/<project>/backup \
  -d '{}'
```

Inspect the result (the `backup.output_zip`, `output_bytes`, `remaining`, and `removed` fields are populated on success):

```bash
curl -sS \
  -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8765/v1/projects/<project>/jobs/latest

curl -sS \
  -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:8765/v1/projects/<project>/jobs/latest/log?tail=200"
```

Verify the archive on disk and its contents:

```bash
ls -lh "$HOME/Library/Application Support/host-updater/backups"
unzip -l "$HOME/Library/Application Support/host-updater/backups"/<project>-backup-*.zip | head
```

Expected:
- entries are `<source>/...` (not absolute paths)
- excluded folders are absent

A backup on a project without a `backup` block returns `400 Bad Request` (`backup not configured for project`).

---

## 12c. Smoke test script jobs

Requires a top-level `scripts` config block and a configured script name.

Start a script:

```bash
curl -sS -X POST \
  -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8765/v1/scripts/rotate-logs
```

Inspect the latest job and log:

```bash
curl -sS \
  -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8765/v1/scripts/rotate-logs/jobs/latest

curl -sS \
  -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:8765/v1/scripts/rotate-logs/jobs/latest/log?tail=200"
```

Expected:
- `kind` is `script`
- `phase` advances through `preflight` → `script` → `done`
- success/failure is recorded in normal job meta and logs

An unknown script name returns `404 Not Found` (`script not found`).

---

## 13. Container-side client values

For any client running in Docker, use:

- base URL: `http://host.docker.internal:8765`
- bearer token: contents of `~/Library/Application Support/host-updater/token.txt`

---

## 14. Important behavior notes

This updater v1 currently means:

- **success** = git/build/compose steps succeeded
- **not** = app health guaranteed after startup

Other key behavior:

- only **one mutable image** per project
- rollback only happens if deploy cutover itself fails
- repo is **not restored** after failed deploy
- repo may remain on attempted branch/commit even if runtime rolls back

---

## 15. If launchd still fails

Run in this order:

### A. Check plist is really expanded

```bash
grep '\$HOME' "$HOME/Library/LaunchAgents/<launchd-label>.plist"
```

This must return **nothing**.

### B. Check launchd’s loaded view

```bash
launchctl print "gui/$(id -u)/<launchd-label>"
```

If you still see `$HOME/...` there, the plist was not rewritten correctly.

### C. Run foreground again

```bash
"$HOME/bin/host-updater" -config "$HOME/Library/Application Support/host-updater/config.json"
```

If it exits, the error printed there is the real startup problem.

### D. Check logs

```bash
tail -100 "$HOME/Library/Logs/host-updater/service.out.log" "$HOME/Library/Logs/host-updater/service.err.log"
```

---

## 16. Restart after rebuilding

If you rebuilt `host-updater` and the LaunchAgent plist did **not** change, restart the running service with:

```bash
launchctl kickstart -k "gui/$(id -u)/<launchd-label>"
```

Then verify:

```bash
launchctl print "gui/$(id -u)/<launchd-label>"
tail -50 "$HOME/Library/Logs/host-updater/service.out.log" \
         "$HOME/Library/Logs/host-updater/service.err.log"
```

If you changed the plist, reload it first:

```bash
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/<launchd-label>.plist" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/<launchd-label>.plist"
launchctl kickstart -k "gui/$(id -u)/<launchd-label>"
```

Do **not** use `sudo` for a user LaunchAgent.

## 17. Minimal success checklist

You’re done when all of these work:

- `"$HOME/bin/host-updater" -version`
- `curl http://127.0.0.1:8765/v1/healthz`
- `launchctl print "gui/$(id -u)/<launchd-label>"` shows absolute paths
- latest job status works with bearer token
- restart works
- deploy returns a `job_id`
