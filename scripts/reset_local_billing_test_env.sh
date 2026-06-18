#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_HOME="${WALNUT_BILLING_TEST_HOME:-$ROOT_DIR/.tmp/local-commerce}"
rm -rf "$TEST_HOME"
printf 'Removed Walnut billing local test env: %s\n' "$TEST_HOME"
