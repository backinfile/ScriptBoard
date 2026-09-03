package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func brokerServiceLogPath(arguments []string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "--state-root" && filepath.IsAbs(arguments[index+1]) {
			return filepath.Join(filepath.Clean(arguments[index+1]), "logs", "broker.log")
		}
	}
	return ""
}

func rotateBrokerServiceLog(path string, maxBytes int64, generations int) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxBytes || generations < 1 {
		return
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", path, generations))
	for index := generations - 1; index >= 1; index-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", path, index), fmt.Sprintf("%s.%d", path, index+1))
	}
	_ = os.Rename(path, path+".1")
}
