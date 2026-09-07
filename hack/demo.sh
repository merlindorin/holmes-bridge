#!/usr/bin/env bash
#
# Run the whole loop against a real HolmesGPT.
#
# Expects Holmes to already be installed in your cluster:
#
#   kind create cluster --name holmes
#   export OPENROUTER_API_KEY=sk-or-...
#   task holmes:install
#
# Then this brings up the incident.io mock and the bridge, port-forwards to
# Holmes, applies a genuinely broken workload for it to investigate, and prints
# what to curl.
#
# Ctrl-C stops everything and leaves the cluster alone.

set -euo pipefail

cd "$(dirname "$0")/.."

# shellcheck source=hack/lib.sh
source hack/lib.sh

# .env first, so it wins over the committed defaults; a variable already
# exported in the shell beats both.
load_dotenv .env
load_dotenv .env.default

TEST_MOCK_PORT="${TEST_MOCK_PORT:-18080}"
TEST_BRIDGE_PORT="${TEST_BRIDGE_PORT:-18081}"
TEST_HOLMES_PORT="${TEST_HOLMES_PORT:-15050}"

HOLMES_NAMESPACE="${HOLMES_NAMESPACE:-holmes}"
HOLMES_RELEASE="${HOLMES_RELEASE:-holmes}"

# Which entry of modelList in hack/holmes-values.yaml to use. Empty asks Holmes
# for its own default.
HOLMES_MODEL="${HOLMES_MODEL:-claude-sonnet}"

TEST_SCENARIO="${TEST_SCENARIO:-checkout-crashloop}"
TEST_APPLY_WORKLOAD="${TEST_APPLY_WORKLOAD:-1}"

# A fixed development secret. Everything here is on loopback; a real deployment
# takes its secret from incident.io's webhook settings.
SECRET="${WEBHOOK_SECRET:-whsec_VEVTVC1NT0NLLU5PVC1BLVJFQUwtU0VDUkVUIQ==}"

# An investigation against a real model takes a while and costs money, so give
# it room and only ever run one at a time here.
INVESTIGATION_TIMEOUT="${INVESTIGATION_TIMEOUT:-10m}"

die() { echo "error: $*" >&2; exit 1; }

pids=()
cleanup() {
  echo
  echo "stopping..."
  for pid in "${pids[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  # The supervisor spawns kubectl as a child; killing the supervisor alone
  # leaves the tunnel behind.
  pkill -f "port-forward -n ${HOLMES_NAMESPACE} svc/${HOLMES_RELEASE}-holmes" 2>/dev/null || true
  wait 2>/dev/null || true
  echo "the cluster and its workloads were left running."
}
trap cleanup EXIT INT TERM

# --- Holmes ------------------------------------------------------------------
#
# Either use a Holmes that is already reachable (someone else's port-forward, or
# a HOLMES_URL pointing elsewhere), or forward to the one in the cluster.

HOLMES_URL="${HOLMES_URL:-http://127.0.0.1:${TEST_HOLMES_PORT}}"

if curl -sf --max-time 3 "${HOLMES_URL}/healthz" >/dev/null 2>&1; then
  echo "using the HolmesGPT already reachable at ${HOLMES_URL}"
else
  command -v kubectl >/dev/null 2>&1 || die "kubectl is not installed and nothing is serving ${HOLMES_URL}"

  # Distinguish "the deployment is not there" from "kubectl could not tell us".
  # Swallowing both into one message means a transient API-server blip reports
  # itself as a missing install, and sends you off to reinstall something that
  # was running the whole time.
  deployment_state() {
    local out
    if out="$(kubectl get deployment "${HOLMES_RELEASE}-holmes" -n "$HOLMES_NAMESPACE" 2>&1)"; then
      printf 'present'
    elif [[ "$out" == *NotFound* ]]; then
      printf 'absent'
    else
      printf 'unreachable\n%s' "$out"
    fi
  }

  state="$(deployment_state)"

  # One retry, because a single failed call is usually a blip rather than a fact.
  if [[ "$state" == unreachable* ]]; then
    sleep 2
    state="$(deployment_state)"
  fi

  case "$state" in
    present) ;;
    absent)
      die "HolmesGPT is not installed in namespace '${HOLMES_NAMESPACE}', and nothing is
serving ${HOLMES_URL}.

Install it:

    kind create cluster --name holmes
    # put OPENROUTER_API_KEY in .env, then:
    task holmes:install"
      ;;
    *)
      die "could not ask the cluster whether HolmesGPT is installed:

