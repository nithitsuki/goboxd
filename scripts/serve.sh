#!/usr/bin/env bash
# Start goboxd natively on the host (no docker) in the background.
#
# Builds ./goboxd if it is missing or older than the source, then launches
# dev-host.sh under nohup and waits for /healthz to come up. Use `serve.sh
# stop` to stop the instance, or `serve.sh status` to check it.
#
# Requires root (nsjail namespaces + multi-uid chown). Passwordless sudo is
# assumed; the dev-host.sh env vars (GOBOXD_LANGS, PORT, ...) can be exported
# before invoking this script.
set -euo pipefail

cd "$(dirname "$0")/.."

PID_FILE="$(pwd)/.goboxd.pid"
LOG_FILE="$(pwd)/goboxd.log"
PORT="${PORT:-8080}"

# --- commands that don't need a running server -------------------------------
case "${1:-start}" in
  stop)
    if [[ -f "$PID_FILE" ]]; then
      PID="$(cat "$PID_FILE")"
      echo "stopping goboxd (pid $PID)..."
      kill "$PID" 2>/dev/null || true
      rm -f "$PID_FILE"
      echo "stopped"
    else
      echo "no pid file ($PID_FILE); is it running?"
    fi
    exit 0
    ;;
  status)
    if [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      echo "goboxd running (pid $(cat "$PID_FILE")) on http://localhost:$PORT"
      curl -s -o /dev/null -w "healthz: %{http_code}\n" "http://localhost:$PORT/healthz" || true
    else
      echo "goboxd not running"
    fi
    exit 0
    ;;
  start) ;;
  *)
    echo "usage: $0 [start|stop|status]" >&2
    exit 2
    ;;
esac

# --- build if needed ---------------------------------------------------------
if [[ ! -x ./goboxd ]] || [[ ./goboxd -ot ./cmd/goboxd ]]; then
  echo "building ./goboxd..."
  go build -o goboxd ./cmd/goboxd
fi

# --- start in background -----------------------------------------------------
if [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
  echo "goboxd already running (pid $(cat "$PID_FILE")) on http://localhost:$PORT"
  exit 0
fi

echo "starting goboxd in the background (log: $LOG_FILE)..."
nohup ./scripts/dev-host.sh > "$LOG_FILE" 2>&1 &
echo $! > "$PID_FILE"

# --- wait for readiness ------------------------------------------------------
for i in $(seq 1 30); do
  if curl -s -o /dev/null "http://localhost:$PORT/healthz"; then
    echo "goboxd is up at http://localhost:$PORT"
    echo "  logs:   tail -f $LOG_FILE"
    echo "  stop:   ./scripts/serve.sh stop"
    exit 0
  fi
  sleep 1
done

echo "goboxd did not become healthy in 30s; see $LOG_FILE" >&2
exit 1
