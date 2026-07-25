#!/usr/bin/env bash
# One-shot Fly.io deploy for Conductor.
# Prerequisites: flyctl installed, and either `fly auth login` or FLY_API_TOKEN.
#
# Usage:
#   export CONDUCTOR_API_KEY=your-secret-gateway-key   # optional; auto-generated on first deploy if unset
#   export OPENAI_API_KEY=sk-...          # or another implemented provider key
#   ./scripts/fly-deploy.sh
#
# If CONDUCTOR_API_KEY is unset and the Fly app has no CONDUCTOR_API_KEY or legacy
# NOVEXA_API_KEY secret yet, a random key is generated, printed once, and set as a
# Fly secret. Redeploys reuse an existing Fly secret instead of rotating it.

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

APP_NAME="${APP_NAME:-conductor-yknfkg}"
REGION="${REGION:-sin}"
VOLUME_NAME="${VOLUME_NAME:-conductor_data}"

if ! "$FLY_BIN" auth whoami >/dev/null 2>&1; then
  echo "Not logged in to Fly.io."
  echo "Run: fly auth login"
  echo "Or set FLY_API_TOKEN (https://fly.io/user/personal_access_tokens)"
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

# Only set CONDUCTOR_API_KEY when the operator provided one, or on first deploy when
# the app has no gateway secret yet (including legacy NOVEXA_API_KEY). Never rotate
# an existing Fly secret silently.
SET_CONDUCTOR_API_KEY=0
has_gateway_secret() {
  local secrets_json secrets_text
  secrets_json="$("$FLY_BIN" secrets list -a "$APP_NAME" --json 2>/dev/null || true)"
  secrets_text="$("$FLY_BIN" secrets list -a "$APP_NAME" 2>/dev/null || true)"
  echo "$secrets_json" | grep -q '"name"[[:space:]]*:[[:space:]]*"CONDUCTOR_API_KEY"' \
    || echo "$secrets_text" | grep -qw "CONDUCTOR_API_KEY" \
    || echo "$secrets_json" | grep -q '"name"[[:space:]]*:[[:space:]]*"NOVEXA_API_KEY"' \
    || echo "$secrets_text" | grep -qw "NOVEXA_API_KEY"
}
if [[ -n "${CONDUCTOR_API_KEY:-}" ]]; then
  SET_CONDUCTOR_API_KEY=1
elif has_gateway_secret; then
  echo "Reusing existing gateway API key Fly secret (CONDUCTOR_API_KEY or NOVEXA_API_KEY; set CONDUCTOR_API_KEY to rotate)."
else
  if command -v openssl >/dev/null 2>&1; then
    CONDUCTOR_API_KEY="$(openssl rand -hex 32)"
  else
    CONDUCTOR_API_KEY="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  fi
  export CONDUCTOR_API_KEY
  SET_CONDUCTOR_API_KEY=1
  echo "Generated CONDUCTOR_API_KEY (will be set as a Fly secret)."
  echo "Save this key: ${CONDUCTOR_API_KEY}"
fi

SECRET_ARGS=()
[[ "$SET_CONDUCTOR_API_KEY" == "1" ]] && SECRET_ARGS+=(CONDUCTOR_API_KEY="$CONDUCTOR_API_KEY")
[[ -n "${OPENAI_API_KEY:-}" ]] && SECRET_ARGS+=(OPENAI_API_KEY="$OPENAI_API_KEY")
[[ -n "${OPENCODE_API_KEY:-}" ]] && SECRET_ARGS+=(OPENCODE_API_KEY="$OPENCODE_API_KEY")
[[ -n "${DEEPSEEK_API_KEY:-}" ]] && SECRET_ARGS+=(DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY")
[[ -n "${NVIDIA_NIM_API_KEY:-}" ]] && SECRET_ARGS+=(NVIDIA_NIM_API_KEY="$NVIDIA_NIM_API_KEY")
[[ -n "${NOUS_PORTAL_API_KEY:-}" ]] && SECRET_ARGS+=(NOUS_PORTAL_API_KEY="$NOUS_PORTAL_API_KEY")

if [[ ${#SECRET_ARGS[@]} -gt 0 ]]; then
  echo "Setting secrets..."
  "$FLY_BIN" secrets set -a "$APP_NAME" "${SECRET_ARGS[@]}"
else
  echo "No new secrets to set."
fi

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
