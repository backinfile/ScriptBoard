package securitybaseline

import (
	"fmt"
	"strings"

	"scriptboard/internal/hostsecurity"
)

type Status string

const (
	StatusPass        Status = "pass"
	StatusAttention   Status = "attention"
	StatusUnavailable Status = "unavailable"
)

type Check struct {
	ID       string
	Category string
	Title    string
	Status   Status
	Evidence string
	Guidance string
}

type Report struct {
	Checks      []Check
	Passed      int
	Attention   int
	Unavailable int
	Score       int
}

func Evaluate(capabilities hostsecurity.Capabilities, updates hostsecurity.SecurityUpdateReport, updateErr error) Report {
	report := Report{}
	add := func(check Check) { report.Checks = append(report.Checks, check) }
	if capabilities.ControlPlanePrivilege.Known {
		check := Check{ID: "least-privilege", Category: "service", Title: "Web control plane least privilege", Status: StatusPass, Evidence: "Web process is not running with host administrator privileges"}
		if capabilities.ControlPlanePrivilege.Administrator {
			check.Status = StatusAttention
			check.Evidence = "Web process has host administrator privileges"
			check.Guidance = "Use the managed three-service installation so privileged mutations stay in Broker."
		}
		add(check)
	} else {
		add(Check{ID: "least-privilege", Category: "service", Title: "Web control plane least privilege", Status: StatusUnavailable, Evidence: "Runtime privilege could not be determined"})
	}
	if capabilities.Firewall.Error != "" {
		add(Check{ID: "firewall", Category: "network", Title: "Host firewall", Status: StatusUnavailable, Evidence: capabilities.Firewall.Error})
	} else if capabilities.Firewall.Installed && capabilities.Firewall.Running {
		add(Check{ID: "firewall", Category: "network", Title: "Host firewall", Status: StatusPass, Evidence: "Host firewall is enabled"})
	} else {
		add(Check{ID: "firewall", Category: "network", Title: "Host firewall", Status: StatusAttention, Evidence: "Host firewall is not enabled", Guidance: "Enable the platform firewall after preserving the active management path."})
	}
	if updateErr != nil {
		add(Check{ID: "security-updates", Category: "patching", Title: "OS security updates", Status: StatusUnavailable, Evidence: updateErr.Error()})
	} else if !updates.Supported {
		add(Check{ID: "security-updates", Category: "patching", Title: "OS security updates", Status: StatusUnavailable, Evidence: "No supported update metadata provider is available"})
	} else if len(updates.Updates) == 0 {
		add(Check{ID: "security-updates", Category: "patching", Title: "OS security updates", Status: StatusPass, Evidence: "No pending security updates are reported by " + updates.Provider})
	} else {
		add(Check{ID: "security-updates", Category: "patching", Title: "OS security updates", Status: StatusAttention, Evidence: fmt.Sprintf("%d pending security updates reported by %s", len(updates.Updates), updates.Provider), Guidance: "Review and install updates through the operating system maintenance process."})
	}
	if capabilities.OS == "linux" {
		evaluateLinux(&report, capabilities)
	} else if capabilities.OS == "windows" {
		evaluateWindows(&report, capabilities)
	}
	for _, check := range report.Checks {
		switch check.Status {
		case StatusPass:
			report.Passed++
		case StatusAttention:
			report.Attention++
		default:
			report.Unavailable++
		}
	}
	available := report.Passed + report.Attention
	if available > 0 {
		report.Score = report.Passed * 100 / available
	}
	return report
}

