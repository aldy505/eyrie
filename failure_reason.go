package main

import (
	"strings"
)

const (
	FailureReasonNetworkUnreachable = "network_unreachable"
	FailureReasonConnectionRefused  = "connection_refused"
	FailureReasonTimeout            = "timeout"
	FailureReasonTLSError           = "tls_error"
	FailureReasonAuthError          = "auth_error"
	FailureReasonDNSError           = "dns_error"
	FailureReasonHTTPError          = "http_error"
	FailureReasonGeneric            = "generic_error"
)

// CategorizeFailureReason maps a raw error string to a semantic failure category.
func CategorizeFailureReason(reason string) string {
	if reason == "" {
		return FailureReasonGeneric
	}

	lowerReason := strings.ToLower(reason)

	// Network unreachable errors
	if strings.Contains(lowerReason, "unreachable") ||
		strings.Contains(lowerReason, "no route to host") ||
		strings.Contains(lowerReason, "network is unreachable") ||
		strings.Contains(lowerReason, "host unreachable") {
		return FailureReasonNetworkUnreachable
	}

	// Connection refused/reset
	if strings.Contains(lowerReason, "connection refused") ||
		strings.Contains(lowerReason, "econnrefused") ||
		strings.Contains(lowerReason, "connection reset") ||
		strings.Contains(lowerReason, "port not open") {
		return FailureReasonConnectionRefused
	}

	// Timeout errors
	if strings.Contains(lowerReason, "timeout") ||
		strings.Contains(lowerReason, "context deadline exceeded") ||
		strings.Contains(lowerReason, "dial timeout") ||
		strings.Contains(lowerReason, "read timeout") ||
		strings.Contains(lowerReason, "write timeout") ||
		strings.Contains(lowerReason, "i/o timeout") {
		return FailureReasonTimeout
	}

	// TLS/Certificate errors
	if strings.Contains(lowerReason, "tls") ||
		strings.Contains(lowerReason, "certificate") ||
		strings.Contains(lowerReason, "cert") ||
		strings.Contains(lowerReason, "ssl") ||
		strings.Contains(lowerReason, "expired") ||
		strings.Contains(lowerReason, "x509") ||
		strings.Contains(lowerReason, "handshake") {
		return FailureReasonTLSError
	}

	// Authentication/Authorization errors
	if strings.Contains(lowerReason, "invalid credentials") ||
		strings.Contains(lowerReason, "auth") ||
		strings.Contains(lowerReason, "permission denied") ||
		strings.Contains(lowerReason, "unauthorized") ||
		strings.Contains(lowerReason, "access denied") ||
		strings.Contains(lowerReason, "forbidden") ||
		strings.Contains(lowerReason, "401") ||
		strings.Contains(lowerReason, "403") {
		return FailureReasonAuthError
	}

	// DNS errors
	if strings.Contains(lowerReason, "dns") ||
		strings.Contains(lowerReason, "lookup") ||
		strings.Contains(lowerReason, "name resolution") ||
		strings.Contains(lowerReason, "getaddrinfo") {
		return FailureReasonDNSError
	}

	// HTTP-specific errors (status codes)
	if strings.Contains(lowerReason, "http") ||
		strings.Contains(lowerReason, "status code") ||
		strings.Contains(lowerReason, "4xx") ||
		strings.Contains(lowerReason, "5xx") {
		return FailureReasonHTTPError
	}

	// Generic fallback
	return FailureReasonGeneric
}

// FriendlyFailureReasonName returns a human-readable name for a failure reason category.
func FriendlyFailureReasonName(category string) string {
	switch category {
	case FailureReasonNetworkUnreachable:
		return "Network Unreachable"
	case FailureReasonConnectionRefused:
		return "Connection Refused"
	case FailureReasonTimeout:
		return "Timeout"
	case FailureReasonTLSError:
		return "TLS/Certificate Error"
	case FailureReasonAuthError:
		return "Authentication Error"
	case FailureReasonDNSError:
		return "DNS Error"
	case FailureReasonHTTPError:
		return "HTTP Error"
	case FailureReasonGeneric:
		return "Generic Error"
	default:
		return "Unknown Error"
	}
}
