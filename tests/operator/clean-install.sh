#!/usr/bin/env bash
set -euo pipefail

BINARY="${1:-./bin/ion}"
if [[ ! -x "$BINARY" ]]; then
  echo "operator clean-install: executable not found: $BINARY" >&2
  exit 1
fi
BINARY="$(realpath "$BINARY")"

ROOT="$(mktemp -d)"
DATA="$ROOT/data"
LOG="$ROOT/dashboard.log"
PORT="${ION_ACCEPTANCE_PORT:-44174}"
PID=""

cleanup() {
  if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
    kill -TERM "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
  rm -rf "$ROOT"
}
trap cleanup EXIT

start_dashboard() {
  : >"$LOG"
  "$BINARY" dashboard \
    --dev-file-kek \
    --data-dir "$DATA" \
    --listen "127.0.0.1:$PORT" \
    --origin "http://127.0.0.1:$PORT" >"$LOG" 2>&1 &
  PID=$!
  for _ in {1..100}; do
    if curl --fail --silent "http://127.0.0.1:$PORT/" >/dev/null; then
      return
    fi
    if ! kill -0 "$PID" 2>/dev/null; then
      cat "$LOG" >&2
      exit 1
    fi
    sleep 0.05
  done
  cat "$LOG" >&2
  echo "operator clean-install: dashboard did not become ready" >&2
  exit 1
}

stop_dashboard() {
  kill -TERM "$PID"
  wait "$PID"
  PID=""
}

"$BINARY" init --dev-file-kek --data-dir "$DATA"
start_dashboard
"$BINARY" tui --attach --check --data-dir "$DATA" --startup-timeout 5s
ACTOR_BEFORE="$(cat "$DATA/operator/actor-id")"
stop_dashboard

start_dashboard
"$BINARY" tui --attach --check --data-dir "$DATA" --startup-timeout 5s
ACTOR_AFTER="$(cat "$DATA/operator/actor-id")"
stop_dashboard

if [[ "$ACTOR_BEFORE" != "$ACTOR_AFTER" ]]; then
  echo "operator clean-install: operator identity changed across restart" >&2
  exit 1
fi
if find "$DATA/operator" -maxdepth 1 -type s -print -quit | grep -q .; then
  echo "operator clean-install: socket remained after clean shutdown" >&2
  exit 1
fi

echo "operator clean-install: dashboard, terminal client, and restart checks passed"
