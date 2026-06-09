package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"sync"
	"time"

	"github.com/guregu/null/v5"
)

var (
	systemCertPoolOnce sync.Once
	systemCertPool     *x509.CertPool
	systemCertPoolErr  error
	systemCertPoolLoad = x509.SystemCertPool
)

func (c *Checker) probeHTTP(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	httpConfig := monitor.EffectiveHTTP()
	requestStart := time.Now()
	checkerTracer := NewCheckerTracer()
	traceCtx := httptrace.WithClientTrace(ctx, checkerTracer.GetClientTrace())

	submission := CheckerSubmissionRequest{
		MonitorID: monitor.ID,
		ProbeType: string(monitor.EffectiveType()),
		Timestamp: time.Now().UTC(),
	}

	request, err := http.NewRequestWithContext(traceCtx, httpConfig.Method, httpConfig.URL, nil)
	if err != nil {
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}
	for key, value := range httpConfig.Headers {
		request.Header.Set(key, value)
	}

	tlsConfig, err := httpConfig.NewTLSConfig()
	if err != nil {
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}

	timeout := monitor.EffectiveTimeout(30 * time.Second)
	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       tlsConfig,
		},
		Timeout: timeout,
	}

	response, err := httpClient.Do(request)
	submission.LatencyMs = time.Since(requestStart).Milliseconds()
	submission.Timings = checkerTracer.GetTimings()

	if response != nil && response.TLS != nil {
		submission.TlsVersion = null.StringFrom(tls.VersionName(response.TLS.Version))
		submission.TlsCipher = null.StringFrom(tls.CipherSuiteName(response.TLS.CipherSuite))
		if len(response.TLS.PeerCertificates) > 0 && response.TLS.PeerCertificates[0] != nil {
			submission.TlsExpiry = null.TimeFrom(response.TLS.PeerCertificates[0].NotAfter)
			submission.TlsCertInfo = buildTLSCertInfoFromConnectionState(response.TLS)
		}
	} else {
		if tlsState := checkerTracer.GetTLSConnectionState(); tlsState != nil {
			submission.TlsVersion = null.StringFrom(tls.VersionName(tlsState.Version))
			submission.TlsCipher = null.StringFrom(tls.CipherSuiteName(tlsState.CipherSuite))
			if len(tlsState.PeerCertificates) > 0 && tlsState.PeerCertificates[0] != nil {
				submission.TlsExpiry = null.TimeFrom(tlsState.PeerCertificates[0].NotAfter)
				submission.TlsCertInfo = buildTLSCertInfoFromConnectionState(tlsState)
			}
		} else if tlsErr := checkerTracer.GetTLSError(); tlsErr != nil {
			if certInfo := buildTLSCertInfoFromError(tlsErr); certInfo.IsExpired || !certInfo.NotBefore.IsZero() {
				if submission.TlsExpiry.IsZero() && certInfo.NotAfter.Valid {
					submission.TlsExpiry = null.TimeFrom(certInfo.NotAfter.Time)
				}
				if submission.TlsVersion.IsZero() && certInfo.Version.Valid {
					submission.TlsVersion = null.StringFrom(certInfo.Version.String)
				}
				if submission.TlsCipher.IsZero() && certInfo.Cipher.Valid {
					submission.TlsCipher = null.StringFrom(certInfo.Cipher.String)
				}
				submission.TlsCertInfo = certInfo
			}
		}
	}

	if err != nil {
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}
	defer func() {
		if response.Body != nil {
			_ = response.Body.Close()
		}
	}()

	submission.StatusCode = response.StatusCode
	submission.Success = monitor.IsSuccessfulStatus(response.StatusCode, false)

	if response.ContentLength > 0 && response.Body != nil {
		bodyBytes, err := io.ReadAll(response.Body)
		if err == nil {
			submission.ResponseBody = null.StringFrom(string(bodyBytes))
		}
	}

	if !submission.Success {
		submission.FailureReason = null.StringFrom(fmt.Sprintf("received unexpected status code %d", response.StatusCode))
	}

	return submission
}

// NewTLSConfig reloads certificate files for each probe so rotated mTLS assets
// are picked up without restarting the checker process.
func (c MonitorHTTPConfig) NewTLSConfig() (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: c.SkipTLSVerifyValue(),
	}

	if c.CACertPath != "" {
		caCertPEM, err := os.ReadFile(c.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("load http CA certificate %q: %w", c.CACertPath, err)
		}

		rootCAs, err := cachedSystemCertPool()
		if err != nil {
			rootCAs = x509.NewCertPool()
		}
		if ok := rootCAs.AppendCertsFromPEM(caCertPEM); !ok {
			return nil, fmt.Errorf("parse http CA certificate %q: no certificates found", c.CACertPath)
		}
		tlsConfig.RootCAs = rootCAs
	}

	if c.ClientCertPath != "" {
		clientCertificate, err := c.loadClientCertificate()
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{clientCertificate}
	}

	return tlsConfig, nil
}

