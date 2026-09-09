package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateFrameAncestors accepts exact HTTP/HTTPS origins or an explicit all-origins wildcard.
func ValidateFrameAncestors(origins []string) error {
	for _, raw := range origins {
		if raw == "*" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, " *;,'\"\t\r\n#") || raw != u.Scheme+"://"+u.Host {
			return fmt.Errorf("frame_ancestors must contain exact HTTP/HTTPS origins or *: %q", raw)
		}
	}
	return nil
}
