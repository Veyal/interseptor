#!/usr/bin/env bash
# Headless-Chrome UI boot smoke: builds the binary, starts a throwaway instance,
# launches headless Chrome and runs scripts/ui_boot_smoke.mjs against it.
#
#   make ui-smoke
#   CHROME=/path/to/chrome CONTROL_PORT=19966 PROXY_PORT=18080 CDP_PORT=9333 scripts/ui_smoke.sh
#
# Needs Node >= 22 and a Chrome/Chromium binary. Never touches real data: the
# instance uses a temp --data-dir and every process is stopped on exit.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
control_port="${CONTROL_PORT:-19966}"
proxy_port="${PROXY_PORT:-18080}"
cdp_port="${CDP_PORT:-9333}"

chrome="${CHROME:-}"
if [[ -z "$chrome" ]]; then
  for candidate in google-chrome google-chrome-stable chromium chromium-browser \
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; do
    if command -v "$candidate" >/dev/null 2>&1 || [[ -x "$candidate" ]]; then
      chrome="$candidate"
      break
    fi
  done
fi
[[ -n "$chrome" ]] || { echo "ui-smoke: no Chrome found; set CHROME=/path/to/chrome" >&2; exit 2; }

work="$(mktemp -d)"
pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do
    [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

(cd "$root" && CGO_ENABLED=0 go build -o "$work/interseptor" ./cmd/interseptor)

INTERSEPTOR_NO_UPDATE_CHECK=1 INTERSEPTOR_NO_BROWSER=1 "$work/interseptor" \
  --data-dir "$work/data" --project smoke \
  --control-port "$control_port" --proxy-port "$proxy_port" >"$work/interseptor.log" 2>&1 &
pids+=($!)

"$chrome" --headless=new --no-sandbox --disable-gpu --no-first-run \
  --remote-debugging-port="$cdp_port" --user-data-dir="$work/chrome" about:blank \
  >"$work/chrome.log" 2>&1 &
pids+=($!)

ready() { curl -fsS -o /dev/null "$1" 2>/dev/null; }
for _ in $(seq 1 60); do
  if ready "http://127.0.0.1:$control_port/" && ready "http://127.0.0.1:$cdp_port/json/version"; then
    break
  fi
  sleep 0.5
done
if ! ready "http://127.0.0.1:$control_port/" || ! ready "http://127.0.0.1:$cdp_port/json/version"; then
  echo "ui-smoke: interseptor or Chrome did not start" >&2
  tail -n 20 "$work/interseptor.log" "$work/chrome.log" >&2 || true
  exit 2
fi

node "$root/scripts/ui_boot_smoke.mjs" "http://127.0.0.1:$control_port/" --cdp "http://127.0.0.1:$cdp_port"
