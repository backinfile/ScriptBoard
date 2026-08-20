package privilegebroker

import (
	"context"
	"net"
	"testing"
	"time"

	"scriptboard/internal/hostsecurity"
	"scriptboard/internal/logstream"
	"scriptboard/internal/servicelogs"
)

func TestRemoteHostSecurityReadsEverySnapshotThroughBroker(t *testing.T) {
	host := &fixtureHostSecurity{
		capabilities: hostsecurity.Capabilities{OS: "linux", Hostname: "root-visible-host", UFWEnabled: true},
		updateReport: hostsecurity.SecurityUpdateReport{Supported: true, Provider: "root-updates"},
		logins:       hostsecurity.LoginPage{Total: 7, Page: 2, Pages: 4},
		bans:         hostsecurity.BanPage{Total: 3, Page: 1, Pages: 1},
	}
	server, client := observabilityFixture(t, host, &fixtureServiceLogs{})
	defer server.Close()
	service := NewRemoteHostSecurity(client)

	if capabilities := service.Capabilities(context.Background()); capabilities.Hostname != "root-visible-host" || !capabilities.UFWEnabled {
		t.Fatalf("capabilities=%#v", capabilities)
	}
	if report, err := service.SecurityUpdates(context.Background(), true); err != nil || report.Provider != "root-updates" {
		t.Fatalf("updates=%#v err=%v", report, err)
	}
	if page, err := service.Logins(context.Background(), hostsecurity.LoginQuery{Range: "7d", Page: 2, PageSize: 20, Refresh: true}); err != nil || page.Total != 7 {
		t.Fatalf("logins=%#v err=%v", page, err)
	}
	if page, err := service.Bans(context.Background(), 1, 20); err != nil || page.Total != 3 {
		t.Fatalf("bans=%#v err=%v", page, err)
	}
	if host.capabilityReads != 1 || host.updateReads != 1 || host.loginReads != 1 || host.banReads != 1 {
		t.Fatalf("reads capabilities=%d updates=%d logins=%d bans=%d", host.capabilityReads, host.updateReads, host.loginReads, host.banReads)
	}
}

func TestRemoteServiceLogsPreservesBoundedQueryThroughBroker(t *testing.T) {
	logs := &fixtureServiceLogs{report: servicelogs.Report{Supported: true, Provider: "root-journal", Entries: []servicelogs.Entry{{Service: "broker", Severity: logstream.SeverityError, Message: "fixture"}}}}
	server, client := observabilityFixture(t, &fixtureHostSecurity{}, logs)
	defer server.Close()
	reader := NewRemoteServiceLogs(client)
	query := servicelogs.Query{Service: "broker", Range: "7d", Severity: logstream.SeverityError, Search: "failed"}
	report, err := reader.List(context.Background(), query)
	if err != nil || report.Provider != "root-journal" || len(report.Entries) != 1 {
		t.Fatalf("report=%#v err=%v", report, err)
	}
	if logs.calls != 1 || logs.query != query {
		t.Fatalf("calls=%d query=%#v", logs.calls, logs.query)
	}
}

func TestObservabilityProtocolRejectsCrossDomainAndUnboundedQueries(t *testing.T) {
	valid := wireRequest{Version: ProtocolVersion, Operation: operationServiceLogsList, RequestID: "observability-validation", ServiceLogs: &serviceLogsWireRequest{Query: servicelogs.Query{Range: "24h"}}}
	if err := validateWireRequest(valid); err != nil {
		t.Fatalf("valid service log request rejected: %v", err)
	}
	cases := []wireRequest{
		func() wireRequest { value := valid; value.HostSecurity = &hostSecurityWireRequest{}; return value }(),
		func() wireRequest { value := valid; value.ServiceLogs.Query.Search = string([]byte{0}); return value }(),
		{Version: ProtocolVersion, Operation: "unknown_observability", RequestID: "observability-validation", HostSecurity: &hostSecurityWireRequest{}},
		{Version: ProtocolVersion, Operation: operationHostSecurityBans, RequestID: "observability-validation", HostSecurity: &hostSecurityWireRequest{BanPage: 1, BanLimit: 1000}},
	}
	for index, request := range cases {
		if err := validateWireRequest(request); err == nil {
			t.Fatalf("invalid observability request %d was accepted", index)
		}
	}
}

func observabilityFixture(t *testing.T, host hostsecurity.Service, logs servicelogs.Reader) (*Server, *Client) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{
		Listener: listener, VerifyPeer: func(net.Conn) error { return nil },
		Authorizer: &fixtureAuthorizer{}, Executor: &fixtureExecutor{}, HostSecurity: host, ServiceLogs: logs, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	client := NewClient(ClientOptions{Dial: func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}})
	return server, client
}

type fixtureServiceLogs struct {
	report servicelogs.Report
	query  servicelogs.Query
	calls  int
}

func (fixture *fixtureServiceLogs) List(_ context.Context, query servicelogs.Query) (servicelogs.Report, error) {
	fixture.calls++
	fixture.query = query
	return fixture.report, nil
}
