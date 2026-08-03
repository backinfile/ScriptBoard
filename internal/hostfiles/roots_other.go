//go:build !windows

package hostfiles

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type systemTopology struct{}

func (systemTopology) Roots() ([]Entry, error) {
	root := string(filepath.Separator)
	return []Entry{{Name: root, Path: root, Kind: Directory}}, nil
}

func (systemTopology) FilesystemRoot(path string) (string, error) {
	return filesystemRoot(path)
}

func (systemTopology) Restricted(path string) bool {
	clean := filepath.Clean(path)
	for _, root := range []string{"/proc", "/sys", "/dev"} {
		if clean == root || strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return true
		}
	}
	for _, root := range restrictedKernelMounts() {
		if clean == root || strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

var restrictedMountsOnce sync.Once
var restrictedMounts []string

func restrictedKernelMounts() []string {
	restrictedMountsOnce.Do(func() {
		file, err := os.Open("/proc/self/mountinfo")
		if err != nil {
			return
		}
		defer file.Close()
		restrictedTypes := map[string]bool{
			"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true,
			"securityfs": true, "debugfs": true, "tracefs": true,
			"cgroup": true, "cgroup2": true, "configfs": true, "pstore": true,
			"efivarfs": true, "bpf": true, "mqueue": true, "hugetlbfs": true,
			"fusectl": true, "autofs": true,
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			separator := -1
			for index, field := range fields {
				if field == "-" {
					separator = index
					break
				}
			}
			if separator < 0 || separator+1 >= len(fields) || len(fields) < 5 || !restrictedTypes[fields[separator+1]] {
				continue
			}
			mountpoint := decodeMountInfoPath(fields[4])
			if filepath.IsAbs(mountpoint) {
				restrictedMounts = append(restrictedMounts, filepath.Clean(mountpoint))
			}
		}
	})
	return restrictedMounts
}

func decodeMountInfoPath(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}
