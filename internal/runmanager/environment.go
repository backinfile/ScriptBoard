package runmanager

import (
	"os"
	"strings"
)

func runEnvironment(runID, scriptPath string, extra []string) []string {
	return runEnvironmentFromParent(os.Environ(), runID, scriptPath, extra)
}

func runEnvironmentFromParent(parent []string, runID, scriptPath string, extra []string) []string {
	environment := append([]string{}, parent...)
	environment = setEnvironmentValue(environment, "SCRIPTBOARD_RUN_ID", runID)
	environment = setEnvironmentValue(environment, "SCRIPTBOARD_SCRIPT_PATH", scriptPath)
	// 额外环境变量最后写入，保证同名覆盖。
	for _, entry := range extra {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		environment = setEnvironmentValue(environment, name, entry[len(name)+1:])
	}
	return environment
}

func setEnvironmentValue(environment []string, name, value string) []string {
	entry := name + "=" + value
	for index, existing := range environment {
		key, _, ok := strings.Cut(existing, "=")
		if ok && strings.EqualFold(key, name) {
			environment[index] = entry
			return environment
		}
	}
	return append(environment, entry)
}
