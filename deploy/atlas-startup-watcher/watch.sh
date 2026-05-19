#!/bin/sh
# atlas-startup-watcher — react to security-atlas-atlas start events,
# extract fresh bootstrap credential from logs + stable creds from .env,
# post formatted message to DISCORD_INFRA_CHANNEL_ID.
set -eu

# docker:cli ships with busybox + docker CLI. Add curl + jq for the Discord
# POST payload. Suppress noise; tolerate failure (we error harder later
# if these are actually missing).
apk add --no-cache curl jq >/dev/null 2>&1 || true

post_discord() {
    msg="$1"
    # Discord caps messages at 2000 chars. Truncate defensively.
    [ "$(echo -n "$msg" | wc -c)" -gt 1900 ] && msg="$(echo "$msg" | head -c 1900)…"

    http_code="$(
        curl -s -o /tmp/discord-resp -w '%{http_code}' -X POST \
            -H "Authorization: Bot $DISCORD_BOT_TOKEN" \
            -H 'Content-Type: application/json' \
            "https://discord.com/api/v10/channels/$DISCORD_INFRA_CHANNEL_ID/messages" \
            -d "$(jq -nc --arg c "$msg" '{content:$c}')"
    )"
    if [ "$http_code" = "200" ]; then
        echo "[watcher] posted OK"
    else
        echo "[watcher] post FAILED HTTP $http_code: $(cat /tmp/discord-resp)"
    fi
}

push_credentials() {
    trigger="$1"
    # Atlas writes startup logs over ~2-10s after container start. Wait 15s
    # to be safe; this is the latency floor.
    sleep 15

    bootstrap_bearer="$(
        docker logs --tail 200 "$ATLAS_CONTAINER" 2>&1 \
        | grep -E 'bootstrap credential issued' \
        | tail -1 \
        | grep -oE 'bearer=[a-f0-9]+' \
        | cut -d= -f2
    )"
    fixed_token="$(grep '^ATLAS_BOOTSTRAP_TOKEN=' /atlas-env | cut -d= -f2-)"
    user_email="$(grep '^ATLAS_DEFAULT_USER_EMAIL=' /atlas-env | cut -d= -f2-)"
    user_pw="$(grep '^ATLAS_DEFAULT_USER_PASSWORD=' /atlas-env | cut -d= -f2-)"
    now="$(date -u +'%Y-%m-%d %H:%M:%S UTC')"

    msg="**🔄 security-atlas restarted** *$trigger* · *$now*

**Web UI:** http://${ATLAS_HOST}:${ATLAS_WEB_PORT}/login

**Fixed-token bearer** (stable across restarts — recommended):
\`\`\`
$fixed_token
\`\`\`

**Bootstrap bearer** (this startup only — rotates on next restart):
\`\`\`
${bootstrap_bearer:-(not in logs yet — atlas may still be writing)}
\`\`\`

**Email/password sign-in** (if that path is enabled):
\`$user_email\` / \`$user_pw\`"

    post_discord "$msg"
}

echo "[watcher] starting; waiting for $ATLAS_CONTAINER start events"

# Initial push so creds land in Discord even if atlas was already up when
# the watcher itself started. This is also the smoke test on first deploy.
push_credentials "watcher startup"

# Long-running event loop. `docker events` blocks; each line is one event.
docker events --filter "container=$ATLAS_CONTAINER" --filter event=start \
    --format '{{.Time}} {{.Action}}' \
| while read -r line; do
    echo "[watcher] event: $line"
    push_credentials "container start"
done
