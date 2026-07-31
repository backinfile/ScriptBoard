package localtls

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
)

// PinnedConfig builds a client configuration that trusts only the configured
// server leaf certificate. It is intended for local health probes that may
// connect through a loopback address not present in the certificate SANs.
func PinnedConfig(certificatePath, preferredServerName string) (*tls.Config, error) {
	raw, err := os.ReadFile(certificatePath)
	if err != nil {
		return nil, fmt.Errorf("read probe certificate: %w", err)
	}
	var block *pem.Block
	for len(raw) != 0 {
		block, raw = pem.Decode(raw)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			break
		}
	}
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("probe certificate file contains no certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse probe certificate: %w", err)
	}
	serverName, err := verifiedServerName(certificate, preferredServerName)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: serverName,
	}, nil
}

func verifiedServerName(certificate *x509.Certificate, preferred string) (string, error) {
	preferred = strings.Trim(preferred, "[]")
	if preferred != "" && preferred != "0.0.0.0" && preferred != "::" {
		if certificate.VerifyHostname(preferred) == nil {
			return preferred, nil
		}
	}
	for _, address := range certificate.IPAddresses {
		name := address.String()
		if certificate.VerifyHostname(name) == nil {
			return name, nil
		}
	}
	for _, name := range certificate.DNSNames {
		candidate := name
		if strings.HasPrefix(candidate, "*.") {
			candidate = "scriptboard-probe" + candidate[1:]
		}
		if net.ParseIP(candidate) == nil && certificate.VerifyHostname(candidate) == nil {
			return candidate, nil
		}
	}
	return "", errors.New("probe certificate has no usable DNS or IP subject alternative name")
}
