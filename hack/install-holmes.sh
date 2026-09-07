#!/usr/bin/env bash
#
# Install HolmesGPT into the current kubectl context, backed by OpenRouter.
#
# Assumes you already have a cluster:
#
#   kind create cluster --name holmes
#
# and an OpenRouter key in .env (see .env.example):
#
#   OPENROUTER_API_KEY=sk-or-...
#
# or exported in your shell, which takes precedence.
#
# This does not create or delete clusters. It installs into whatever context
# kubectl is pointed at, and refuses to run against anything that does not look
# local unless you say ALLOW_REMOTE_CLUSTER=1.

set -euo pipefail

cd "$(dirname "$0")/.."

# shellcheck source=hack/lib.sh
source hack/lib.sh

# .env first, so it wins over the committed defaults; a variable already
# exported in the shell beats both.
load_dotenv .env
load_dotenv .env.default

NAMESPACE="${HOLMES_NAMESPACE:-holmes}"
RELEASE="${HOLMES_RELEASE:-holmes}"
VALUES="${HOLMES_VALUES:-hack/holmes-values.yaml}"
CHART_VERSION="${HOLMES_CHART_VERSION:-}"
REPO_NAME="robusta"
REPO_URL="https://robusta-charts.storage.googleapis.com"

die() { echo "error: $*" >&2; exit 1; }

for tool in kubectl helm; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is not installed"
done

[[ -f "$VALUES" ]] || die "values file not found: $VALUES"

if [[ -z "${OPENROUTER_API_KEY:-}" ]]; then
  die "OPENROUTER_API_KEY is not set.

Get a key from https://openrouter.ai/keys, then put it in .env:

    OPENROUTER_API_KEY=sk-or-...

(copy .env.example if you do not have a .env yet), or export it for one run:

    OPENROUTER_API_KEY=sk-or-... task holmes:install"
fi

[[ "$OPENROUTER_API_KEY" == sk-or-* ]] ||
  echo "warning: OPENROUTER_API_KEY does not start with 'sk-or-'; is that really an OpenRouter key?" >&2

context="$(kubectl config current-context 2>/dev/null)" ||
  die "kubectl has no current context. Create a cluster first: kind create cluster --name holmes"

# Holmes gets cluster-wide read access and talks to a paid API. Installing it
# into a production cluster by accident because kubectl was pointed there is a
# genuinely easy mistake, so make it a deliberate one.
if [[ "${ALLOW_REMOTE_CLUSTER:-0}" != "1" ]]; then
  case "$context" in
    kind-*|minikube|docker-desktop|orbstack|rancher-desktop|colima) ;;
    *)
      die "current kubectl context is '$context', which does not look like a local cluster.

Holmes is granted cluster-wide read access by this chart. If you really mean to
install into '$context', re-run with ALLOW_REMOTE_CLUSTER=1."
      ;;
  esac
fi

kubectl cluster-info >/dev/null 2>&1 ||
  die "cannot reach the cluster for context '$context'. Is it running?"

echo "installing HolmesGPT"
echo "  context:   $context"
echo "  namespace: $NAMESPACE"
echo "  release:   $RELEASE"
echo "  values:    $VALUES"
echo

helm repo add "$REPO_NAME" "$REPO_URL" >/dev/null 2>&1 || true
helm repo update "$REPO_NAME" >/dev/null

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

# Recreated every run so a rotated key actually takes effect. The pod picks it
# up on the rollout that follows.
kubectl create secret generic holmes-openrouter \
  --namespace "$NAMESPACE" \
  --from-literal=openrouter-api-key="$OPENROUTER_API_KEY" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
echo "secret/holmes-openrouter configured"

version_args=()
[[ -n "$CHART_VERSION" ]] && version_args=(--version "$CHART_VERSION")

helm upgrade --install "$RELEASE" "$REPO_NAME/holmes" \
  --namespace "$NAMESPACE" \
  --values "$VALUES" \
  "${version_args[@]}" \
  --wait --timeout 5m

echo
echo "waiting for Holmes to become ready..."
if ! kubectl rollout status "deployment/${RELEASE}-holmes" \
     --namespace "$NAMESPACE" --timeout 5m; then
  echo >&2
  echo "Holmes did not become ready. Recent logs:" >&2
  kubectl logs --namespace "$NAMESPACE" "deployment/${RELEASE}-holmes" --tail 40 >&2 || true
  exit 1
fi

cat <<EOF

HolmesGPT is running in namespace '$NAMESPACE'.

  # Reach it locally
  kubectl port-forward -n $NAMESPACE svc/${RELEASE}-holmes 15050:80

  # Check which models and toolsets came up
  curl -s http://localhost:15050/api/info | jq

  # Run the whole loop against it
  task demo

EOF
