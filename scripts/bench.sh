#!/usr/bin/env bash
# Reproducible performance benchmarks for Interceptor.
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
bin=$(mktemp -t interceptor-bench)
CGO_ENABLED=0 go build -o "$bin" ./cmd/interceptor
home=$(mktemp -d)
trap 'kill "${pid:-}" 2>/dev/null || true; rm -rf "$home" "$bin"' EXIT

start=$(python3 -c 'import time; print(time.time())')
HOME="$home" INTERCEPTOR_NO_BROWSER=1 "$bin" >/dev/null 2>&1 &
pid=$!
until curl -fsS http://127.0.0.1:9966/ -o /dev/null 2>/dev/null; do
  kill -0 "$pid" 2>/dev/null || { echo "server exited early"; exit 1; }
done
end=$(python3 -c 'import time; print(time.time())')
python3 -c "print('cold start to serving UI: %.0f ms' % (($end - $start) * 1000))"

rss=$(ps -o rss= -p "$pid" | tr -d ' ')
python3 -c "print('idle RSS: %.1f MB' % ($rss / 1024))"
