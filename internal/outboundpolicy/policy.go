package outboundpolicy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type Policy struct {
	AllowPrivate bool
	AllowAnyPort bool
	Resolver     Resolver
}

func (policy Policy) Transport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = policy.DialContext
	return transport
}

func (policy Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse outbound address: %w", err)
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 {
		return nil, errors.New("invalid outbound port")
	}
	if !policy.AllowAnyPort && port != 80 && port != 443 {
		return nil, fmt.Errorf("outbound port %d is not allowed", port)
	}

	addresses, err := policy.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, candidate := range addresses {
		if !policy.AllowsAddress(candidate) {
			return nil, fmt.Errorf("outbound address %s is not allowed", candidate)
		}
	}

	dialer := &net.Dialer{}
	var failures []error
	for _, candidate := range addresses {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), rawPort))
		if dialErr == nil {
			return connection, nil
		}
		failures = append(failures, dialErr)
	}
	return nil, fmt.Errorf("connect to validated outbound address: %w", errors.Join(failures...))
}

func (policy Policy) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{address.Unmap()}, nil
	}
	resolver := policy.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve outbound host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("outbound host resolved to no addresses")
	}
	for index := range addresses {
		addresses[index] = addresses[index].Unmap()
	}
	return addresses, nil
}

func (policy Policy) AllowsAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || isAlwaysBlocked(address) {
		return false
	}
	if address.IsLoopback() || address.IsPrivate() {
		return policy.AllowPrivate && !isMetadataAddress(address)
	}
	return address.IsGlobalUnicast() && !isReservedAddress(address)
}

func isMetadataAddress(address netip.Addr) bool {
	return netip.MustParsePrefix("169.254.169.254/32").Contains(address) ||
		netip.MustParsePrefix("fd00:ec2::254/128").Contains(address)
}

func isAlwaysBlocked(address netip.Addr) bool {
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
	} {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func isReservedAddress(address netip.Addr) bool {
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("2001:db8::/32"),
	} {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
