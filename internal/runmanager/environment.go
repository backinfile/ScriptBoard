package runmanager

import (
	"os"
	"strings"
)

func runEnvironment(runID, scriptPath string) []string {
	return runEnvironmentFromParent(os.Environ(), runID, scriptPath)
}

func runEnvironmentFromParent(parent []string, runID, scriptPath string) []string {
	environment := append([]string{}, parent...)
	environment = setEnvironmentValue(environment, "SCRIPTBOARD_RUN_ID", runID)
	environment = setEnvironmentValue(environment, "SCRIPTBOARD_SCRIPT_PATH", scriptPath)
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
