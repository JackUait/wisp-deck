#!/usr/bin/env bash
set -euo pipefail

export WISP_DECK_TESTING=1
exec -a '__WISP_DECK_REPOSITORY_TEST_V1__.test' go test "$@"
