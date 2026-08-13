#!/usr/bin/env bash
set -euo pipefail

fuzz_time="${SCRIPTBOARD_FUZZ_TIME:-30s}"

go test ./internal/outboundpolicy \
  -run='^$' -fuzz=FuzzPolicyAllowsAddress -fuzztime="$fuzz_time"
go test ./internal/app \
  -run='^$' -fuzz=FuzzNormalizeHTTPHost -fuzztime="$fuzz_time"
go test ./internal/runmanager \
  -run='^$' -fuzz=FuzzParseArgumentsRejectsControlCharacters -fuzztime="$fuzz_time"
