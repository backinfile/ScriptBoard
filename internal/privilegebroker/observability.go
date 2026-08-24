package privilegebroker

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"scriptboard/internal/hostsecurity"
	"scriptboard/internal/logstream"
	"scriptboard/internal/servicelogs"
)

type hostSecurityWireRequest struct {
	Refresh  bool                     `json:"refresh,omitempty"`
	Login    *hostsecurity.LoginQuery `json:"login,omitempty"`
	BanPage  int                      `json:"ban_page,omitempty"`
	BanLimit int                      `json:"ban_limit,omitempty"`
}

type hostSecurityWireResponse struct {
	Capabilities *hostsecurity.Capabilities         `json:"capabilities,omitempty"`
	Updates      *hostsecurity.SecurityUpdateReport `json:"updates,omitempty"`
	Logins       *hostsecurity.LoginPage            `json:"logins,omitempty"`
	Bans         *hostsecurity.BanPage              `json:"bans,omitempty"`
}

type serviceLogsWireRequest struct {
	Query servicelogs.Query `json:"query"`
}

type serviceLogsWireResponse struct {
	Report *servicelogs.Report `json:"report,omitempty"`
}

func isObservabilityOperation(operation string) bool {
	switch operation {
	case operationHostSecurityCapabilities, operationHostSecurityUpdates, operationHostSecurityLogins, operationHostSecurityBans, operationServiceLogsList:
		return true
	default:
		return false
	}
}

func validateObservabilityRequest(request wireRequest) error {
	if !isObservabilityOperation(request.Operation) {
		return errors.New("observability operation is invalid")
	}
	if request.SessionToken != "" || request.Capability != "" || request.Action != "" || request.Resource != "" || request.Revision != "" ||
		request.ParametersSHA256 != "" || len(request.Parameters) != 0 || hasMFAFields(request) || hasPasskeyFields(request) ||
		hasRemoteWebsiteFields(request) || hasProviderFields(request) || request.MySQL != nil || request.Redis != nil || request.HostFiles != nil ||
		request.StateBackup != nil || request.Registry != nil || request.Runtime != nil || request.Kubeconfig != nil {
		return errors.New("observability request contains unrelated fields")
	}
	if request.Operation == operationServiceLogsList {
		if request.HostSecurity != nil || request.ServiceLogs == nil || !validServiceLogQuery(request.ServiceLogs.Query) {
			return errors.New("service log request is invalid")
		}
		return nil
	}
	if request.HostSecurity == nil || request.ServiceLogs != nil {
		return errors.New("host security request is invalid")
	}
	payload := request.HostSecurity
	switch request.Operation {
	case operationHostSecurityCapabilities:
		if payload.Refresh || payload.Login != nil || payload.BanPage != 0 || payload.BanLimit != 0 {
			return errors.New("host security capabilities request contains unrelated fields")
		}
	case operationHostSecurityUpdates:
		if payload.Login != nil || payload.BanPage != 0 || payload.BanLimit != 0 {
			return errors.New("host security updates request contains unrelated fields")
		}
	case operationHostSecurityLogins:
		if payload.Refresh || payload.Login == nil || payload.BanPage != 0 || payload.BanLimit != 0 || !validHostSecurityLoginQuery(*payload.Login) {
			return errors.New("host security login request is invalid")
		}
	case operationHostSecurityBans:
		if payload.Refresh || payload.Login != nil || payload.BanPage < 1 || payload.BanPage > 100000 || payload.BanLimit < 1 || payload.BanLimit > 100 {
			return errors.New("host security ban request is invalid")
		}
	}
	return nil
}

func validHostSecurityLoginQuery(query hostsecurity.LoginQuery) bool {
	if query.Range != "24h" && query.Range != "7d" && query.Range != "30d" || query.Page < 1 || query.Page > 100000 {
		return false
	}
	if query.PageSize != 5 && query.PageSize != 20 && query.PageSize != 50 && query.PageSize != 100 {
		return false
	}
	if query.Result != "" && query.Result != hostsecurity.ResultSuccess && query.Result != hostsecurity.ResultFailure {
		return false
	}
	if query.Type != "" && query.Type != "ssh" && query.Type != "rdp" || len(query.Type) > 16 {
		return false
	}
	return query.Start.IsZero() && query.End.IsZero() || !query.Start.IsZero() && !query.End.IsZero() && query.End.After(query.Start) && query.End.Sub(query.Start) <= 31*24*time.Hour
}

func validServiceLogQuery(query servicelogs.Query) bool {
	if query.Service != "" && query.Service != "web" && query.Service != "broker" && query.Service != "ai" && query.Service != "runner" {
		return false
	}
	if query.Range != "24h" && query.Range != "7d" && query.Range != "30d" {
		return false
	}
	if query.Severity != "" && query.Severity != logstream.SeverityNormal && query.Severity != logstream.SeverityWarning && query.Severity != logstream.SeverityError {
		return false
	}
	return len(query.Search) <= 128 && utf8.ValidString(query.Search) && strings.IndexFunc(query.Search, unicode.IsControl) < 0
}

