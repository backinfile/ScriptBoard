package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"scriptboard/internal/config"
	"testing"
)

func TestFrameAncestorsValidationAndPrecedence(t *testing.T) {
	for _, origin := range []string{"http://localhost:8787", "https://parent.example", "http://[::1]:8787"} {
		if err := config.ValidateFrameAncestors([]string{origin}); err != nil {
			t.Fatal(err)
		}
	}
	for _, origin := range []string{"*", "'self'", "https://*.example", "https://a.example/", "https://a.example/path", "https://a.example?", "https://a.example#", "https://u:p@a.example", "https://a.example; frame-src *", "javascript:alert(1)", "https://", "https://a.example\n"} {
		if err := config.ValidateFrameAncestors([]string{origin}); err == nil {
			t.Errorf("accepted %q", origin)
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("frame_ancestors: [http://yaml.example]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		env         string
		flags, want []string
	}{
		{want: []string{"http://yaml.example"}},
		{env: "https://env.example,http://localhost:9000", want: []string{"https://env.example", "http://localhost:9000"}},
		{env: "https://env.example", flags: []string{"--frame-ancestor", "https://cli.example", "--frame-ancestor", "http://localhost:9001"}, want: []string{"https://cli.example", "http://localhost:9001"}},
	} {
		args := append([]string{"--config", path}, tc.flags...)
		cfg, err := config.Load(args, func(key string) string {
			if key == "SCRIPTBOARD_FRAME_ANCESTORS" {
				return tc.env
			}
			return ""
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg.FrameAncestors, tc.want) {
			t.Fatalf("got %v want %v", cfg.FrameAncestors, tc.want)
		}
	}
	if _, err := config.Load([]string{"--config", path, "--frame-ancestor", "*"}, func(string) string { return "" }); err == nil {
		t.Fatal("invalid CLI origin accepted")
	}
}