func evaluateLinux(report *Report, capabilities hostsecurity.Capabilities) {
	add := func(check Check) { report.Checks = append(report.Checks, check) }
	if !capabilities.SSH.Installed {
		add(Check{ID: "ssh-service", Category: "remote-login", Title: "SSH service", Status: StatusPass, Evidence: "OpenSSH server is not installed"})
		return
	}
	if capabilities.SSH.Error != "" {
		add(Check{ID: "ssh-effective-config", Category: "remote-login", Title: "SSH effective configuration", Status: StatusUnavailable, Evidence: capabilities.SSH.Error})
	} else {
		add(booleanCheck("ssh-public-key", "remote-login", "SSH public-key authentication", strings.EqualFold(capabilities.SSHLogin.PublicKeyAuthentication, "yes"), capabilities.SSHLogin.PublicKeyAuthentication, "Enable public-key authentication before disabling weaker methods."))
		add(booleanCheck("ssh-password", "remote-login", "SSH password authentication", strings.EqualFold(capabilities.SSHLogin.PasswordAuthentication, "no"), capabilities.SSHLogin.PasswordAuthentication, "Disable SSH password authentication after confirming key access."))
		rootSafe := strings.EqualFold(capabilities.SSHLogin.RootLogin, "no") || strings.EqualFold(capabilities.SSHLogin.RootLogin, "prohibit-password") || strings.EqualFold(capabilities.SSHLogin.RootLogin, "without-password")
		add(booleanCheck("ssh-root", "remote-login", "SSH root sign-in", rootSafe, capabilities.SSHLogin.RootLogin, "Disable direct root sign-in or restrict it to keys."))
		add(booleanCheck("ssh-empty-passwords", "remote-login", "SSH empty passwords", strings.EqualFold(capabilities.SSHLogin.EmptyPasswords, "no"), capabilities.SSHLogin.EmptyPasswords, "Set PermitEmptyPasswords no."))
		maxAuthSafe := capabilities.SSHLogin.MaxAuthTries > 0 && capabilities.SSHLogin.MaxAuthTries <= 6
		add(booleanCheck("ssh-max-auth", "remote-login", "SSH authentication attempts", maxAuthSafe, fmt.Sprintf("MaxAuthTries=%d", capabilities.SSHLogin.MaxAuthTries), "Set a bounded MaxAuthTries value of 6 or less."))
	}
	if capabilities.Fail2Ban.Installed && capabilities.Fail2Ban.Running {
		add(Check{ID: "fail2ban", Category: "remote-login", Title: "SSH brute-force protection", Status: StatusPass, Evidence: "Fail2Ban sshd protection is running"})
	} else {
		add(Check{ID: "fail2ban", Category: "remote-login", Title: "SSH brute-force protection", Status: StatusAttention, Evidence: "Fail2Ban sshd protection is not running", Guidance: "Install and start Fail2Ban or provide equivalent host-level protection."})
	}
}

func evaluateWindows(report *Report, capabilities hostsecurity.Capabilities) {
	if len(capabilities.Profiles) == 0 {
		report.Checks = append(report.Checks, Check{ID: "windows-firewall-profiles", Category: "network", Title: "Windows firewall profiles", Status: StatusUnavailable, Evidence: "Firewall profiles could not be read"})
		return
	}
	disabled := make([]string, 0)
	for _, profile := range capabilities.Profiles {
		if !profile.Enabled {
			disabled = append(disabled, profile.Name)
		}
	}
	if len(disabled) == 0 {
		report.Checks = append(report.Checks, Check{ID: "windows-firewall-profiles", Category: "network", Title: "Windows firewall profiles", Status: StatusPass, Evidence: "All detected Windows Defender Firewall profiles are enabled"})
	} else {
		report.Checks = append(report.Checks, Check{ID: "windows-firewall-profiles", Category: "network", Title: "Windows firewall profiles", Status: StatusAttention, Evidence: "Disabled profiles: " + strings.Join(disabled, ", "), Guidance: "Enable every applicable Windows Defender Firewall profile."})
	}
}

func booleanCheck(id, category, title string, pass bool, evidence, guidance string) Check {
	status := StatusAttention
	if pass {
		status = StatusPass
	}
	if strings.TrimSpace(evidence) == "" {
		status = StatusUnavailable
		evidence = "Effective value is unavailable"
	}
	if status != StatusAttention {
		guidance = ""
	}
	return Check{ID: id, Category: category, Title: title, Status: status, Evidence: evidence, Guidance: guidance}
}
