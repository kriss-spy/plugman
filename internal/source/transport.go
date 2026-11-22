package source

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// Metadata GETs are safe to repeat before any Vault mutation. Keep attempts
// bounded, preserve the client's deadline, and never retry permanent failures.
func getMetadata(client HTTPDoer, request *http.Request, operation string) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		response, err := client.Do(request.Clone(request.Context()))
		if err == nil {
			return response, nil
		}
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		if attempt == 3 || !transientTransportError(err) {
			return nil, &Error{
				Code: ErrorTransportFailure, Operation: operation, URL: request.URL.String(), Err: err,
				Message: fmt.Sprintf("could not reach release service at %s: %s (%d attempt(s)); check network and proxy settings", request.URL.Hostname(), transportCause(err), attempt),
			}
		}
		timer := time.NewTimer(time.Duration(attempt) * 100 * time.Millisecond)
		select {
		case <-request.Context().Done():
			timer.Stop()
			return nil, request.Context().Err()
		case <-timer.C:
		}
	}
}

func transientTransportError(err error) bool {
	var network net.Error
	return (errors.As(err, &network) && network.Timeout()) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED)
}

// Use fixed descriptions rather than raw error strings: proxy URLs and signed
// asset redirect URLs can contain credentials that must not reach diagnostics.
func transportCause(err error) string {
	var network net.Error
	var dns *net.DNSError
	var unknownAuthority x509.UnknownAuthorityError
	var invalidCertificate x509.CertificateInvalidError
	var hostname x509.HostnameError
	var requestError *url.Error
	switch {
	case errors.As(err, &network) && network.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return "request timed out"
	case errors.As(err, &dns):
		return "DNS lookup failed"
	case errors.As(err, &unknownAuthority), errors.As(err, &invalidCertificate), errors.As(err, &hostname):
		return "TLS certificate validation failed"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET):
		return "connection closed before a response arrived"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.As(err, &requestError):
		return "request failed"
	default:
		return "connection failed"
	}
}
