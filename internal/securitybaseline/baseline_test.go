package securitybaseline

import (
	"testing"

	"scriptboard/internal/hostsecurity"
)

func TestLinuxBaselineUsesEffectiveValuesAndPendingSecurityUpdates(t *testing.T) {
	report := Evaluate(hostsecurity.Capabilities{
		OS: "linux", AdministratorKnown: true, Administrator: false,
		Firewall: hostsecurity.Component{Installed: true, Running: true},
		SSH:      hostsecurity.Component{Installed: true, Running: true},
		SSHLogin: hostsecurity.SSHLoginSurface{PublicKeyAuthentication: "yes", PasswordAuthentication: "yes", RootLogin: "prohibit-password", EmptyPasswords: "no", MaxAuthTries: 3},
		Fail2Ban: hostsecurity.Component{Installed: true, Running: true},
	}, hostsecurity.SecurityUpdateReport{Supported: true, Provider: "APT", Updates: []hostsecurity.SecurityUpdate{{Identifier: "openssl"}}}, nil)
	if report.Attention != 2 || report.Passed != 7 || report.Score != 77 {
		t.Fatalf("baseline report = %#v", report)
	}
	assertCheck(t, report, "ssh-password", StatusAttention)
	assertCheck(t, report, "security-updates", StatusAttention)
	assertCheck(t, report, "least-privilege", StatusPass)
}

func TestWindowsBaselineFlagsAdministratorWebAndDisabledProfile(t *testing.T) {
	report := Evaluate(hostsecurity.Capabilities{
		OS: "windows", AdministratorKnown: true, Administrator: true,
		Firewall: hostsecurity.Component{Installed: true, Running: true},
		Profiles: []hostsecurity.FirewallProfile{{Name: "Domain", Enabled: true}, {Name: "Public", Enabled: false}},
	}, hostsecurity.SecurityUpdateReport{Supported: true, Provider: "Windows Update Agent"}, nil)
	assertCheck(t, report, "least-privilege", StatusAttention)
	assertCheck(t, report, "windows-firewall-profiles", StatusAttention)
	if report.Attention != 2 || report.Passed != 2 || report.Score != 50 {
		t.Fatalf("baseline report = %#v", report)
	}
}

func assertCheck(t *testing.T, report Report, id string, status Status) {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			if check.Status != status {
				t.Fatalf("check %s status=%s want=%s", id, check.Status, status)
			}
			return
		}
	}
	t.Fatalf("check %s is missing: %#v", id, report.Checks)
}
