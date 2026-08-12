package buildinfo

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

const (
	Repository             = "backinfile/ScriptBoard"
	DatabaseSchemaVersion  = 42
	UpdaterProtocolVersion = 1
	ReleaseInfoFilename    = "RELEASE.json"
)

// These values are injected by scripts/build-release.ps1. Development builds
// deliberately retain the defaults and never participate in network updates.
var (
	Version               = "development"
	Tag                   = ""
	Commit                = "unknown"
	BuiltAt               = ""
	ReleaseBuildValue     = "false"
	UpdatePublicKeyID     = ""
	UpdatePublicKeyBase64 = ""
	UpdateNextKeyID       = ""
	UpdateNextKeyBase64   = ""
	UpdateRevokedKeyIDs   = ""
)

type Info struct {
	Version                string `json:"version"`
	Tag                    string `json:"tag"`
	Commit                 string `json:"commit"`
	BuiltAt                string `json:"built_at"`
	ReleaseBuild           bool   `json:"release_build"`
	DatabaseSchemaVersion  int    `json:"database_schema"`
	UpdaterProtocolVersion int    `json:"updater_protocol"`
	Repository             string `json:"repository"`
}

func Current() Info {
	release, _ := strconv.ParseBool(ReleaseBuildValue)
	return Info{
		Version: Version, Tag: Tag, Commit: Commit, BuiltAt: BuiltAt,
		ReleaseBuild: release, DatabaseSchemaVersion: DatabaseSchemaVersion,
		UpdaterProtocolVersion: UpdaterProtocolVersion, Repository: Repository,
	}
}

func (i Info) ValidRelease() bool {
	if !i.ReleaseBuild || i.Version == "" || i.Version == "development" || i.Tag != "v"+i.Version {
		return false
	}
	if len(i.Commit) != 40 {
		return false
	}
	for _, character := range i.Commit {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	_, err := time.Parse(time.RFC3339, i.BuiltAt)
	return err == nil
}

func JSON() ([]byte, error) {
	return json.Marshal(Current())
}
