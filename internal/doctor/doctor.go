package doctor

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/diskspace"
	"scriptboard/internal/platformservice"
)

type Config struct {
	ManagedRoot   string
	StateRoot     string
	GitExecutable string
	ConfigPath    string
	Listen        string
	TLSCert       string
	TLSKey        string
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
	checkConfig(&report, config.ConfigPath)
	checkDisk(&report, "managed-disk", config.ManagedRoot)
	checkDisk(&report, "state-disk", config.StateRoot)
	checkSQLite(&report, filepath.Join(config.StateRoot, "app.db"))
	checkGit(&report, config.GitExecutable)
	checkExecutors(&report)
	checkNetwork(&report, config.Listen, config.TLSCert, config.TLSKey)
	checkService(&report)
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
	var journal string
	var version int
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err == nil {
		healthy = strings.EqualFold(journal, "wal")
		report.Checks = append(report.Checks, Check{Name: "sqlite-wal", Healthy: healthy, Detail: journal})
		if !healthy {
			report.Healthy = false
		}
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err == nil {
		report.Checks = append(report.Checks, Check{Name: "sqlite-schema", Healthy: version > 0, Detail: fmt.Sprintf("version %d", version)})
		if version <= 0 {
			report.Healthy = false
		}
	}
	checkRunLogs(report, db)
}

func checkRunLogs(report *Report, db *sql.DB) {
	rows, err := db.Query("SELECT id, log_path FROM runs WHERE log_path <> '' AND log_expired = 0")
	if err != nil {
		return
	}
	defer rows.Close()
	missing := 0
	for rows.Next() {
		var id, path string
		if rows.Scan(&id, &path) == nil {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				missing++
			}
		}
	}
	healthy := missing == 0
	report.Checks = append(report.Checks, Check{Name: "run-logs", Healthy: healthy, Detail: fmt.Sprintf("missing %d", missing)})
	if !healthy {
		report.Healthy = false
	}
}

func checkConfig(report *Report, path string) {
	if path == "" {
		report.Checks = append(report.Checks, Check{Name: "config", Healthy: true, Detail: "使用默认值、环境变量或命令行配置"})
		return
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		report.Checks = append(report.Checks, Check{Name: "config", Healthy: true, Detail: "配置文件不存在；当前使用其他配置层"})
		return
	}
	healthy := err == nil && info.Mode().IsRegular()
	detail := path
	if err != nil {
		detail = err.Error()
	}
	report.Checks = append(report.Checks, Check{Name: "config", Healthy: healthy, Detail: detail})
	if !healthy {
		report.Healthy = false
	}
}

func checkNetwork(report *Report, address, certificate, key string) {
	if address == "" {
		report.Checks = append(report.Checks, Check{Name: "network", Healthy: true, Detail: "未提供监听地址，跳过端口检查"})
		return
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		report.Checks = append(report.Checks, Check{Name: "network", Detail: err.Error()})
		report.Healthy = false
		return
	}
	tlsHealthy := (certificate == "") == (key == "")
	tlsDetail := "未启用 TLS"
	if tlsHealthy && certificate != "" {
		_, err := tls.LoadX509KeyPair(certificate, key)
		tlsHealthy = err == nil
		tlsDetail = "证书与私钥有效"
		if err != nil {
			tlsDetail = err.Error()
		}
	}
	report.Checks = append(report.Checks, Check{Name: "tls", Healthy: tlsHealthy, Detail: tlsDetail})
	if !tlsHealthy {
		report.Healthy = false
	}
	listener, err := net.Listen("tcp", address)
	if err == nil {
		_ = listener.Close()
		report.Checks = append(report.Checks, Check{Name: "listen-port", Healthy: true, Detail: "端口可用"})
		return
	}
	connection, dialErr := net.DialTimeout("tcp", address, time.Second)
	if dialErr == nil {
		_ = connection.Close()
		report.Checks = append(report.Checks, Check{Name: "listen-port", Healthy: true, Detail: "端口正由可连接的服务使用"})
		return
	}
	report.Checks = append(report.Checks, Check{Name: "listen-port", Healthy: false, Detail: err.Error()})
	report.Healthy = false
}

func checkService(report *Report) {
	status, err := platformservice.Status()
	detail := strings.TrimSpace(status)
	if detail == "" && err != nil {
		detail = err.Error()
	}
	// 未安装服务不妨碍手动运行，因此只报告而不使整个 doctor 失败。
	report.Checks = append(report.Checks, Check{Name: "service", Healthy: true, Detail: detail})
}

func checkDisk(report *Report, name, path string) {
	available, err := diskspace.Available(path)
	healthy := err == nil && available >= diskspace.MinimumWritableBytes
	detail := fmt.Sprintf("%d bytes available", available)
	if err != nil {
		detail = err.Error()
	}
	report.Checks = append(report.Checks, Check{Name: name, Healthy: healthy, Detail: detail})
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
