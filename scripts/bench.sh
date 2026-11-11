#!/usr/bin/env bash
# Reproducible performance benchmarks for Interseptor.
# Records the numbers documented in docs/benchmarks.md:
#   - Go microbenchmarks (capture streaming, flow insert rate)
#   - cold start to a serving UI
#   - idle resident memory (RSS)
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== Go benchmarks =="
go test ./internal/capture/ ./internal/store/ -bench . -benchmem -run '^$'

echo
echo "== cold start + idle RSS =="
# Private ports by default so a running Interseptor on 8080/9966 neither blocks
# the benchmark instance nor answers the readiness probe in its place.
control_port="${CONTROL_PORT:-19976}"
proxy_port="${PROXY_PORT:-18086}"
start_timeout="${START_TIMEOUT:-30}" # seconds

bin=$(mktemp "${TMPDIR:-/tmp}/interseptor-bench.XXXXXX")
home=$(mktemp -d "${TMPDIR:-/tmp}/interseptor-bench-home.XXXXXX")
pid=""
cleanup() {
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$home" "$bin"
}
trap cleanup EXIT

CGO_ENABLED=0 go build -o "$bin" ./cmd/interseptor

now() { python3 -c 'import time; print(time.time())'; }

start=$(now)
HOME="$home" INTERSEPTOR_NO_BROWSER=1 INTERSEPTOR_NO_UPDATE_CHECK=1 "$bin" \
  --data-dir "$home/data" --control-port "$control_port" --proxy-port "$proxy_port" \
  >"$home/server.log" 2>&1 &
pid=$!

# Poll with a short sleep, a per-probe timeout and an overall deadline: the old
# loop spun on curl with neither, so it burned a core and never ended when the
# port was answered by something that is not this server.
deadline=$((SECONDS + start_timeout))
until curl -fsS --max-time 2 -o /dev/null "http://127.0.0.1:$control_port/" 2>/dev/null; do
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "server exited early:" >&2
    tail -n 20 "$home/server.log" >&2 || true
    exit 1
  fi
  if ((SECONDS >= deadline)); then
    echo "server did not answer on 127.0.0.1:$control_port within ${start_timeout}s:" >&2
    tail -n 20 "$home/server.log" >&2 || true
    exit 1
  fi
  sleep 0.02
done
end=$(now)
kill -0 "$pid" 2>/dev/null || { echo "server exited before it was measured" >&2; exit 1; }
python3 -c "print('cold start to serving UI: %.0f ms' % (($end - $start) * 1000))"

rss=$(ps -o rss= -p "$pid" | tr -d ' ')
[[ -n "$rss" ]] || { echo "could not read the server's RSS" >&2; exit 1; }
python3 -c "print('idle RSS: %.1f MB' % ($rss / 1024))"
