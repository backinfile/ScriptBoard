package privilegebroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"scriptboard/internal/hostsecurity"
)

type HostSecurityService struct {
	reader hostsecurity.Service
	client *Client
}

func NewHostSecurityService(reader hostsecurity.Service, client *Client) (*HostSecurityService, error) {
	if reader == nil || client == nil {
		return nil, errors.New("host security reader and privileged Broker client are required")
	}
	return &HostSecurityService{reader: reader, client: client}, nil
}

func (service *HostSecurityService) Capabilities(ctx context.Context) hostsecurity.Capabilities {
	return service.reader.Capabilities(ctx)
}

func (service *HostSecurityService) SecurityUpdates(ctx context.Context, refresh bool) (hostsecurity.SecurityUpdateReport, error) {
	return service.reader.SecurityUpdates(ctx, refresh)
}

func (service *HostSecurityService) Logins(ctx context.Context, query hostsecurity.LoginQuery) (hostsecurity.LoginPage, error) {
	return service.reader.Logins(ctx, query)
}

func (service *HostSecurityService) Bans(ctx context.Context, page, pageSize int) (hostsecurity.BanPage, error) {
	return service.reader.Bans(ctx, page, pageSize)
}

func (service *HostSecurityService) Install(ctx context.Context, component string) error {
	parameters, _ := json.Marshal(installComponentParameters{Component: component})
	return service.client.Invoke(ctx, ActionInstallComponent, component, "component-v1", parameters)
}

func (service *HostSecurityService) Unban(ctx context.Context, jail, ip string) error {
	parameters, _ := json.Marshal(unbanParameters{Jail: jail, IP: ip})
	return service.client.Invoke(ctx, ActionFail2BanUnban, jail+":"+ip, "ban-v1", parameters)
}

func (service *HostSecurityService) EnableUFW(ctx context.Context, baseline []hostsecurity.FirewallRule) error {
	parameters, _ := json.Marshal(enableUFWParameters{Baseline: baseline})
	return service.client.Invoke(ctx, ActionUFWEnable, "ufw", rulesRevision(baseline, hostsecurity.UFWDefaults{}), parameters)
}

func (service *HostSecurityService) ApplyUFW(ctx context.Context, baseline, desired []hostsecurity.FirewallRule, baselineDefaults, desiredDefaults hostsecurity.UFWDefaults) error {
	parameters, _ := json.Marshal(applyUFWParameters{
		Baseline: baseline, Desired: desired, BaselineDefaults: baselineDefaults, DesiredDefaults: desiredDefaults,
	})
	return service.client.Invoke(ctx, ActionUFWApply, "ufw", rulesRevision(baseline, baselineDefaults), parameters)
}

func (service *HostSecurityService) AddWindowsFirewallRule(ctx context.Context, rule hostsecurity.FirewallRule) error {
	parameters, _ := json.Marshal(addWindowsFirewallParameters{Rule: rule})
	return service.client.Invoke(ctx, ActionWindowsFirewallAdd, rule.Name, "absent-v1", parameters)
}

func (service *HostSecurityService) SetWindowsFirewallRuleEnabled(ctx context.Context, id string, enabled bool) error {
	baseline, err := service.windowsRule(ctx, id)
	if err != nil {
		return err
	}
	parameters, _ := json.Marshal(setWindowsFirewallParameters{ID: id, Enabled: enabled, Baseline: baseline})
	return service.client.Invoke(ctx, ActionWindowsFirewallSet, id, ruleRevision(baseline), parameters)
}

func (service *HostSecurityService) DeleteWindowsFirewallRule(ctx context.Context, id string) error {
	baseline, err := service.windowsRule(ctx, id)
	if err != nil {
		return err
	}
	parameters, _ := json.Marshal(deleteWindowsFirewallParameters{ID: id, Baseline: baseline})
	return service.client.Invoke(ctx, ActionWindowsFirewallDelete, id, ruleRevision(baseline), parameters)
}

func (service *HostSecurityService) windowsRule(ctx context.Context, id string) (hostsecurity.FirewallRule, error) {
	for _, rule := range service.reader.Capabilities(ctx).Rules {
		if rule.ID == id {
			return rule, nil
		}
	}
	return hostsecurity.FirewallRule{}, hostsecurity.ErrInvalidRule
}

type HostSecurityExecutor struct{ service hostsecurity.Service }

func NewHostSecurityExecutor(service hostsecurity.Service) (*HostSecurityExecutor, error) {
	if service == nil {
		return nil, errors.New("direct host security service is required")
	}
	return &HostSecurityExecutor{service: service}, nil
}

