package web

import (
	"net/http"
	"net/url"
)

// Preserve the Documents origin through preview, edit and save using a local route allowlist.
func documentOrigin(request *http.Request) string {
	value := safeLocalReturnPath(request.FormValue("return_to"))
	parsed, err := url.Parse(value)
	if err != nil || parsed.Path != "/resources/documents" {
		return ""
	}
	return value
}

func documentFileURL(route, path, origin string) string {
	destination := routeFileURL(route, path)
	if origin != "" {
		destination += "&" + url.Values{"return_to": {origin}}.Encode()
	}
	return destination
}

func textPageBack(request *http.Request, parent string) (string, string) {
	if origin := documentOrigin(request); origin != "" {
		return origin, "editor.back_documents"
	}
	return filesURL(parent), "editor.back_directory"
}
