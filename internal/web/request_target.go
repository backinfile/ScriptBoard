package web

import (
	"net/http"
	"strings"
)

// validRequestTarget rejects path spellings that an authorization layer and a
// downstream HTTP implementation can interpret as different resources.
func validRequestTarget(request *http.Request) bool {
	if request.URL == nil || request.URL.IsAbs() || !strings.HasPrefix(request.RequestURI, "/") {
		return false
	}
	escapedPath := strings.ToLower(request.URL.EscapedPath())
	if strings.Contains(escapedPath, "%2f") || strings.Contains(escapedPath, "%5c") {
		return false
	}
	if strings.Contains(request.URL.Path, "//") {
		return false
	}
	for _, segment := range strings.Split(request.URL.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