${state#unreachable$'\n'}

Check that your kubectl context ($(kubectl config current-context 2>/dev/null || echo 'none')) points at a running cluster."
      ;;
  esac

  echo "port-forwarding to ${HOLMES_RELEASE}-holmes in namespace ${HOLMES_NAMESPACE}"

  # Supervised, not fired once. `kubectl port-forward` gives up on an idle
  # connection, a pod restart, or any transport hiccup, and does not come back
  # on its own. An investigation runs for minutes against a paid model, so a
  # forward that quietly dies halfway through costs real money and reports
  # itself as "connection refused" — which looks like Holmes is down rather
  # than like the tunnel went away.
  forward_forever() {
    # `set -e` is inherited here, and a dropped port-forward exits non-zero —
    # which would kill this supervisor on the very first reconnect it exists to
    # handle. Turn it off for the loop.
    set +e

    while true; do
      kubectl port-forward -n "$HOLMES_NAMESPACE" \
        "svc/${HOLMES_RELEASE}-holmes" "${TEST_HOLMES_PORT}:80" >/dev/null 2>&1

      # Any exit means the tunnel is gone, clean or not. Only the trap stops us.
      echo "port-forward to Holmes dropped, reconnecting..." >&2
      sleep 1
    done
  }

  forward_forever &
  pids+=($!)

  echo -n "waiting for Holmes"
  for _ in $(seq 1 60); do
    curl -sf --max-time 2 "${HOLMES_URL}/healthz" >/dev/null 2>&1 && break
    echo -n "."
    sleep 0.5
  done
  echo
  curl -sf --max-time 3 "${HOLMES_URL}/healthz" >/dev/null 2>&1 ||
    die "Holmes did not become reachable at ${HOLMES_URL}"
fi

# Report what Holmes can actually do before spending a request on it.
if command -v jq >/dev/null 2>&1; then
  info="$(curl -sf --max-time 10 "${HOLMES_URL}/api/info" 2>/dev/null || echo '{}')"
  models="$(echo "$info" | jq -r '(.models // []) | join(", ")')"
  enabled="$(echo "$info" | jq -r '.toolsets_summary.enabled // 0')"
  failed="$(echo "$info" | jq -r '.toolsets_summary.failed // 0')"

  echo "HolmesGPT: models [${models:-unknown}], toolsets ${enabled} enabled / ${failed} failed"

  if [[ -n "$HOLMES_MODEL" && -n "$models" ]] && ! echo "$info" | jq -e --arg m "$HOLMES_MODEL" '.models // [] | index($m)' >/dev/null; then
    die "Holmes does not serve model '${HOLMES_MODEL}'. It has: ${models}

Set HOLMES_MODEL to one of those, or add it to modelList in hack/holmes-values.yaml
and re-run 'task holmes:install'."
  fi

  [[ "$enabled" == "0" ]] && echo "warning: Holmes has no working toolsets — it cannot inspect the cluster, so its analysis will be speculation" >&2
fi

# --- the workload Holmes will investigate ------------------------------------

if [[ "$TEST_APPLY_WORKLOAD" == "1" ]] && command -v kubectl >/dev/null 2>&1; then
  echo "applying the broken demo workload (namespace checkout-demo)"
  kubectl apply -f hack/demo-workload.yaml >/dev/null
  # No wait: the whole point is that it never becomes ready. Give it long enough
  # to crash at least once so there is something in the logs to find.
  sleep 5
fi

# --- the mock and the bridge -------------------------------------------------

echo "building..."
go build -o bin/incidentio-mock ./cmd/incidentio-mock
go build -o bin/holmes-bridge ./cmd/holmes-bridge

echo "starting incident.io mock on :${TEST_MOCK_PORT} (scenario: ${TEST_SCENARIO})"
./bin/incidentio-mock serve \
  --http-port "${TEST_MOCK_PORT}" \
  --scenario "${TEST_SCENARIO}" \
  --webhook-secret "${SECRET}" \
  --webhook "name=bridge,url=http://127.0.0.1:${TEST_BRIDGE_PORT}/webhooks/incidentio" &
pids+=($!)

echo "starting holmes-bridge on :${TEST_BRIDGE_PORT}"
./bin/holmes-bridge incidentio serve \
  --http-port "${TEST_BRIDGE_PORT}" \
  --incidentio-url "http://127.0.0.1:${TEST_MOCK_PORT}" \
  --holmes-url "${HOLMES_URL}" \
  ${HOLMES_MODEL:+--holmes-model "${HOLMES_MODEL}"} \
  --webhook-secret "${SECRET}" \
  --max-concurrent 1 \
  --investigation-timeout "${INVESTIGATION_TIMEOUT}" &
pids+=($!)

echo -n "waiting for the bridge and the mock"
for _ in $(seq 1 60); do
  if curl -sf "http://127.0.0.1:${TEST_MOCK_PORT}/liveness" >/dev/null 2>&1 &&
     curl -sf "http://127.0.0.1:${TEST_BRIDGE_PORT}/readiness" >/dev/null 2>&1; then
    break
  fi
  echo -n "."
  sleep 0.5
done
echo " ready"

cat <<EOF

  incident.io mock   http://127.0.0.1:${TEST_MOCK_PORT}
  holmes-bridge      http://127.0.0.1:${TEST_BRIDGE_PORT}
  HolmesGPT          ${HOLMES_URL}  (model: ${HOLMES_MODEL:-server default})

The mock is serving '${TEST_SCENARIO}', which describes the broken workload now
running in the checkout-demo namespace. TEST-201 is the live SEV1.

Try:

  # Investigate the incident that is already open. This calls a real model, so
  # it takes a minute or two and costs a few cents.
  INC=\$(curl -s http://127.0.0.1:${TEST_MOCK_PORT}/v2/incidents \\
    | jq -r '.incidents[] | select(.reference=="TEST-201") | .id')
  curl -s -X POST http://127.0.0.1:${TEST_BRIDGE_PORT}/investigations/\$INC | jq -r '.analysis'

  # Or declare a new incident and let the webhook drive it
  curl -s -X POST http://127.0.0.1:${TEST_MOCK_PORT}/v2/incidents \\
    -H 'Content-Type: application/json' \\
    -d '{"idempotency_key":"demo-1","name":"Checkout API will not start",
         "summary":"checkout-api pods are in CrashLoopBackOff in the checkout-demo namespace.",
         "visibility":"public","severity_id":"SEV1","incident_status_id":"triage"}' \\
    | jq -r '.incident.reference'

  # Read the analysis back off the incident
  curl -s "http://127.0.0.1:${TEST_MOCK_PORT}/v2/incident_updates?incident_id=\$INC" \\
    | jq -r '.incident_updates[].message'

  # What actually is broken, for comparison
  kubectl get pods,svc,endpoints -n checkout-demo

EOF

wait
