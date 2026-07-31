package app

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type expiredAuditCandidate struct {
	auditID        int64
	runID          string
	sourceFilename string
	sourceExpired  bool
}

func cleanupExpiredAuditEvents(db *sql.DB, stateRoot string, cutoff time.Time) (int, error) {
	rows, err := db.Query(`SELECT audit_events.id, COALESCE(runs.id, ''), COALESCE(runs.source_filename, ''), COALESCE(runs.source_expired, 0)
		FROM audit_events
		LEFT JOIN runs ON runs.source_audit_event_id = audit_events.id AND runs.script_kind = 'one_time'
		WHERE audit_events.occurred_at < ?
		ORDER BY audit_events.id`, cutoff.UTC().Unix())
	if err != nil {
		return 0, err
	}
	var candidates []expiredAuditCandidate
	for rows.Next() {
		var candidate expiredAuditCandidate
		if err := rows.Scan(&candidate.auditID, &candidate.runID, &candidate.sourceFilename, &candidate.sourceExpired); err != nil {
			_ = rows.Close()
			return 0, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	deleted := 0
	var failures []string
	for _, candidate := range candidates {
		if candidate.runID != "" && !candidate.sourceExpired {
			if candidate.runID != filepath.Base(candidate.runID) ||
				candidate.sourceFilename == "" ||
				candidate.sourceFilename != filepath.Base(candidate.sourceFilename) ||
				strings.ContainsAny(candidate.runID+candidate.sourceFilename, `/\`) {
				failures = append(failures, fmt.Sprintf("audit %d has unsafe source metadata", candidate.auditID))
				continue
			}
			sourcePath := filepath.Join(stateRoot, "runs", candidate.runID, candidate.sourceFilename)
			if err := os.Remove(sourcePath); err != nil && !os.IsNotExist(err) {
				failures = append(failures, fmt.Sprintf("audit %d source removal: %v", candidate.auditID, err))
				continue
			}
		}
		transaction, err := db.Begin()
		if err != nil {
			failures = append(failures, fmt.Sprintf("audit %d transaction: %v", candidate.auditID, err))
			continue
		}
		if candidate.runID != "" {
			_, err = transaction.Exec("UPDATE runs SET source_expired = 1, source_audit_event_id = NULL WHERE id = ? AND source_audit_event_id = ?", candidate.runID, candidate.auditID)
		}
		if err == nil {
			_, err = transaction.Exec("DELETE FROM audit_events WHERE id = ?", candidate.auditID)
		}
		if err == nil {
			err = transaction.Commit()
		} else {
			_ = transaction.Rollback()
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("audit %d database cleanup: %v", candidate.auditID, err))
			continue
		}
		deleted++
	}
	if len(failures) > 0 {
		return deleted, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return deleted, nil
}
