package buildinfo

import "testing"

func TestDevelopmentBuildIsNotAValidRelease(t *testing.T) {
	if Current().ValidRelease() {
		t.Fatal("development build reported itself as a valid release")
	}
}

func TestValidRelease(t *testing.T) {
	info := Info{
		Version: "1.2.3", Tag: "v1.2.3",
		Commit:  "0123456789abcdef0123456789abcdef01234567",
		BuiltAt: "2026-07-29T00:00:00Z", ReleaseBuild: true,
	}
	if !info.ValidRelease() {
		t.Fatal("valid release rejected")
	}
}
