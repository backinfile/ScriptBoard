package mysqlmanager

import (
	"testing"
	"time"
)

func TestClassifySQLReadOnlyAllowsSupportedStatementsAndCTE(t *testing.T) {
	t.Parallel()
	for _, statement := range []string{
		"select * from users",
		"/* leading */ ShOw tables",
		"-- explanation\nDESC users",
		"WITH active AS (SELECT id FROM users WHERE enabled=1) SELECT * FROM active",
		"EXPLAIN UPDATE users SET enabled=0",
	} {
		classification, err := classifySQL(statement)
		if err != nil {
			t.Fatalf("classify %q: %v", statement, err)
		}
		if !classification.readOnly || !classification.query {
			t.Fatalf("expected read-only query for %q: %#v", statement, classification)
		}
	}
}

func TestClassifySQLBlocksBypassesAndDangerousWrites(t *testing.T) {
	t.Parallel()
	blocked := []string{
		"SELECT 1; DELETE FROM users",
		"CALL rebuild_cache()",
		"WITH changed AS (DELETE FROM users) SELECT * FROM changed",
		"SELECT * FROM users FOR UPDATE",
		"SELECT secret INTO OUTFILE '/tmp/leak' FROM users",
		"/* unterminated",
	}
	for _, statement := range blocked {
		if _, err := classifySQL(statement); err == nil {
			t.Fatalf("expected %q to be rejected", statement)
		}
	}
	classification, err := classifySQL("WITH active AS (SELECT id FROM users) DELETE FROM users")
	if err != nil || classification.kind != "DELETE" || classification.readOnly {
		t.Fatalf("write CTE terminal statement must remain classified as write: %#v, %v", classification, err)
	}
	for _, statement := range []string{"UPDATE users SET enabled=0", "delete from users", "DROP TABLE users", "ALTER TABLE users ADD x INT", "TRUNCATE users"} {
		classification, err := classifySQL(statement)
		if err != nil {
			t.Fatalf("classify %q: %v", statement, err)
		}
		if !classification.dangerous {
			t.Fatalf("expected dangerous classification for %q", statement)
		}
	}
	classification, err = classifySQL("UPDATE users SET enabled=0 WHERE id=1")
	if err != nil || classification.dangerous {
		t.Fatalf("WHERE-limited update should not require dangerous override: %#v, %v", classification, err)
	}
}

func TestClassifySQLDoesNotTreatQuotedSemicolonAsMultipleStatements(t *testing.T) {
	t.Parallel()
	classification, err := classifySQL("SELECT '; not another statement' AS value;")
	if err != nil || classification.kind != "SELECT" {
		t.Fatalf("quoted semicolon should be accepted: %#v, %v", classification, err)
	}
}

func TestBoundedSQLLimits(t *testing.T) {
	t.Parallel()
	timeout, rows := boundedSQLLimits(10*time.Minute, 50000)
	if timeout != maximumSQLTimeout || rows != maximumSQLRows {
		t.Fatalf("limits were not capped: %s, %d", timeout, rows)
	}
	timeout, rows = boundedSQLLimits(0, 0)
	if timeout != defaultSQLTimeout || rows != defaultSQLRows {
		t.Fatalf("defaults were not applied: %s, %d", timeout, rows)
	}
}
