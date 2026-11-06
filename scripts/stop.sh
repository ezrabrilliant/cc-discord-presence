#!/bin/bash
# Stop Discord Rich Presence daemon
# WARNING: Linux support is untested. Please report issues on GitHub.

# Configuration
CLAUDE_DIR="$HOME/.claude"
PID_FILE="$CLAUDE_DIR/discord-presence.pid"
SESSIONS_DIR="$CLAUDE_DIR/discord-presence-sessions"
REFCOUNT_FILE="$CLAUDE_DIR/discord-presence.refcount" # replaced by SESSIONS_DIR on Windows
ENDED_DIR="$CLAUDE_DIR/discord-presence-ended"
STALE_MINUTES=720
CLAUDE_PROCESS="${CC_DISCORD_PRESENCE_CLAUDE_PROCESS:-claude}" # tests use a stand-in

# Hook input: JSON from Claude Code on stdin; none when run by hand
HOOK_INPUT=""
[[ -t 0 ]] || HOOK_INPUT=$(cat)

# hook_field NAME prints a string field of the hook input
hook_field() {
    printf '%s' "$HOOK_INPUT" | tr -d '\r\n' | sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

SESSION_ID=$(hook_field session_id | tr -cd 'A-Za-z0-9_-')

# Record that this session ended: the daemon then skips its transcript, the
# freshest just now, until it changes again (as on --resume). Records go
# after a day.
if [[ -n "$SESSION_ID" ]]; then
    mkdir -p "$ENDED_DIR"
    touch "$ENDED_DIR/$SESSION_ID"
    find "$ENDED_DIR" -type f -mmin +1440 -delete 2>/dev/null
fi

# Detect platform
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
IS_WINDOWS=false
case "$OS" in
    mingw*|msys*|cygwin*) IS_WINDOWS=true ;;
esac

# Cross-platform process operations
process_exists() {
    local pid=$1
    if $IS_WINDOWS; then
        tasklist //FI "PID eq $pid" 2>/dev/null | grep -q "$pid"
    else
        kill -0 "$pid" 2>/dev/null
    fi
}

# The pid file outlives a daemon that exited (reboot, Discord not running),
# and the OS may hand its pid to another program: trust it only when that
# pid runs our binary.
daemon_running() {
    local pid=$1
    [[ -n "$pid" ]] || return 1
    if $IS_WINDOWS; then
        tasklist //FI "PID eq $pid" //FO CSV //NH 2>/dev/null | grep -q '^"cc-discord-presence-'
    else
        ps -p "$pid" -o args= 2>/dev/null | grep -q 'cc-discord-presence-'
    fi
}

kill_process() {
    local pid=$1
    if $IS_WINDOWS; then
        taskkill //F //PID "$pid" >/dev/null 2>&1
    else
        kill "$pid" 2>/dev/null
    fi
}

# A session that crashed or whose window was closed never runs SessionEnd.
# Drop markers whose transcript (or, lacking one, the marker itself) hasn't
# changed for STALE_MINUTES.
prune_stale_sessions() {
    local marker transcript
    for marker in "$SESSIONS_DIR"/*; do
        [[ -f "$marker" ]] || continue
        transcript=$(cat "$marker")
        [[ -n "$transcript" && -f "$transcript" ]] || transcript=$marker
        if [[ -n "$(find "$transcript" -mmin +"$STALE_MINUTES" 2>/dev/null)" ]]; then
            rm -f "$marker"
        fi
    done
}

# claude_code_args prints the command line of each process named claude, one
# per line. On Windows the Claude desktop app, a claude.exe under
# AnthropicClaude, is left out; tasklist shows no command lines.
claude_code_args() {
    if $IS_WINDOWS; then
        powershell.exe -NoProfile -NonInteractive -Command "Get-CimInstance Win32_Process -Filter \"Name='$CLAUDE_PROCESS.exe'\" | Where-Object { \$_.ExecutablePath -notlike '*\\AnthropicClaude\\*' } | ForEach-Object { [string]\$_.CommandLine }" 2>/dev/null | tr -d '\r'
    else
        local pid
        for pid in $(pgrep -x "$CLAUDE_PROCESS" 2>/dev/null); do
            ps -p "$pid" -o args= 2>/dev/null
        done
    fi
}

# is_helper ARGS succeeds for a claude process that is no session: the Claude
# in Chrome native host, which runs for as long as Chrome is open, or
# claude mcp serve. The daemon skips the same ones (claudeCodeHelper).
is_helper() {
    local arg
    local -a args
    read -ra args <<< "$(printf '%s' "$1" | tr -d '"')"
    [[ "${args[1]} ${args[2]}" == "mcp serve" ]] && return 0
    for arg in "${args[@]:1}"; do
        [[ "$arg" == "--chrome-native-host" ]] && return 0
    done
    return 1
}

# claude_code_count prints how many Claude Code sessions run, the ending
# session's own included (it runs its SessionEnd hook).
claude_code_count() {
    local args count=0
    while IFS= read -r args || [[ -n "$args" ]]; do
        is_helper "$args" || count=$((count + 1))
    done < <(claude_code_args)
    echo "$count"
}

count_sessions() {
    local marker count=0
    for marker in "$SESSIONS_DIR"/*; do
        [[ -f "$marker" ]] && count=$((count + 1))
    done
    echo "$count"
}

# Session tracking: Windows uses one marker per session id, Unix uses PID files
if $IS_WINDOWS; then
    rm -f "$REFCOUNT_FILE"
    rm -f "$SESSIONS_DIR/${SESSION_ID:-manual}"
    prune_stale_sessions
    ACTIVE_SESSIONS=$(count_sessions)

    if [[ $ACTIVE_SESSIONS -gt 0 ]]; then
        echo "Discord Rich Presence still in use by $ACTIVE_SESSIONS session(s)"
        exit 0
    fi
else
    SESSION_PID="${PPID:-$$}"
    rm -f "$SESSIONS_DIR/$SESSION_PID"

    # Count remaining active sessions and clean up orphans
    ACTIVE_SESSIONS=0
    if [[ -d "$SESSIONS_DIR" ]]; then
        for session_file in "$SESSIONS_DIR"/*; do
            [[ -f "$session_file" ]] || continue
            pid=$(basename "$session_file")
            if process_exists "$pid"; then
                ACTIVE_SESSIONS=$((ACTIVE_SESSIONS + 1))
            else
                rm -f "$session_file"
            fi
        done
    fi

    if [[ $ACTIVE_SESSIONS -gt 0 ]]; then
        echo "Discord Rich Presence still in use by $ACTIVE_SESSIONS session(s)"
        exit 0
    fi
    rm -rf "$SESSIONS_DIR"
fi

# Sessions started before the plugin was installed, or by an older version
# of it, ran no SessionStart hook and have no marker, but still run. A hook
# (not a manual stop) leaves the daemon to them.
if [[ -n "$SESSION_ID" ]] && (( $(claude_code_count) > 1 )); then
    echo "Discord Rich Presence still in use by another Claude Code session"
    exit 0
fi

# Stop the daemon
if [[ -f "$PID_FILE" ]]; then
    PID=$(cat "$PID_FILE")
    if daemon_running "$PID"; then
        kill_process "$PID"
        echo "Discord Rich Presence stopped (PID: $PID)"
    fi
    rm -f "$PID_FILE"
else
    # Fallback: kill by process name
    if $IS_WINDOWS; then
        taskkill //F //IM "cc-discord-presence-windows-amd64.exe" >/dev/null 2>&1 || true
    else
        pkill -f cc-discord-presence 2>/dev/null || true
    fi
fi
