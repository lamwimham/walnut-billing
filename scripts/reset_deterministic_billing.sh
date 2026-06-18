#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="${WALNUT_BILLING_CONFIG_FILE:-$ROOT_DIR/config/local.deterministic.env}"

if [[ -f "$CONFIG_FILE" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$CONFIG_FILE"
  set +a
fi

TEST_HOME="${WALNUT_BILLING_TEST_HOME:-.tmp/deterministic-billing}"
if [[ "$TEST_HOME" != /* ]]; then
  TEST_HOME="$ROOT_DIR/$TEST_HOME"
fi

rm -rf "$TEST_HOME"
printf 'Removed Walnut billing deterministic home: %s\n' "$TEST_HOME"
