package update

import "errors"

const (
	SourceGitHub     = "github"
	SourceGHProxy    = "gh-proxy"
	SourceGHProxyNet = "ghproxy-net"
)

type SourceDescriptor struct {
	ID   string
	Host string
}

var sourceDescriptors = []SourceDescriptor{
	{ID: SourceGitHub, Host: "github.com"},
	{ID: SourceGHProxy, Host: "gh-proxy.com"},
	{ID: SourceGHProxyNet, Host: "ghproxy.net"},
}

var ErrUnknownSource = errors.New("unknown update source")

func AvailableSources() []SourceDescriptor {
	return append([]SourceDescriptor(nil), sourceDescriptors...)
}

func validSourceID(id string) bool {
	for _, descriptor := range sourceDescriptors {
		if descriptor.ID == id {
			return true
		}
	}
	return false
}

func defaultSources() map[string]ReleaseSource {
	return map[string]ReleaseSource{
		SourceGitHub:     NewGitHubSource(),
		SourceGHProxy:    newGitHubProxySource("https://gh-proxy.com/", true),
		SourceGHProxyNet: newGitHubProxySource("https://ghproxy.net/", false),
	}
}

func normalizeSourceID(id string) (string, error) {
	if id == "" {
		return SourceGitHub, nil
	}
	if !validSourceID(id) {
		return "", ErrUnknownSource
	}
	return id, nil
}
