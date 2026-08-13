package secretredaction_test

import (
	"encoding/json"
	"strings"
	"testing"

	"scriptboard/internal/secretredaction"
)

func TestStringRedactsCommonSecretFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		secret string
	}{
		{name: "password assignment", input: "password=hunter2-value", secret: "hunter2-value"},
		{name: "json API key", input: `{"api_key":"sk-test12345678901234567890"}`, secret: "sk-test12345678901234567890"},
		{name: "bearer authorization", input: "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature", secret: "eyJhbGciOiJIUzI1NiJ9.payload.signature"},
		{name: "basic authorization", input: "proxy-authorization=Basic dXNlcjpwYXNzd29yZA==", secret: "dXNlcjpwYXNzd29yZA=="},
		{name: "query token", input: "https://example.test/hook?token=query-secret-value&mode=fast", secret: "query-secret-value"},
		{name: "URL password", input: "mysql://operator:database-password@db.example.test/app", secret: "database-password"},
		{name: "ScriptBoard key", input: "key sbk_0123456789abcdef.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", secret: "sbk_0123456789abcdef.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{name: "GitHub token", input: "github_pat_11AA0_thisisasynthetictokenvalue123456", secret: "github_pat_11AA0_thisisasynthetictokenvalue123456"},
		{name: "AWS access key", input: "AKIAIOSFODNN7EXAMPLE", secret: "AKIAIOSFODNN7EXAMPLE"},
		{name: "private key", input: "before\n-----BEGIN PRIVATE KEY-----\nsynthetic-private-material\n-----END PRIVATE KEY-----\nafter", secret: "synthetic-private-material"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := secretredaction.String(test.input)
			if strings.Contains(got, test.secret) {
				t.Fatalf("redacted output still contains secret: %q", got)
			}
			if !strings.Contains(got, secretredaction.Marker) {
				t.Fatalf("redacted output does not contain marker: %q", got)
			}
		})
	}
}

func TestMarshalJSONRedactsNestedAndEscapedSecretsWithoutBreakingJSON(t *testing.T) {
	t.Parallel()

	const secret = "nested-body-password"
	encoded, err := secretredaction.MarshalJSON(map[string]any{
		"Authorization": "Bearer synthetic-export-token-value",
		"body":          `{"password":"` + secret + `"}`,
		"private_key":   "-----BEGIN PRIVATE KEY-----\nmaterial\n-----END PRIVATE KEY-----",
		"headers": []map[string]string{
			{"name": "Cookie", "value": "session=short-value"},
		},
		"url": "https://example.test/?api_key=query-export-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(encoded) {
		t.Fatalf("redacted export is invalid JSON: %s", encoded)
	}
	for _, value := range []string{"synthetic-export-token-value", secret, "query-export-secret", "material", "short-value"} {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("redacted export retained %q: %s", value, encoded)
		}
	}
}

func TestStringPreservesNonSecretOperationalData(t *testing.T) {
	t.Parallel()

	input := "action=update target=release/1.2.3 sha256=0123456789abcdef source=127.0.0.1:8080"
	if got := secretredaction.String(input); got != input {
		t.Fatalf("non-secret value changed:\n got: %q\nwant: %q", got, input)
	}
}