func (server *Server) observabilityOperation(request wireRequest) wireResponse {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if request.Operation == operationServiceLogsList {
		if server.serviceLogs == nil {
			return wireResponse{Status: statusError, ErrorCode: "service_logs_unavailable", Message: "service log reader is unavailable"}
		}
		report, err := server.serviceLogs.List(ctx, request.ServiceLogs.Query)
		if err != nil {
			return wireResponse{Status: statusError, ErrorCode: "service_logs_failed", Message: "service log collection failed"}
		}
		return wireResponse{Status: statusOK, ServiceLogs: &serviceLogsWireResponse{Report: &report}}
	}
	if server.hostSecurity == nil {
		return wireResponse{Status: statusError, ErrorCode: "host_security_unavailable", Message: "host security service is unavailable"}
	}
	response := &hostSecurityWireResponse{}
	var err error
	switch request.Operation {
	case operationHostSecurityCapabilities:
		capabilities := server.hostSecurity.Capabilities(ctx)
		response.Capabilities = &capabilities
	case operationHostSecurityUpdates:
		var report hostsecurity.SecurityUpdateReport
		report, err = server.hostSecurity.SecurityUpdates(ctx, request.HostSecurity.Refresh)
		response.Updates = &report
	case operationHostSecurityLogins:
		var page hostsecurity.LoginPage
		page, err = server.hostSecurity.Logins(ctx, *request.HostSecurity.Login)
		response.Logins = &page
	case operationHostSecurityBans:
		var page hostsecurity.BanPage
		page, err = server.hostSecurity.Bans(ctx, request.HostSecurity.BanPage, request.HostSecurity.BanLimit)
		response.Bans = &page
	}
	if err != nil {
		return wireResponse{Status: statusError, ErrorCode: "host_security_failed", Message: "host security collection failed"}
	}
	return wireResponse{Status: statusOK, HostSecurity: response}
}

type RemoteHostSecurity struct {
	client                *Client
	controlPlanePrivilege hostsecurity.RuntimePrivilege
}

func NewRemoteHostSecurity(client *Client, controlPlanePrivilege hostsecurity.RuntimePrivilege) *RemoteHostSecurity {
	return &RemoteHostSecurity{client: client, controlPlanePrivilege: controlPlanePrivilege}
}

func (service *RemoteHostSecurity) call(ctx context.Context, operation string, payload hostSecurityWireRequest) (*hostSecurityWireResponse, error) {
	requestID, err := observabilityRequestID("host-security")
	if err != nil {
		return nil, err
	}
	response, err := service.client.call(ctx, wireRequest{Version: ProtocolVersion, Operation: operation, RequestID: requestID, HostSecurity: &payload})
	if err != nil {
		return nil, err
	}
	if response.HostSecurity == nil {
		return nil, errors.New("privileged Broker returned no host security response")
	}
	return response.HostSecurity, nil
}

func (service *RemoteHostSecurity) Capabilities(ctx context.Context) hostsecurity.Capabilities {
	response, err := service.call(ctx, operationHostSecurityCapabilities, hostSecurityWireRequest{})
	if err != nil || response.Capabilities == nil {
		message := "privileged Broker returned no host security capabilities"
		if err != nil {
			message = err.Error()
		}
		return hostsecurity.Capabilities{SSH: hostsecurity.Component{Error: message}, UFW: hostsecurity.Component{Error: message}, Firewall: hostsecurity.Component{Error: message}}
	}
	capabilities := *response.Capabilities
	// Broker reports the collector identity; only Web composition can state the control-plane identity.
	capabilities.ControlPlanePrivilege = service.controlPlanePrivilege
	return capabilities
}

func (service *RemoteHostSecurity) SecurityUpdates(ctx context.Context, refresh bool) (hostsecurity.SecurityUpdateReport, error) {
	response, err := service.call(ctx, operationHostSecurityUpdates, hostSecurityWireRequest{Refresh: refresh})
	if err != nil || response.Updates == nil {
		if err == nil {
			err = errors.New("privileged Broker returned no security update report")
		}
		return hostsecurity.SecurityUpdateReport{}, err
	}
	return *response.Updates, nil
}

func (service *RemoteHostSecurity) Logins(ctx context.Context, query hostsecurity.LoginQuery) (hostsecurity.LoginPage, error) {
	response, err := service.call(ctx, operationHostSecurityLogins, hostSecurityWireRequest{Login: &query})
	if err != nil || response.Logins == nil {
		if err == nil {
			err = errors.New("privileged Broker returned no security login page")
		}
		return hostsecurity.LoginPage{}, err
	}
	return *response.Logins, nil
}

