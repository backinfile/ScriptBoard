package mysqlmanager

import "testing"

func TestReplacementDatabaseStatementRejectsUnsafeServerMetadata(t *testing.T) {
	for _, test := range []struct {
		charset, collation string
	}{
		{"utf8mb4; DROP DATABASE mysql", ""},
		{"utf8mb4", "utf8mb4_0900_ai_ci /*"},
		{"", "utf8mb4_0900_ai_ci"},
	} {
		if _, err := replacementDatabaseStatement("application", test.charset, test.collation); err == nil {
			t.Fatalf("unsafe metadata accepted: charset=%q collation=%q", test.charset, test.collation)
		}
	}
	statement, err := replacementDatabaseStatement("application", "utf8mb4", "utf8mb4_0900_ai_ci")
	if err != nil || statement != "CREATE DATABASE `application` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci" {
		t.Fatalf("statement=%q err=%v", statement, err)
	}
}
