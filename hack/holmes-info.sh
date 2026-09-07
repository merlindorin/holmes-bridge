#!/usr/bin/env bash
#
# Read HolmesGPT's /api/info through a temporary port-forward.
#
# This is the quickest check that a Holmes install is actually usable: it shows
# which models loaded (so you know your OpenRouter key worked) and how many
# toolsets came up (so you know it can inspect the cluster rather than just
# talk about it).

set -euo pipefail

cd "$(dirname "$0")/.."
# shellcheck source=hack/lib.sh
source hack/lib.sh
load_dotenv .env
load_dotenv .env.default

NAMESPACE="${HOLMES_NAMESPACE:-holmes}"
RELEASE="${HOLMES_RELEASE:-holmes}"
PORT="${TEST_HOLMES_INFO_PORT:-15051}"

command -v kubectl >/dev/null 2>&1 || { echo "kubectl is not installed" >&2; exit 1; }

kubectl port-forward -n "$NAMESPACE" "svc/${RELEASE}-holmes" "${PORT}:80" >/dev/null 2>&1 &
forward=$!
trap 'kill $forward 2>/dev/null || true' EXIT

for _ in $(seq 1 40); do
  curl -sf --max-time 2 "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 && break
  sleep 0.5
done

if ! curl -sf --max-time 3 "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
  echo "could not reach Holmes in namespace '${NAMESPACE}'." >&2
  echo "Is it installed?  task holmes:install" >&2
  exit 1
fi

if command -v jq >/dev/null 2>&1; then
  # "disabled" is the normal state for the ~20 integrations this cluster does
  # not have; listing them buries the two lines that matter. Only genuinely
  # failed toolsets — ones that were asked for and could not start — are shown.
  curl -s --max-time 15 "http://127.0.0.1:${PORT}/api/info?detail=full" \
    | jq '{
        version,
        models,
        toolsets_summary,
        enabled: [(.toolsets // [])[] | select(.status == "enabled") | .name],
        failed: [(.toolsets // [])[]
                 | select(.status != "enabled" and .status != "disabled")
                 | {name, error}]
      }'
else
  curl -s --max-time 15 "http://127.0.0.1:${PORT}/api/info"
fi