func (service *RemoteHostSecurity) Bans(ctx context.Context, page, pageSize int) (hostsecurity.BanPage, error) {
	response, err := service.call(ctx, operationHostSecurityBans, hostSecurityWireRequest{BanPage: page, BanLimit: pageSize})
	if err != nil || response.Bans == nil {
		if err == nil {
			err = errors.New("privileged Broker returned no security ban page")
		}
		return hostsecurity.BanPage{}, err
	}
	return *response.Bans, nil
}

func (service *RemoteHostSecurity) Install(ctx context.Context, component string) error {
	parameters, _ := json.Marshal(installComponentParameters{Component: component})
	return service.client.Invoke(ctx, ActionInstallComponent, component, "component-v1", parameters)
}

func (service *RemoteHostSecurity) Unban(ctx context.Context, jail, ip string) error {
	parameters, _ := json.Marshal(unbanParameters{Jail: jail, IP: ip})
	return service.client.Invoke(ctx, ActionFail2BanUnban, jail+":"+ip, "ban-v1", parameters)
}

func (service *RemoteHostSecurity) Ban(ctx context.Context, jail, ip string, durationSeconds int) error {
	parameters, _ := json.Marshal(banParameters{Jail: jail, IP: ip, DurationSeconds: durationSeconds})
	resource := fmt.Sprintf("%s:%s:%d", jail, ip, durationSeconds)
	return service.client.Invoke(ctx, ActionFail2BanBan, resource, "ban-v2", parameters)
}

func (service *RemoteHostSecurity) EnableUFW(ctx context.Context, baseline []hostsecurity.FirewallRule) error {
	parameters, _ := json.Marshal(enableUFWParameters{Baseline: baseline})
	return service.client.Invoke(ctx, ActionUFWEnable, "ufw", rulesRevision(baseline, hostsecurity.UFWDefaults{}), parameters)
}

func (service *RemoteHostSecurity) ApplyUFW(ctx context.Context, baseline, desired []hostsecurity.FirewallRule, baselineDefaults, desiredDefaults hostsecurity.UFWDefaults) error {
	parameters, _ := json.Marshal(applyUFWParameters{Baseline: baseline, Desired: desired, BaselineDefaults: baselineDefaults, DesiredDefaults: desiredDefaults})
	return service.client.Invoke(ctx, ActionUFWApply, "ufw", rulesRevision(baseline, baselineDefaults), parameters)
}

func (service *RemoteHostSecurity) AddWindowsFirewallRule(ctx context.Context, rule hostsecurity.FirewallRule) error {
	parameters, _ := json.Marshal(addWindowsFirewallParameters{Rule: rule})
	return service.client.Invoke(ctx, ActionWindowsFirewallAdd, rule.Name, "absent-v1", parameters)
}

func (service *RemoteHostSecurity) SetWindowsFirewallRuleEnabled(ctx context.Context, id string, enabled bool) error {
	baseline, err := service.windowsRule(ctx, id)
	if err != nil {
		return err
	}
	parameters, _ := json.Marshal(setWindowsFirewallParameters{ID: id, Enabled: enabled, Baseline: baseline})
	return service.client.Invoke(ctx, ActionWindowsFirewallSet, id, ruleRevision(baseline), parameters)
}

func (service *RemoteHostSecurity) DeleteWindowsFirewallRule(ctx context.Context, id string) error {
	baseline, err := service.windowsRule(ctx, id)
	if err != nil {
		return err
	}
	parameters, _ := json.Marshal(deleteWindowsFirewallParameters{ID: id, Baseline: baseline})
	return service.client.Invoke(ctx, ActionWindowsFirewallDelete, id, ruleRevision(baseline), parameters)
}

func (service *RemoteHostSecurity) windowsRule(ctx context.Context, id string) (hostsecurity.FirewallRule, error) {
	for _, rule := range service.Capabilities(ctx).Rules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return hostsecurity.FirewallRule{}, hostsecurity.ErrInvalidRule
}

type RemoteServiceLogs struct{ client *Client }

func NewRemoteServiceLogs(client *Client) *RemoteServiceLogs {
	return &RemoteServiceLogs{client: client}
}

func (reader *RemoteServiceLogs) List(ctx context.Context, query servicelogs.Query) (servicelogs.Report, error) {
	requestID, err := observabilityRequestID("service-logs")
	if err != nil {
		return servicelogs.Report{}, err
	}
	response, err := reader.client.call(ctx, wireRequest{Version: ProtocolVersion, Operation: operationServiceLogsList, RequestID: requestID, ServiceLogs: &serviceLogsWireRequest{Query: query}})
	if err != nil {
		return servicelogs.Report{}, err
	}
	if response.ServiceLogs == nil || response.ServiceLogs.Report == nil {
		return servicelogs.Report{}, errors.New("privileged Broker returned no service log report")
	}
	return *response.ServiceLogs.Report, nil
}

func observabilityRequestID(prefix string) (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + ":" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

var _ hostsecurity.Service = (*RemoteHostSecurity)(nil)
var _ servicelogs.Reader = (*RemoteServiceLogs)(nil)
