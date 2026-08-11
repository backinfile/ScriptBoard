package doctor

import (
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"scriptboard/internal/auditcheckpoint"
	"scriptboard/internal/buildinfo"
	"scriptboard/internal/diskspace"
	"scriptboard/internal/installation"
	"scriptboard/internal/platformservice"
	"scriptboard/internal/secretredaction"
	"scriptboard/internal/secretstore"
	updatepkg "scriptboard/internal/update"
)

type Config struct {
	StateRoot  string
	ConfigPath string
	Listen     string
	TLSCert    string
	TLSKey     string
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
	checkDirectory("state-root", config.StateRoot)
	credentialKeyPath, credentialKeyErr := secretstore.KeyPathForStateRoot(config.StateRoot)
	if credentialKeyErr != nil {
		required("credential-master-key", false, credentialKeyErr.Error())
	} else if info, statErr := os.Stat(credentialKeyPath); statErr != nil {
		required("credential-master-key", false, statErr.Error())
	} else {
		required("credential-master-key", info.Mode().IsRegular(), credentialKeyPath)
	}
	_, auditKeyPath, auditCheckpointPath, auditCheckpointErr := auditcheckpoint.PathsForStateRoot(config.StateRoot)
	if auditCheckpointErr != nil {
		required("audit-checkpoint-key", false, auditCheckpointErr.Error())
		required("audit-checkpoint", false, auditCheckpointErr.Error())
	} else {
		for _, check := range []struct {
			name string
			path string
		}{{"audit-checkpoint-key", auditKeyPath}, {"audit-checkpoint", auditCheckpointPath}} {
			info, err := os.Stat(check.path)
			if err != nil {
				required(check.name, false, err.Error())
			} else {
				required(check.name, info.Mode().IsRegular(), check.path)
			}
		}
	}
	checkConfig(&report, config.ConfigPath)
	checkDisk(&report, "state-disk", config.StateRoot)
	checkSQLite(&report, filepath.Join(config.StateRoot, "app.db"))
	checkExecutors(&report)
	checkNetwork(&report, config.Listen, config.TLSCert, config.TLSKey)
	checkService(&report)
	checkUpdateInstallation(&report, config.StateRoot)
	for index := range report.Checks {
		report.Checks[index].Detail = secretredaction.String(report.Checks[index].Detail)
	}
	return report
}

func checkUpdateInstallation(report *Report, stateRoot string) {
	build := buildinfo.Current()
	if !build.ValidRelease() {
		report.Checks = append(report.Checks,
			Check{Name: "update-signing-key", Healthy: true, Detail: "development build; update trust is disabled"},
			Check{Name: "update-installation", Healthy: true, Detail: "development or portable installation"},
		)
		return
	}
	keyHealthy := validEmbeddedPublicKey(buildinfo.UpdatePublicKeyID, buildinfo.UpdatePublicKeyBase64)
	if buildinfo.UpdateNextKeyID != "" || buildinfo.UpdateNextKeyBase64 != "" {
		keyHealthy = keyHealthy && validEmbeddedPublicKey(buildinfo.UpdateNextKeyID, buildinfo.UpdateNextKeyBase64) &&
			buildinfo.UpdateNextKeyID != buildinfo.UpdatePublicKeyID
	}
	detail := "embedded Ed25519 update key is valid"
	if !keyHealthy {
		detail = "formal release is missing a valid embedded update signing key"
		report.Healthy = false
	}
	report.Checks = append(report.Checks, Check{Name: "update-signing-key", Healthy: keyHealthy, Detail: detail})

	metadata, err := installation.Load(stateRoot)
	if os.IsNotExist(err) {
		report.Checks = append(report.Checks, Check{Name: "update-installation", Healthy: true, Detail: "portable installation"})
		return
	}
	if err != nil {
		report.Checks = append(report.Checks, Check{Name: "update-installation", Healthy: false, Detail: err.Error()})
		report.Healthy = false
		return
	}
	info, err := installation.ReadVersionInfo(metadata, metadata.Current)
	if err == nil {
		err = installation.ValidateVersion(metadata, metadata.Current, info)
	}
	if err == nil {
		var matches bool
		matches, err = platformservice.MatchesExecutable(installation.ServiceEntryExecutable(metadata), metadata.ConfigPath)
		if err == nil && !matches {
			err = fmt.Errorf("service target does not match the active Installed Release")
		}
	}
	healthy := err == nil
	detail = metadata.InstallRoot + " (" + metadata.Current + ")"
	if err != nil {
		detail = err.Error()
		report.Healthy = false
	}
	report.Checks = append(report.Checks, Check{Name: "update-installation", Healthy: healthy, Detail: detail})
	checkDisk(report, "install-disk", metadata.InstallRoot)

	active, activeErr := updatepkg.LoadActive(stateRoot)
	if os.IsNotExist(activeErr) {
		report.Checks = append(report.Checks, Check{Name: "update-operation", Healthy: true, Detail: "no update operation"})
		return
	}
	if activeErr != nil {
		report.Checks = append(report.Checks, Check{Name: "update-operation", Healthy: false, Detail: activeErr.Error()})
		report.Healthy = false
		return
	}
	operation, operationErr := updatepkg.LoadOperation(stateRoot, active.OperationID)
	operationHealthy := operationErr == nil && operation.Phase != updatepkg.PhaseNeedsRecovery
	operationDetail := fmt.Sprintf("%s (%s)", active.OperationID, operation.Phase)
	if operationErr != nil {
		operationDetail = operationErr.Error()
	}
	if !operationHealthy {
		report.Healthy = false
	}
	report.Checks = append(report.Checks, Check{Name: "update-operation", Healthy: operationHealthy, Detail: operationDetail})
}

func validEmbeddedPublicKey(id, encoded string) bool {
	if id == "" || encoded == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	return err == nil && len(decoded) == 32
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
