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
TEST_HOME="${WALNUT_BILLING_TEST_HOME:-$ROOT_DIR/.tmp/local-commerce}"
PORT="${SERVER_PORT:-8082}"
mkdir -p "$TEST_HOME/data"

export SERVER_PORT="$PORT"
export SERVER_ENV="${SERVER_ENV:-dev}"
export DATABASE_DRIVER="${DATABASE_DRIVER:-sqlite}"
export DATABASE_DSN="${DATABASE_DSN:-$TEST_HOME/data/walnut_billing_test.db}"
export ADMIN_API_KEYS="${ADMIN_API_KEYS:-local-admin-key}"
export PAYMENT_MOCK_CHECKOUT_BASE_URL="${PAYMENT_MOCK_CHECKOUT_BASE_URL:-http://localhost:$PORT}"
export ACCESS_SNAPSHOT_SECRET="${ACCESS_SNAPSHOT_SECRET:-walnut-dev-access-snapshot-secret}"
export ACCESS_SNAPSHOT_SIGNATURE_ALGORITHM="${ACCESS_SNAPSHOT_SIGNATURE_ALGORITHM:-HS256}"
export ACCESS_SNAPSHOT_KEY_ID="${ACCESS_SNAPSHOT_KEY_ID:-dev}"
export ACCESS_TRIAL_DURATION_DAYS="${ACCESS_TRIAL_DURATION_DAYS:-14}"
export ACCESS_MAX_DEVICES="${ACCESS_MAX_DEVICES:-2}"

printf 'Walnut billing local test env\n'
printf '  home: %s\n' "$TEST_HOME"
printf '  db:   %s\n' "$DATABASE_DSN"
printf '  url:  http://127.0.0.1:%s\n' "$PORT"
printf '  admin key: %s\n' "$ADMIN_API_KEYS"

verify_go_toolchain

cd "$ROOT_DIR"
exec env -u GOROOT -u GOTOOLDIR go run ./cmd/server
