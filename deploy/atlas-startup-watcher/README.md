# atlas-startup-watcher

Tiny sidecar container that watches for `security-atlas-atlas` container start events and posts the current bearer tokens + sign-in credentials to a Discord channel. Saves you from SSH'ing to Unraid + grepping logs every time atlas restarts (which happens on every Watchtower pull).

## What it does

Subscribes to `docker events --filter container=<atlas> --filter event=start`. On each detected start (including the watcher's own startup, so creds land in Discord even when atlas was already running):

1. Sleeps 15s (lets atlas write its startup logs)
2. Extracts the rotating **bootstrap bearer** from atlas's stderr (`bootstrap credential issued ... bearer=<hex>`)
3. Reads the stable **fixed-token bearer**, **default user email**, and **default user password** from atlas's mounted `.env`
4. Formats a Markdown message and POSTs to the configured Discord channel via the bot's REST API

Latency: ~15-20s from atlas-restart to Discord message.

## What gets posted

````
🔄 security-atlas restarted *<trigger>* · *<UTC timestamp>*

Web UI: http://<ATLAS_HOST>:<ATLAS_WEB_PORT>/login

Fixed-token bearer (stable across restarts — recommended):
```<the stable token>```

Bootstrap bearer (this startup only — rotates on next restart):
```<fresh per-startup token>```

Email/password sign-in (if that path is enabled):
<email> / <password>
````

`<trigger>` is one of:

- `watcher startup` — the watcher itself just (re)started; this is also what fires on first deploy
- `container start` — atlas restarted (Watchtower pull, manual restart, container recreate)

## ⚠️ Security trade-off

Both bearer tokens and the user password land in the Discord channel **in plaintext** and persist in the channel scrollback indefinitely. **Anyone with read access to the channel gets atlas.**

This is acceptable for a closed personal homelab channel — the convenience-vs-risk math is fine when only you can see the channel. It is **not acceptable** if you ever:

- Invite a teammate to the Discord server with broad channel visibility
- Change the channel's permission model (it should be private / restricted to you)
- Set up the server to share the channel with a bot or webhook that mirrors elsewhere

If any of those happen, rotate the atlas tokens AND consider switching the watcher to post truncated hints (e.g. `Bearer: 8922206f…11d5 — full value in 1Password`) rather than full values.

## Install

```bash
# 1. Stage files on Unraid
SSH_AUTH_SOCK="" ssh -i /tmp/unraid_key2 -o IdentitiesOnly=yes -o "IdentityAgent none" \
    root@192.168.1.246 "mkdir -p /mnt/user/appdata/atlas-startup-watcher"

scp -i /tmp/unraid_key2 -o IdentitiesOnly=yes -o "IdentityAgent none" \
    deploy/atlas-startup-watcher/docker-compose.yml \
    deploy/atlas-startup-watcher/watch.sh \
    deploy/atlas-startup-watcher/.env.example \
    root@192.168.1.246:/mnt/user/appdata/atlas-startup-watcher/

# 2. Create .env on Unraid (don't ship the real one in git)
SSH_AUTH_SOCK="" ssh -i /tmp/unraid_key2 -o IdentitiesOnly=yes -o "IdentityAgent none" \
    root@192.168.1.246 \
    "cd /mnt/user/appdata/atlas-startup-watcher && cp .env.example .env && chmod 600 .env"

# 3. Edit the .env to fill in DISCORD_BOT_TOKEN + DISCORD_INFRA_CHANNEL_ID.
#    If you have a personal-ai project, the canonical source is:
#      ~/Development/personal-ai/.env  (or wherever its .env lives)
#    Strip inline `# comment` suffixes when copying values.

# 4. Bring up
SSH_AUTH_SOCK="" ssh -i /tmp/unraid_key2 -o IdentitiesOnly=yes -o "IdentityAgent none" \
    root@192.168.1.246 \
    "cd /mnt/user/appdata/atlas-startup-watcher && docker compose up -d"

# 5. Verify (Discord should receive the initial-startup message within 15-20s)
SSH_AUTH_SOCK="" ssh -i /tmp/unraid_key2 -o IdentitiesOnly=yes -o "IdentityAgent none" \
    root@192.168.1.246 "docker logs --tail 10 atlas-startup-watcher"
```

Expected log shape on a clean install:

```
[watcher] starting; waiting for security-atlas-atlas start events
[watcher] posted OK
[watcher] event: 1779142834 start    ← only if atlas restarts after watcher startup
[watcher] posted OK
```

## How it's wired

```
┌─────────────────────────────┐         ┌──────────────────┐
│ security-atlas-atlas        │         │ Discord channel  │
│   stderr: bootstrap creds   │         │   (UC-16: infra) │
└─────┬───────────────────────┘         └──────────────────┘
      │ docker logs                                  ▲
      │                                              │
      │      ┌─────────────────────────────┐         │ REST API
      │      │ atlas-startup-watcher       │         │ (Bot auth)
      └─────►│   docker events --filter    │─────────┘
             │   docker logs <container>   │
             │   curl Discord API          │
             └──────┬──────────────────────┘
                    │
                    ▼
        /mnt/user/appdata/security-atlas/.env
        (mounted read-only; fixed-token + email/pw)
```

## Files

| File                 | Role                                                                                                                                                         |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `docker-compose.yml` | Single-service definition. Image `docker:cli` (busybox + docker CLI baked in). Mounts `/var/run/docker.sock:ro`, atlas's `.env:ro`, and the watch script.    |
| `watch.sh`           | The event-loop. Installs `curl` + `jq` at startup (busybox doesn't ship them); does an initial push; then `docker events` blocks waiting for atlas restarts. |
| `.env.example`       | Template — copy to `.env` on Unraid and fill in two required values. `.env` itself stays out of git.                                                         |

## Operational notes

### Watchtower discipline

The container has **NO** `com.centurylinklabs.watchtower.enable=true` label. `watch.sh` is hand-rolled and the `docker:cli` base image's CLI surface changes occasionally between minor versions in ways that break the watch script (we hit one such break during initial deploy — `docker events --format '{{.Status}}'` was deprecated to `{{.Action}}`). Manual upgrade only — read the docker-cli release notes before bumping.

### Editing the message format

The Markdown payload is built in `push_credentials()` between `msg=` and the `post_discord` call. The format string is plain bash; edit freely. After editing:

```bash
scp watch.sh root@192.168.1.246:/mnt/user/appdata/atlas-startup-watcher/
SSH ... "cd /mnt/user/appdata/atlas-startup-watcher && docker compose restart"
```

The restart fires a fresh initial push, so you'll see the new format immediately.

### Common failure modes

| Symptom                                        | Cause                                                                       | Fix                                                                                                                                                                              |
| ---------------------------------------------- | --------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------- |
| `post FAILED HTTP 401`                         | Bot token revoked or rotated                                                | Update `DISCORD_BOT_TOKEN` in `.env`; `docker compose restart`                                                                                                                   |
| `post FAILED HTTP 403`                         | Bot not in channel, or channel ACL changed                                  | Add the bot to the channel; verify `DISCORD_INFRA_CHANNEL_ID` is correct                                                                                                         |
| `post FAILED HTTP 404`                         | `DISCORD_INFRA_CHANNEL_ID` wrong (e.g. has inline `# comment` not stripped) | Re-extract the ID; verify it's exactly 18-19 digits                                                                                                                              |
| `[watcher] event: ... start` never fires       | Watching the wrong container name                                           | Verify `ATLAS_CONTAINER` matches the actual atlas container name (`docker ps`)                                                                                                   |
| Discord message shows empty `bootstrap bearer` | Atlas didn't log it within the 15s sleep window                             | Either atlas hasn't fully started (slow disk?) or the log-line regex broke. Check `docker logs security-atlas-atlas                                                              | grep bootstrap` directly. |
| Watcher container restart-loops                | `set -eu` killed the script on an undefined variable expansion              | Check `docker logs atlas-startup-watcher`; usually a Markdown `_$var_` collision (single-char trailing/leading `_` is a valid identifier char). Use `*var*` or `${var}` instead. |

### Cost

Negligible:

- Container: ~10 MB image + ~5 MB resident memory
- Network: 1 POST per atlas-restart (maybe a few per day)
- Discord rate limits: not approached (the API allows ~5 messages/5s; we send ~1 per restart)

### Removing it

```bash
SSH ... "cd /mnt/user/appdata/atlas-startup-watcher && docker compose down && \
         cd .. && rm -rf atlas-startup-watcher"
```

The atlas-side `.env` (the source of the credentials) is untouched.

## Provenance

Initial deploy + this PR generated 2026-05-18. The watcher idea came from the realization that the bootstrap bearer rotates on every atlas restart (every Watchtower pull), so manually re-fetching it via SSH every time was a high-friction loop. Posting to the existing personal-ai Discord channel (UC-16 infra alerts) reuses the bot the operator already has running.