func cachedSystemCertPool() (*x509.CertPool, error) {
	systemCertPoolOnce.Do(func() {
		systemCertPool, systemCertPoolErr = systemCertPoolLoad()
		if systemCertPoolErr == nil && systemCertPool == nil {
			systemCertPool = x509.NewCertPool()
		}
	})
	if systemCertPoolErr != nil {
		return nil, systemCertPoolErr
	}
	return systemCertPool.Clone(), nil
}

func (c MonitorHTTPConfig) loadClientCertificate() (tls.Certificate, error) {
	if c.ClientKeyPassword == "" {
		certificate, err := tls.LoadX509KeyPair(c.ClientCertPath, c.ClientKeyPath)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("load http client certificate %q and key %q: %w", c.ClientCertPath, c.ClientKeyPath, err)
		}
		return certificate, nil
	}

	certPEM, err := os.ReadFile(c.ClientCertPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load http client certificate %q: %w", c.ClientCertPath, err)
	}
	keyPEM, err := os.ReadFile(c.ClientKeyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load http client key %q: %w", c.ClientKeyPath, err)
	}

	if certificate, err := tls.X509KeyPair(certPEM, keyPEM); err == nil {
		return certificate, nil
	} else if !pemContainsEncryptedBlock(keyPEM) {
		return tls.Certificate{}, fmt.Errorf("load http client certificate %q and key %q: %w", c.ClientCertPath, c.ClientKeyPath, err)
	}

	decryptedKeyPEM, err := decryptPEMPrivateKey(keyPEM, c.ClientKeyPassword)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("decrypt http client key %q: %w", c.ClientKeyPath, err)
	}

	certificate, err := tls.X509KeyPair(certPEM, decryptedKeyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load decrypted http client certificate %q and key %q: %w", c.ClientCertPath, c.ClientKeyPath, err)
	}
	return certificate, nil
}

func decryptPEMPrivateKey(keyPEM []byte, password string) ([]byte, error) {
	var decryptedPEM []byte
	remaining := keyPEM
	decodedAny := false

	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			break
		}
		decodedAny = true

		if x509.IsEncryptedPEMBlock(block) {
			der, err := x509.DecryptPEMBlock(block, []byte(password))
			if err != nil {
				return nil, err
			}
			block = &pem.Block{Type: block.Type, Bytes: der}
		}

		decryptedPEM = append(decryptedPEM, pem.EncodeToMemory(block)...)
		remaining = rest
	}

	if !decodedAny {
		return nil, fmt.Errorf("invalid PEM data")
	}

	return decryptedPEM, nil
}

func pemContainsEncryptedBlock(pemBytes []byte) bool {
	remaining := pemBytes
	for len(remaining) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil {
			return false
		}
		if x509.IsEncryptedPEMBlock(block) {
			return true
		}
		remaining = rest
	}
	return false
}

func buildTLSCertInfoFromConnectionState(tlsState *tls.ConnectionState) TLSCertificateInfo {
	if tlsState == nil || len(tlsState.PeerCertificates) == 0 {
		return TLSCertificateInfo{}
	}

	cert := tlsState.PeerCertificates[0]
	fingerprint := ""
	if len(cert.RawSubject) > 0 {
		fingerprint = fmt.Sprintf("%x", cert.RawSubject)
	}

	notAfter := cert.NotAfter.UTC()
	isExpired := time.Now().UTC().After(notAfter)

	return TLSCertificateInfo{
		Version:     null.StringFrom(tls.VersionName(tlsState.Version)),
		Cipher:      null.StringFrom(tls.CipherSuiteName(tlsState.CipherSuite)),
		NotBefore:   null.TimeFrom(cert.NotBefore.UTC()),
		NotAfter:    null.TimeFrom(notAfter),
		Issuer:      null.StringFrom(cert.Issuer.String()),
		Subject:     null.StringFrom(cert.Subject.String()),
		DN:          null.StringFrom(cert.Subject.CommonName),
		Fingerprint: null.StringFrom(fingerprint),
		IsExpired:   isExpired,
	}
}

func buildTLSCertInfoFromError(err error) TLSCertificateInfo {
	if err == nil {
		return TLSCertificateInfo{}
	}

	errStr := err.Error()
	pemBlock, _ := pem.Decode([]byte(errStr))
	if pemBlock == nil {
		return TLSCertificateInfo{}
	}

	cert, parseErr := x509.ParseCertificate(pemBlock.Bytes)
	if parseErr != nil {
		return TLSCertificateInfo{}
	}

	notAfter := cert.NotAfter.UTC()
	isExpired := time.Now().UTC().After(notAfter)
	fingerprint := ""
	if len(cert.RawSubject) > 0 {
		fingerprint = fmt.Sprintf("%x", cert.RawSubject)
	}

	return TLSCertificateInfo{
		NotBefore:   null.TimeFrom(cert.NotBefore.UTC()),
		NotAfter:    null.TimeFrom(notAfter),
		Issuer:      null.StringFrom(cert.Issuer.String()),
		Subject:     null.StringFrom(cert.Subject.String()),
		DN:          null.StringFrom(cert.Subject.CommonName),
		Fingerprint: null.StringFrom(fingerprint),
		IsExpired:   isExpired,
	}
}
