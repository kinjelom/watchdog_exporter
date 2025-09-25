package validator

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CertInfo captures basic facts about a certificate in the chain.
type CertInfo struct {
	Position        int       // 0 = leaf, then upward toward root
	SerialHex       string    // uppercase hex without leading 0x
	CommonName      string    // Subject CN (informational; SANs drive verification)
	NotAfter        time.Time // certificate expiry
	DaysLeft        float64   // days until expiry (can be negative if already expired)
	IsCA            bool      // whether BasicConstraints.CA is set
	IssuerCN        string    // Issuer CN for readability
	SubjectAltNames []string  // DNS names (subset for quick view)
}

// CertsReport is a summary of TLS facts for metrics and validation.
type CertsReport struct {
	HadTLS       bool
	ChainValid   bool       // true if VerifiedChains present (hostname & chain validated)
	Certificates []CertInfo // ordered leaf -> ... -> (possibly) root
}

// TLSChecker defines TLS-related validation and inspection.
type TLSChecker interface {
	TLSClientConfigWithSNI(serverName string) *tls.Config
	CheckHandshakeError(err error) (string, bool)
	Inspect(resp *http.Response) CertsReport
}

type DefaultTLSChecker struct {
	debug bool
}

func NewDefaultTLSChecker(debug bool) *DefaultTLSChecker { return &DefaultTLSChecker{debug: debug} }

func (d *DefaultTLSChecker) TLSClientConfigWithSNI(serverName string) *tls.Config {
	return &tls.Config{ServerName: serverName}
}

func (d *DefaultTLSChecker) CheckHandshakeError(err error) (string, bool) {
	var cErr x509.CertificateInvalidError
	if errors.As(err, &cErr) {
		if cErr.Reason == x509.Expired {
			return "expired-cert-leaf", true
		}
		return "invalid-tls-certificate", true
	}

	var uaErr x509.UnknownAuthorityError
	if errors.As(err, &uaErr) {
		return "invalid-tls-chain", true
	}

	var hnErr x509.HostnameError
	if errors.As(err, &hnErr) {
		return "invalid-tls-hostname", true
	}

	// Many TLS problems are wrapped under *url.Error during handshake.
	var uErr *url.Error
	if errors.As(err, &uErr) {
		// Only treat as TLS-related if it wraps a known TLS/x509 error.
		if isTLSError(uErr.Err) {
			return "invalid-tls-chain", true
		}
	}

	return "", false
}

func (d *DefaultTLSChecker) Inspect(resp *http.Response) CertsReport {
	cs := resp.TLS
	if cs == nil {
		// Plain HTTP
		return CertsReport{HadTLS: false}
	}
	rep := CertsReport{
		HadTLS: true,
	}
	now := time.Now()

	// VerifiedChains present => chain is valid and hostname verified.
	rep.ChainValid = len(cs.VerifiedChains) > 0

	// Choose the best available chain to report:
	// Prefer the first VerifiedChain (what Go actually validated), else fall back to PeerCertificates.
	var chosen []*x509.Certificate
	if len(cs.VerifiedChains) > 0 && len(cs.VerifiedChains[0]) > 0 {
		chosen = cs.VerifiedChains[0]
	} else if len(cs.PeerCertificates) > 0 {
		chosen = cs.PeerCertificates
	}

	// Build detailed CertInfo list leaf -> ... -> issuer/root (as provided).
	rep.Certificates = make([]CertInfo, 0, len(chosen))
	for i, c := range chosen {
		days := c.NotAfter.Sub(now).Hours() / 24.0
		ci := CertInfo{
			Position:        i,
			SerialHex:       bigIntToUpperHex(c.SerialNumber),
			CommonName:      c.Subject.CommonName,
			NotAfter:        c.NotAfter,
			DaysLeft:        days,
			IsCA:            c.IsCA,
			IssuerCN:        c.Issuer.CommonName,
			SubjectAltNames: c.DNSNames,
		}
		rep.Certificates = append(rep.Certificates, ci)
	}

	return rep
}

// bigIntToUpperHex converts a *big.Int to an uppercase hex string (no 0x prefix).
func bigIntToUpperHex(b *big.Int) string {
	if b == nil {
		return ""
	}
	h := b.Bytes()
	if len(h) == 0 {
		return "0"
	}
	return strings.ToUpper(hex.EncodeToString(h))
}

// helper function to detect TLS-related underlying errors
func isTLSError(err error) bool {
	switch {
	case errors.As(err, new(x509.CertificateInvalidError)),
		errors.As(err, new(x509.UnknownAuthorityError)),
		errors.As(err, new(x509.HostnameError)),
		strings.Contains(strings.ToLower(err.Error()), "tls"),
		strings.Contains(strings.ToLower(err.Error()), "certificate"),
		strings.Contains(strings.ToLower(err.Error()), "handshake"):
		return true
	default:
		return false
	}
}
