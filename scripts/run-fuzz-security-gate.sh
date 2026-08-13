#!/usr/bin/env bash
set -euo pipefail

fuzz_time="${SCRIPTBOARD_FUZZ_TIME:-30s}"

go test ./internal/outboundpolicy \
  -run='^$' -fuzz=FuzzPolicyAllowsAddress -fuzztime="$fuzz_time" -parallel=4
go test ./internal/web \
  -run='^$' -fuzz=FuzzNormalizeHTTPHost -fuzztime="$fuzz_time" -parallel=4
go test ./internal/runmanager \
  -run='^$' -fuzz=FuzzParseArgumentsRejectsControlCharacters -fuzztime="$fuzz_time" -parallel=4
