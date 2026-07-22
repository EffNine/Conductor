#!/usr/bin/env bash
# One-shot Fly.io deploy for Conductor.
# Prerequisites: flyctl installed, and either `fly auth login` or FLY_API_TOKEN.
#
# Usage:
#   export CONDUCTOR_API_KEY=your-secret-gateway-key
#   export OPENAI_API_KEY=sk-...          # or another implemented provider key
#   ./scripts/fly-deploy.sh
#
# Optional:
#   APP_NAME=my-conductor REGION=iad ./scripts/fly-deploy.sh

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if ! command -v fly >/dev/null 2>&1 && ! command -v flyctl >/dev/null 2>&1; then
  echo "flyctl not found. Installing..."
  curl -L https://fly.io/install.sh | sh
  export FLYCTL_INSTALL="${FLYCTL_INSTALL:-$HOME/.fly}"
  export PATH="$FLYCTL_INSTALL/bin:$PATH"
fi

FLY_BIN="$(command -v fly || command -v flyctl)"

APP_NAME="${APP_NAME:-conductor}"
REGION="${REGION:-iad}"
VOLUME_NAME="${VOLUME_NAME:-conductor_data}"

if ! "$FLY_BIN" auth whoami >/dev/null 2>&1; then
  echo "Not logged in to Fly.io."
  echo "Run: fly auth login"
  echo "Or set FLY_API_TOKEN (https://fly.io/user/personal_access_tokens)"
  exit 1
fi

if [[ -z "${CONDUCTOR_API_KEY:-}" ]]; then
  echo "CONDUCTOR_API_KEY is required."
  exit 1
fi

if [[ "${SKIP_PROVIDER_CHECK:-0}" != "1" ]]; then
  if [[ -z "${OPENAI_API_KEY:-}${OPENCODE_API_KEY:-}${DEEPSEEK_API_KEY:-}${NVIDIA_NIM_API_KEY:-}${NOUS_PORTAL_API_KEY:-}" ]]; then
    echo "Set at least one provider key (OPENAI_API_KEY, OPENCODE_API_KEY, DEEPSEEK_API_KEY, NVIDIA_NIM_API_KEY, or NOUS_PORTAL_API_KEY)."
    echo "Or set SKIP_PROVIDER_CHECK=1 to deploy without one."
    exit 1
  fi
fi

echo "Using app=${APP_NAME} region=${REGION}"

if ! "$FLY_BIN" status -a "$APP_NAME" >/dev/null 2>&1; then
  echo "Creating app ${APP_NAME}..."
  if ! "$FLY_BIN" apps create "$APP_NAME" --org personal 2>/dev/null \
    && ! "$FLY_BIN" apps create "$APP_NAME"; then
    echo "Could not create app '${APP_NAME}' (name may be taken)."
    echo "Retry with: APP_NAME=conductor-\$USER ./scripts/fly-deploy.sh"
    exit 1
  fi
fi

if ! "$FLY_BIN" volumes list -a "$APP_NAME" --json 2>/dev/null | grep -q "\"name\":\"${VOLUME_NAME}\""; then
  # JSON may include spaces after colons depending on flyctl version
  if ! "$FLY_BIN" volumes list -a "$APP_NAME" 2>/dev/null | grep -qw "$VOLUME_NAME"; then
    echo "Creating volume ${VOLUME_NAME} in ${REGION}..."
    "$FLY_BIN" volumes create "$VOLUME_NAME" --size 1 --region "$REGION" -a "$APP_NAME" --yes
  fi
fi

SECRET_ARGS=(CONDUCTOR_API_KEY="$CONDUCTOR_API_KEY")
[[ -n "${OPENAI_API_KEY:-}" ]] && SECRET_ARGS+=(OPENAI_API_KEY="$OPENAI_API_KEY")
[[ -n "${OPENCODE_API_KEY:-}" ]] && SECRET_ARGS+=(OPENCODE_API_KEY="$OPENCODE_API_KEY")
[[ -n "${DEEPSEEK_API_KEY:-}" ]] && SECRET_ARGS+=(DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY")
[[ -n "${NVIDIA_NIM_API_KEY:-}" ]] && SECRET_ARGS+=(NVIDIA_NIM_API_KEY="$NVIDIA_NIM_API_KEY")
[[ -n "${NOUS_PORTAL_API_KEY:-}" ]] && SECRET_ARGS+=(NOUS_PORTAL_API_KEY="$NOUS_PORTAL_API_KEY")

echo "Setting secrets..."
"$FLY_BIN" secrets set -a "$APP_NAME" "${SECRET_ARGS[@]}"

echo "Deploying (remote builder)..."
"$FLY_BIN" deploy -a "$APP_NAME" --config fly.toml --remote-only

echo ""
echo "Done. Status:"
"$FLY_BIN" status -a "$APP_NAME"
HOSTNAME="$("$FLY_BIN" info -a "$APP_NAME" --json 2>/dev/null | grep -o '"Hostname": "[^"]*"' | head -1 | cut -d'"' -f4 || true)"
if [[ -z "$HOSTNAME" ]]; then
  HOSTNAME="${APP_NAME}.fly.dev"
fi
echo ""
echo "Health: https://${HOSTNAME}/health"
echo "API:    https://${HOSTNAME}/v1"
echo "Auth:   Authorization: Bearer \$CONDUCTOR_API_KEY"
