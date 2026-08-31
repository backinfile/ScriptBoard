#!/usr/bin/env bash
set -euo pipefail

# Fix: duration-based fuzzing can report context deadline exceeded when a worker
# returns just after the timer. A fixed mutation budget keeps CI deterministic.
fuzz_time="${SCRIPTBOARD_FUZZ_TIME:-200000x}"

go test ./internal/outboundpolicy \
  -run='^$' -fuzz=FuzzPolicyAllowsAddress -fuzztime="$fuzz_time" -parallel=4
go test ./internal/web \
  -run='^$' -fuzz=FuzzNormalizeHTTPHost -fuzztime="$fuzz_time" -parallel=4
go test ./internal/web \
  -run='^$' -fuzz=FuzzValidRequestTarget -fuzztime="$fuzz_time" -parallel=4
go test ./internal/update \
  -run='^$' -fuzz=FuzzSafeArchivePath -fuzztime="$fuzz_time" -parallel=4
go test ./internal/runmanager \
  -run='^$' -fuzz=FuzzParseArgumentsRejectsControlCharacters -fuzztime="$fuzz_time" -parallel=4
