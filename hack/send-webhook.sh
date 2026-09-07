#!/usr/bin/env bash
#
# Send a correctly-signed incident.io webhook to a bridge.
#
# incident.io signs with the Standard Webhooks scheme, which means an HMAC over
# "<id>.<timestamp>.<body>" keyed on the base64-decoded secret. Getting that
# right by hand is fiddly, and a wrong signature is indistinguishable from a
# bridge that is down — both just say 401.
#
#   ./hack/send-webhook.sh                                   # local bridge
#   ./hack/send-webhook.sh https://my-peer.example.com       # through a tunnel
#   INCIDENT_ID=01ABC ./hack/send-webhook.sh                 # a specific incident
#
# Set WEBHOOK_SECRET to whatever the bridge was started with.

set -euo pipefail

cd "$(dirname "$0")/.."

# shellcheck source=hack/lib.sh
source hack/lib.sh
load_dotenv .env
load_dotenv .env.default

BRIDGE_URL="${1:-http://127.0.0.1:${BRIDGE_PORT:-18081}}"
MOCK_URL="${MOCK_URL:-http://127.0.0.1:${MOCK_PORT:-18080}}"
SECRET="${WEBHOOK_SECRET:-whsec_VEVTVC1NT0NLLU5PVC1BLVJFQUwtU0VDUkVUIQ==}"
EVENT="${EVENT:-public_incident.incident_created_v2}"

# Default to whatever the mock is serving, so the bridge can actually fetch the
# incident it is told about. Override for a real org.
INCIDENT_ID="${INCIDENT_ID:-}"
if [[ -z "$INCIDENT_ID" ]]; then
  INCIDENT_ID="$(curl -sf --max-time 10 "${MOCK_URL}/v2/incidents" 2>/dev/null \
    | python3 -c 'import json,sys; print((json.load(sys.stdin).get("incidents") or [{}])[0].get("id",""))' 2>/dev/null || true)"
fi

if [[ -z "$INCIDENT_ID" ]]; then
  echo "error: no incident to reference." >&2
  echo "Start the mock, or pass one: INCIDENT_ID=01ABC $0 $BRIDGE_URL" >&2
  exit 1
fi

echo "POST ${BRIDGE_URL%/}/webhooks/incidentio"
echo "  event:    $EVENT"
echo "  incident: $INCIDENT_ID"

python3 - "$BRIDGE_URL" "$SECRET" "$EVENT" "$INCIDENT_ID" <<'PY'
import base64, hashlib, hmac, json, subprocess, sys, time

url, secret, event, incident = sys.argv[1:5]

key = base64.b64decode(secret.removeprefix("whsec_"))
body = json.dumps({"event_type": event, event: {"id": incident}}, separators=(",", ":"))
msg_id, ts = f"msg_test_{int(time.time())}", str(int(time.time()))
sig = "v1," + base64.b64encode(
    hmac.new(key, f"{msg_id}.{ts}.{body}".encode(), hashlib.sha256).digest()).decode()

out = subprocess.run([
    "curl", "-s", "-w", "\n%{http_code}", "--max-time", "60", "-X", "POST",
    url.rstrip("/") + "/webhooks/incidentio",
    "-H", "Content-Type: application/json",
    "-H", f"webhook-id: {msg_id}",
    "-H", f"webhook-timestamp: {ts}",
    "-H", f"webhook-signature: {sig}",
    "-d", body,
], capture_output=True, text=True, check=False)

*payload, status = out.stdout.split("\n")
payload = "\n".join(payload).strip()

print(f"  -> HTTP {status}")
if payload:
    print("  " + payload)

if status == "401":
    print("\n401 means the signature did not verify: the bridge was started with a "
          "different --webhook-secret than the one used here.", file=sys.stderr)

sys.exit(0 if status.startswith("2") else 1)
PY
