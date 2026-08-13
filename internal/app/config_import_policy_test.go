package app

import "testing"

func TestJSONConfigurationImportPolicyRejectsActiveAndMismatchedContent(t *testing.T) {
	t.Parallel()
	valid := []byte(`{"format":"scriptboard.fixture"}`)
	if err := validateJSONConfigurationImport("fixture.json", "application/json", valid, 1024); err != nil {
		t.Fatalf("valid JSON configuration rejected: %v", err)
	}
	if err := validateJSONConfigurationImport("fixture.json", "application/octet-stream", valid, 1024); err != nil {
		t.Fatalf("browser octet-stream JSON rejected: %v", err)
	}
	tests := []struct {
		name, filename, contentType string
		body                        []byte
		maximum                     int64
	}{
		{name: "active extension", filename: "fixture.json.exe", contentType: "application/json", body: valid, maximum: 1024},
		{name: "wrong mime", filename: "fixture.json", contentType: "image/svg+xml", body: valid, maximum: 1024},
		{name: "array root", filename: "fixture.json", contentType: "application/json", body: []byte(`[]`), maximum: 1024},
		{name: "binary", filename: "fixture.json", contentType: "application/json", body: []byte("{\x00}"), maximum: 1024},
		{name: "too large", filename: "fixture.json", contentType: "application/json", body: valid, maximum: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateJSONConfigurationImport(test.filename, test.contentType, test.body, test.maximum); err == nil {
				t.Fatal("unsafe configuration import was accepted")
			}
		})
	}
}
