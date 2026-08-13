package web

import (
	"database/sql"
	"time"

	auditdomain "scriptboard/internal/audit"
)

func cleanupExpiredAuditEvents(db *sql.DB, stateRoot string, cutoff time.Time) (int, error) {
	return auditdomain.CleanupExpiredEvents(db, stateRoot, cutoff)
}

func cleanupExpiredAuditEventsBefore(db *sql.DB, stateRoot string, cutoff time.Time, preserveFromAuditID int64) (int, error) {
	return auditdomain.CleanupExpiredEventsBefore(db, stateRoot, cutoff, preserveFromAuditID)
}
