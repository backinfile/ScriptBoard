package outboundpolicy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestExternalPolicyRejectsNonPublicDestinations(t *testing.T) {
	t.Parallel()

	policy := Policy{}
	for _, raw := range []string{
		"127.0.0.1", "10.0.0.1", "169.254.169.254", "0.0.0.0",
		"::1", "fd00:ec2::254", "fe80::1", "2001:db8::1",
	} {
		address := netip.MustParseAddr(raw)
		if policy.AllowsAddress(address) {
			t.Errorf("external policy allowed %s", address)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		address := netip.MustParseAddr(raw)
		if !policy.AllowsAddress(address) {
			t.Errorf("external policy rejected %s", address)
		}
	}
}

func TestInternalProbePolicyStillRejectsLinkLocalAndMetadataDestinations(t *testing.T) {
	t.Parallel()

	policy := Policy{AllowPrivate: true}
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "::1", "fd00::1"} {
		if !policy.AllowsAddress(netip.MustParseAddr(raw)) {
			t.Errorf("internal policy rejected %s", raw)
		}
	}
	for _, raw := range []string{"169.254.169.254", "fe80::1", "fd00:ec2::254"} {
		if policy.AllowsAddress(netip.MustParseAddr(raw)) {
			t.Errorf("internal policy allowed metadata or link-local destination %s", raw)
		}
	}
}

func TestTransportDoesNotUseEnvironmentProxyAndCanExplicitlyReachLocalProbe(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	policy := Policy{AllowPrivate: true, AllowAnyPort: true}
	client := &http.Client{Transport: policy.Transport()}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", response.StatusCode)
	}
	transport := policy.Transport()
	if transport.Proxy != nil {
		t.Fatal("outbound transport inherited an environment proxy")
	}
}

func TestExternalDialRejectsNonStandardPortsBeforeConnecting(t *testing.T) {
	t.Parallel()

	policy := Policy{Resolver: staticResolver{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}}}
	if _, err := policy.DialContext(context.Background(), "tcp", "example.com:8080"); err == nil {
		t.Fatal("external policy accepted a non-standard port")
	}
}

func FuzzPolicyAllowsAddress(f *testing.F) {
	for _, seed := range []string{"127.0.0.1", "169.254.169.254", "8.8.8.8", "::1", "fd00:ec2::254", "2606:4700:4700::1111", "not-an-ip"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		address, err := netip.ParseAddr(raw)
		if err != nil {
			return
		}
		external := Policy{}
		internal := Policy{AllowPrivate: true}
		if external.AllowsAddress(address) && !internal.AllowsAddress(address) {
			t.Fatalf("external policy is broader than internal policy for %s", address)
		}
	})
}

type staticResolver struct{ addresses []netip.Addr }

func (resolver staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return resolver.addresses, nil
}

var _ Resolver = staticResolver{}
