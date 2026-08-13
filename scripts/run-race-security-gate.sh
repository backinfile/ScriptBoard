#!/usr/bin/env bash
set -euo pipefail

go test -race \
  ./internal/web \
  ./internal/assistant/pirpc \
  ./internal/assistant/providerproxy \
  ./internal/assistant/runtimehost \
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
  ./internal/uploadinbox \
  -count=1
