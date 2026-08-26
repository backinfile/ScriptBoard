#!/usr/bin/env bash
set -euo pipefail

# Keep this list aligned with current security-boundary packages so retired modules do not block releases.
go test -race \
  ./internal/web \
  ./internal/auditlog \
  ./internal/auditnotification \
  ./internal/customdashboard \
  ./internal/externaltrigger \
  ./internal/hostfiles \
  ./internal/mysqlmanager \
  ./internal/outboundpolicy \
  ./internal/privilegebroker \
  ./internal/processlaunch \
  ./internal/runmanager \
  ./internal/runnerhost \
  ./internal/securityevents \
  ./internal/statebackup \
  ./internal/update \
  -count=1
