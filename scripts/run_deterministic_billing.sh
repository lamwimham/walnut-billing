#!/usr/bin/env bash
set -euo pipefail


verify_go_toolchain() {
  # Shell profiles sometimes pin GOROOT/GOTOOLDIR to an older Go install. That
  # makes `go run` mix a new go command with old compiler tools and fail with:
  # compile: version "go1.x" does not match go tool version "go1.y".
  unset GOROOT GOTOOLDIR

  if ! command -v go >/dev/null 2>&1; then
    echo "Go is not installed or not in PATH." >&2
    exit 1
  fi

  local go_bin go_version compile_version
  go_bin="$(command -v go)"
  go_version="$(env -u GOROOT -u GOTOOLDIR go env GOVERSION 2>/dev/null || true)"
  compile_version="$(env -u GOROOT -u GOTOOLDIR go tool compile -V 2>/dev/null | awk '{print $3}' || true)"
  if [[ -n "$go_version" && -n "$compile_version" && "$go_version" != "$compile_version" ]]; then
    cat >&2 <<EOF
Go toolchain mismatch detected.
  go binary:       $go_bin
  go env GOVERSION:$go_version
  compile version: $compile_version

Fix your shell Go environment, then retry:
  unset GOROOT GOTOOLDIR
  go clean -cache

If it still fails, reinstall one Go version and remove stale GOROOT/GOTOOLDIR
exports from ~/.zshrc, ~/.zprofile, or ~/.bash_profile.
EOF
    exit 1
  fi
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="${WALNUT_BILLING_CONFIG_FILE:-$ROOT_DIR/config/local.deterministic.env}"

# Preserve explicit one-off overrides while still loading the deterministic profile.
OVERRIDE_SERVER_PORT="${SERVER_PORT:-}"
OVERRIDE_TEST_HOME="${WALNUT_BILLING_TEST_HOME:-}"
OVERRIDE_DATABASE_DSN="${DATABASE_DSN:-}"
OVERRIDE_MOCK_BASE_URL="${PAYMENT_MOCK_CHECKOUT_BASE_URL:-}"

if [[ ! -f "$CONFIG_FILE" ]]; then
  echo "Missing config file: $CONFIG_FILE" >&2
  exit 1
fi

set -a
# shellcheck source=/dev/null
source "$CONFIG_FILE"
set +a

if [[ -n "$OVERRIDE_SERVER_PORT" ]]; then
  export SERVER_PORT="$OVERRIDE_SERVER_PORT"
fi
if [[ -n "$OVERRIDE_TEST_HOME" ]]; then
  export WALNUT_BILLING_TEST_HOME="$OVERRIDE_TEST_HOME"
fi

TEST_HOME="${WALNUT_BILLING_TEST_HOME:-.tmp/deterministic-billing}"
if [[ "$TEST_HOME" != /* ]]; then
  TEST_HOME="$ROOT_DIR/$TEST_HOME"
fi
mkdir -p "$TEST_HOME/data"

if [[ -n "$OVERRIDE_DATABASE_DSN" ]]; then
  export DATABASE_DSN="$OVERRIDE_DATABASE_DSN"
else
  export DATABASE_DSN="$TEST_HOME/data/walnut_billing_deterministic.db"
fi
mkdir -p "$(dirname "$DATABASE_DSN")"

if [[ -n "$OVERRIDE_MOCK_BASE_URL" ]]; then
  export PAYMENT_MOCK_CHECKOUT_BASE_URL="$OVERRIDE_MOCK_BASE_URL"
else
  export PAYMENT_MOCK_CHECKOUT_BASE_URL="http://127.0.0.1:${SERVER_PORT}"
fi

printf 'Walnut billing deterministic profile\n'
printf '  config:       %s\n' "$CONFIG_FILE"
printf '  env:          %s\n' "$SERVER_ENV"
printf '  url:          http://127.0.0.1:%s\n' "$SERVER_PORT"
printf '  dashboard:    http://127.0.0.1:%s/dashboard\n' "$SERVER_PORT"
printf '  db:           %s\n' "$DATABASE_DSN"
printf '  mock checkout:%s\n' "$PAYMENT_MOCK_CHECKOUT_BASE_URL"
printf '  admin keys:   local-admin-key / support-key / ops-key\n'
printf '\n'

verify_go_toolchain

cd "$ROOT_DIR"
exec env -u GOROOT -u GOTOOLDIR go run ./cmd/server