func (executor *HostSecurityExecutor) Execute(ctx context.Context, request ExecutionRequest) error {
	switch request.Action {
	case ActionInstallComponent:
		var parameters installComponentParameters
		if err := decodeParameters(request.Parameters, &parameters); err != nil {
			return err
		}
		if request.Resource != parameters.Component || request.Revision != "component-v1" {
			return errors.New("privileged component binding does not match parameters")
		}
		return executor.service.Install(ctx, parameters.Component)
	case ActionFail2BanUnban:
		var parameters unbanParameters
		if err := decodeParameters(request.Parameters, &parameters); err != nil {
			return err
		}
		if request.Resource != parameters.Jail+":"+parameters.IP || request.Revision != "ban-v1" {
			return errors.New("privileged unban binding does not match parameters")
		}
		return executor.service.Unban(ctx, parameters.Jail, parameters.IP)
	case ActionUFWEnable:
		var parameters enableUFWParameters
		if err := decodeParameters(request.Parameters, &parameters); err != nil {
			return err
		}
		if request.Resource != "ufw" || request.Revision != rulesRevision(parameters.Baseline, hostsecurity.UFWDefaults{}) {
			return errors.New("privileged UFW binding does not match parameters")
		}
		current := executor.service.Capabilities(ctx)
		if rulesRevision(current.Rules, hostsecurity.UFWDefaults{}) != rulesRevision(parameters.Baseline, hostsecurity.UFWDefaults{}) {
			return hostsecurity.ErrFirewallConflict
		}
		return executor.service.EnableUFW(ctx, parameters.Baseline)
	case ActionUFWApply:
		var parameters applyUFWParameters
		if err := decodeParameters(request.Parameters, &parameters); err != nil {
			return err
		}
		if request.Resource != "ufw" || request.Revision != rulesRevision(parameters.Baseline, parameters.BaselineDefaults) {
			return errors.New("privileged UFW binding does not match parameters")
		}
		return executor.service.ApplyUFW(ctx, parameters.Baseline, parameters.Desired, parameters.BaselineDefaults, parameters.DesiredDefaults)
	case ActionWindowsFirewallAdd:
		var parameters addWindowsFirewallParameters
		if err := decodeParameters(request.Parameters, &parameters); err != nil {
			return err
		}
		if request.Resource != parameters.Rule.Name || request.Revision != "absent-v1" {
			return errors.New("privileged Windows firewall binding does not match parameters")
		}
		for _, current := range executor.service.Capabilities(ctx).Rules {
			if strings.EqualFold(current.Name, parameters.Rule.Name) {
				return hostsecurity.ErrFirewallConflict
			}
		}
		return executor.service.AddWindowsFirewallRule(ctx, parameters.Rule)
	case ActionWindowsFirewallSet:
		var parameters setWindowsFirewallParameters
		if err := decodeParameters(request.Parameters, &parameters); err != nil {
			return err
		}
		if request.Resource != parameters.ID || request.Revision != ruleRevision(parameters.Baseline) {
			return errors.New("privileged Windows firewall binding does not match parameters")
		}
		if err := executor.verifyWindowsBaseline(ctx, parameters.ID, parameters.Baseline); err != nil {
			return err
		}
		return executor.service.SetWindowsFirewallRuleEnabled(ctx, parameters.ID, parameters.Enabled)
	case ActionWindowsFirewallDelete:
		var parameters deleteWindowsFirewallParameters
		if err := decodeParameters(request.Parameters, &parameters); err != nil {
			return err
		}
		if request.Resource != parameters.ID || request.Revision != ruleRevision(parameters.Baseline) {
			return errors.New("privileged Windows firewall binding does not match parameters")
		}
		if err := executor.verifyWindowsBaseline(ctx, parameters.ID, parameters.Baseline); err != nil {
			return err
		}
		return executor.service.DeleteWindowsFirewallRule(ctx, parameters.ID)
	default:
		return errors.New("privileged host security action is not registered")
	}
}

func (executor *HostSecurityExecutor) verifyWindowsBaseline(ctx context.Context, id string, baseline hostsecurity.FirewallRule) error {
	for _, current := range executor.service.Capabilities(ctx).Rules {
		if current.ID == id {
			if current != baseline {
				return hostsecurity.ErrFirewallConflict
			}
			return nil
		}
	}
	return hostsecurity.ErrFirewallConflict
}

type installComponentParameters struct {
	Component string `json:"component"`
}

type unbanParameters struct {
	Jail string `json:"jail"`
	IP   string `json:"ip"`
}

type enableUFWParameters struct {
	Baseline []hostsecurity.FirewallRule `json:"baseline"`
}

type applyUFWParameters struct {
	Baseline         []hostsecurity.FirewallRule `json:"baseline"`
	Desired          []hostsecurity.FirewallRule `json:"desired"`
	BaselineDefaults hostsecurity.UFWDefaults    `json:"baseline_defaults"`
	DesiredDefaults  hostsecurity.UFWDefaults    `json:"desired_defaults"`
}

type addWindowsFirewallParameters struct {
	Rule hostsecurity.FirewallRule `json:"rule"`
}

type setWindowsFirewallParameters struct {
	ID       string                    `json:"id"`
	Enabled  bool                      `json:"enabled"`
	Baseline hostsecurity.FirewallRule `json:"baseline"`
}

type deleteWindowsFirewallParameters struct {
	ID       string                    `json:"id"`
	Baseline hostsecurity.FirewallRule `json:"baseline"`
}

func decodeParameters(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode privileged host security parameters: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("privileged host security parameters have trailing data")
	}
	return nil
}

func ruleRevision(rule hostsecurity.FirewallRule) string {
	body, _ := json.Marshal(rule)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func rulesRevision(rules []hostsecurity.FirewallRule, defaults hostsecurity.UFWDefaults) string {
	body, _ := json.Marshal(struct {
		Rules    []hostsecurity.FirewallRule `json:"rules"`
		Defaults hostsecurity.UFWDefaults    `json:"defaults"`
	}{rules, defaults})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
