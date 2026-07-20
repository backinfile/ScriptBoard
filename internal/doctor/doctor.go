package doctor

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	_ "modernc.org/sqlite"
)

type Config struct {
	ManagedRoot   string
	StateRoot     string
	GitExecutable string
}

type Check struct {
	Name    string
	Healthy bool
	Detail  string
}

type Report struct {
	Healthy bool
	Checks  []Check
}

func (r Report) HasHealthy(name string) bool {
	for _, check := range r.Checks {
		if check.Name == name && check.Healthy {
			return true
		}
	}
	return false
}

func Run(config Config) Report {
	report := Report{Healthy: true}
	required := func(name string, healthy bool, detail string) {
		report.Checks = append(report.Checks, Check{Name: name, Healthy: healthy, Detail: detail})
		if !healthy {
			report.Healthy = false
		}
	}
	checkDirectory := func(name, path string) {
		info, err := os.Stat(path)
		if err != nil {
			required(name, false, err.Error())
			return
		}
		required(name, info.IsDir(), path)
	}
	checkDirectory("managed-root", config.ManagedRoot)
	checkDirectory("state-root", config.StateRoot)
	checkSQLite(&report, filepath.Join(config.StateRoot, "app.db"))
	checkGit(&report, config.GitExecutable)
	checkExecutors(&report)
	return report
}

func checkSQLite(report *Report, path string) {
	if _, err := os.Stat(path); err != nil {
		report.Healthy = false
		report.Checks = append(report.Checks, Check{Name: "sqlite-integrity", Detail: err.Error()})
		return
	}
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		report.Healthy = false
		report.Checks = append(report.Checks, Check{Name: "sqlite-integrity", Detail: err.Error()})
		return
	}
	defer db.Close()
	var result string
	err = db.QueryRow("PRAGMA integrity_check").Scan(&result)
	healthy := err == nil && result == "ok"
	detail := result
	if err != nil {
		detail = err.Error()
	}
	report.Checks = append(report.Checks, Check{Name: "sqlite-integrity", Healthy: healthy, Detail: detail})
	if !healthy {
		report.Healthy = false
	}
}

func checkGit(report *Report, configured string) {
	executable := configured
	if executable == "" {
		executable, _ = exec.LookPath("git")
	}
	if executable == "" {
		report.Checks = append(report.Checks, Check{Name: "git", Healthy: true, Detail: "未安装；Version Protection 保持关闭时允许"})
		return
	}
	output, err := exec.Command(executable, "--version").CombinedOutput()
	report.Checks = append(report.Checks, Check{Name: "git", Healthy: err == nil, Detail: strings.TrimSpace(string(output))})
}

func checkExecutors(report *Report) {
	names := []string{"bash", "sh", "python3", "python", "pwsh"}
	if runtime.GOOS == "windows" {
		names = []string{"cmd.exe", "pwsh.exe", "powershell.exe", "py.exe", "python.exe", "bash.exe"}
	}
	var available []string
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			available = append(available, path)
		}
	}
	detail := fmt.Sprintf("可用执行器 %d 个", len(available))
	report.Checks = append(report.Checks, Check{Name: "executors", Healthy: len(available) > 0, Detail: detail})
}
