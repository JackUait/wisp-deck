#!/bin/bash
set -e
export WISP_DECK_TESTING=1

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"
exec "$SCRIPT_DIR/scripts/go-test.sh" ./cmd/wisp-deck-tui/... ./test/bash/... ./test/internal/... ./test/npx/... ./internal/... "$@"
