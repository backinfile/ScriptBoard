package runmanager

import (
	"os"
	"runtime"
	"strings"
)

func runEnvironment(runID, scriptPath string) []string {
	return minimalRunEnvironment(runtime.GOOS, os.Environ(), runID, scriptPath)
}

func minimalRunEnvironment(goos string, parent []string, runID, scriptPath string) []string {
	var environment []string
	if goos == "windows" {
		root := environmentValue(parent, "SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		root = strings.TrimRight(root, `\/`)
		environment = []string{
			"SystemRoot=" + root,
			"WINDIR=" + root,
			"ComSpec=" + root + `\System32\cmd.exe`,
			"PATH=" + root + `\System32;` + root,
			"PATHEXT=.COM;.EXE;.BAT;.CMD",
			"TEMP=" + root + `\Temp`,
			"TMP=" + root + `\Temp`,
		}
	} else {
		environment = []string{
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"HOME=/var/empty",
			"LANG=C.UTF-8",
			"LC_ALL=C.UTF-8",
			"TMPDIR=/tmp",
		}
	}
	return append(environment,
		"SCRIPTBOARD_RUN_ID="+runID,
		"SCRIPTBOARD_SCRIPT_PATH="+scriptPath,
	)
}

func environmentValue(environment []string, name string) string {
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}
