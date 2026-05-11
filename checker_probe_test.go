package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guregu/null/v5"
)

type fakeSQLDB struct {
	pingErr error
}

func (f fakeSQLDB) PingContext(context.Context) error {
	return f.pingErr
}

func (f fakeSQLDB) Close() error {
	return nil
}

func TestProbeHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	submission := checker.probeHTTP(t.Context(), Monitor{
		ID:   "http-monitor",
		Type: MonitorTypeHTTP,
		HTTP: &MonitorHTTPConfig{
			Method:              http.MethodPost,
			URL:                 server.URL,
			ExpectedStatusCodes: []int{http.StatusAccepted},
		},
	})

	if !submission.Success {
		t.Fatalf("expected http probe success, got failure reason %q", submission.FailureReason.String)
	}
	if submission.StatusCode != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, submission.StatusCode)
	}
	if !strings.Contains(submission.ResponseBody.String, `"ok":true`) {
		t.Fatalf("expected response body to be captured, got %q", submission.ResponseBody.String)
	}
}

func TestProbeHTTPUnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	submission := checker.probeHTTP(t.Context(), Monitor{
		ID:   "http-monitor",
		Type: MonitorTypeHTTP,
		HTTP: &MonitorHTTPConfig{
			Method:              http.MethodGet,
			URL:                 server.URL,
			ExpectedStatusCodes: []int{http.StatusOK},
		},
	})

	if submission.Success {
		t.Fatal("expected http probe to fail on unexpected status")
	}
	if !submission.FailureReason.Valid || !strings.Contains(submission.FailureReason.String, "unexpected status code 500") {
		t.Fatalf("expected unexpected status failure reason, got %q", submission.FailureReason.String)
	}
}

func TestProbeHTTPMutualTLS(t *testing.T) {
	caCertPEM, caCert, caKey := mustGenerateCertificateAuthority(t)
	serverCertPEM, serverKeyPEM := mustGenerateLeafCertificate(t, caCert, caKey, false, "127.0.0.1")
	clientCertPEM, clientKeyPEM := mustGenerateLeafCertificate(t, caCert, caKey, true, "eyrie-client")

	caCertPath := writeTempTLSFile(t, "ca-*.pem", caCertPEM)
	clientCertPath := writeTempTLSFile(t, "client-*.crt", clientCertPEM)
	clientKeyPath := writeTempTLSFile(t, "client-*.key", clientKeyPEM)

	serverCertificate, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("failed to load server certificate: %v", err)
	}

	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caCertPEM) {
		t.Fatal("failed to append CA certificate to client CA pool")
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			t.Fatal("expected peer certificate on mutual TLS request")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCertificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}
	server.StartTLS()
	defer server.Close()

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	submission := checker.probeHTTP(t.Context(), Monitor{
		ID:   "http-monitor",
		Type: MonitorTypeHTTP,
		HTTP: &MonitorHTTPConfig{
			Method:              http.MethodGet,
			URL:                 server.URL,
			CACertPath:          caCertPath,
			ClientCertPath:      clientCertPath,
			ClientKeyPath:       clientKeyPath,
			ExpectedStatusCodes: []int{http.StatusOK},
		},
	})

	if !submission.Success {
		t.Fatalf("expected http probe success, got failure reason %q", submission.FailureReason.String)
	}
}

func TestNewTLSConfigCachesSystemCertPool(t *testing.T) {
	oldOnce := systemCertPoolOnce
	oldPool := systemCertPool
	oldErr := systemCertPoolErr
	oldLoader := systemCertPoolLoad
	t.Cleanup(func() {
		systemCertPoolOnce = oldOnce
		systemCertPool = oldPool
		systemCertPoolErr = oldErr
		systemCertPoolLoad = oldLoader
	})

	systemCertPoolOnce = sync.Once{}
	systemCertPool = nil
	systemCertPoolErr = nil

	var calls int
	systemCertPoolLoad = func() (*x509.CertPool, error) {
		calls++
		return x509.NewCertPool(), nil
	}

	caCertPEM, _, _ := mustGenerateCertificateAuthority(t)
	caCertPath := writeTempTLSFile(t, "ca-*.pem", caCertPEM)

	firstConfig, err := (MonitorHTTPConfig{CACertPath: caCertPath}).NewTLSConfig()
	if err != nil {
		t.Fatalf("expected first TLS config to load, got %v", err)
	}
	secondConfig, err := (MonitorHTTPConfig{CACertPath: caCertPath}).NewTLSConfig()
	if err != nil {
		t.Fatalf("expected second TLS config to load, got %v", err)
	}

	if calls != 1 {
		t.Fatalf("expected system CA pool to load once, got %d calls", calls)
	}
	if firstConfig.RootCAs == nil || secondConfig.RootCAs == nil {
		t.Fatal("expected both TLS configs to include root CAs")
	}
	if firstConfig.RootCAs == secondConfig.RootCAs {
		t.Fatal("expected TLS configs to use cloned root CA pools")
	}
}

func TestProbeHTTPMutualTLSEncryptedPrivateKey(t *testing.T) {
	caCertPEM, caCert, caKey := mustGenerateCertificateAuthority(t)
	serverCertPEM, serverKeyPEM := mustGenerateLeafCertificate(t, caCert, caKey, false, "127.0.0.1")
	clientCertPEM, clientKeyPEM := mustGenerateLeafCertificate(t, caCert, caKey, true, "eyrie-client")

	encryptedClientKeyPEM := mustEncryptPEMBlock(t, clientKeyPEM, "hunter2")
	caCertPath := writeTempTLSFile(t, "ca-*.pem", caCertPEM)
	clientCertPath := writeTempTLSFile(t, "client-*.crt", clientCertPEM)
	clientKeyPath := writeTempTLSFile(t, "client-*.key", encryptedClientKeyPEM)

	serverCertificate, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("failed to load server certificate: %v", err)
	}

	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caCertPEM) {
		t.Fatal("failed to append CA certificate to client CA pool")
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCertificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
	}
	server.StartTLS()
	defer server.Close()

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	submission := checker.probeHTTP(t.Context(), Monitor{
		ID:   "http-monitor",
		Type: MonitorTypeHTTP,
		HTTP: &MonitorHTTPConfig{
			Method:              http.MethodGet,
			URL:                 server.URL,
			CACertPath:          caCertPath,
			ClientCertPath:      clientCertPath,
			ClientKeyPath:       clientKeyPath,
			ClientKeyPassword:   "hunter2",
			ExpectedStatusCodes: []int{http.StatusOK},
		},
	})

	if !submission.Success {
		t.Fatalf("expected http probe success, got failure reason %q", submission.FailureReason.String)
	}
}

func TestProbeTCP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create tcp listener: %v", err)
	}
	defer listener.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buffer := make([]byte, 16)
		n, err := conn.Read(buffer)
		if err != nil {
			return
		}
		if got := string(buffer[:n]); got != "PING\n" {
			t.Errorf("expected tcp payload %q, got %q", "PING\n", got)
		}
		_, _ = conn.Write([]byte("PONG\n"))
	}()

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	submission := checker.probeTCP(t.Context(), Monitor{
		ID:   "tcp-monitor",
		Type: MonitorTypeTCP,
		TCP: MonitorTCPConfig{
			Address:        listener.Addr().String(),
			Send:           null.StringFrom("PING\n"),
			ExpectContains: null.StringFrom("PONG"),
		},
	})
	wg.Wait()

	if !submission.Success {
		t.Fatalf("expected tcp probe success, got failure reason %q", submission.FailureReason.String)
	}
	if !strings.Contains(submission.ResponseBody.String, "PONG") {
		t.Fatalf("expected tcp response to be captured, got %q", submission.ResponseBody.String)
	}
}

func TestProbeRedis(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create redis listener: %v", err)
	}
	defer listener.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "*") {
				for i := 0; i < 2; i++ {
					if _, err := reader.ReadString('\n'); err != nil {
						return
					}
				}
				if _, err := conn.Write([]byte("+PONG\r\n")); err != nil {
					return
				}
				return
			}
		}
	}()

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	submission := checker.probeRedis(t.Context(), Monitor{
		ID:   "redis-monitor",
		Type: MonitorTypeRedis,
		Redis: MonitorRedisConfig{
			Address: listener.Addr().String(),
		},
	})
	wg.Wait()

	if !submission.Success {
		t.Fatalf("expected redis probe success, got failure reason %q", submission.FailureReason.String)
	}
}

func TestProbeICMP(t *testing.T) {
	original := pingCommandContext
	t.Cleanup(func() {
		pingCommandContext = original
	})

	pingCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf 'icmp ok'; exit 0")
	}

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	submission := checker.probeICMP(t.Context(), Monitor{
		ID:   "icmp-monitor",
		Type: MonitorTypeICMP,
		ICMP: MonitorICMPConfig{
			Host: "127.0.0.1",
		},
	})

	if !submission.Success {
		t.Fatalf("expected icmp probe success, got failure reason %q", submission.FailureReason.String)
	}
	if submission.ResponseBody.String != "icmp ok" {
		t.Fatalf("expected icmp output to be preserved, got %q", submission.ResponseBody.String)
	}
}

func TestProbeICMPFailure(t *testing.T) {
	original := pingCommandContext
	t.Cleanup(func() {
		pingCommandContext = original
	})

	pingCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "printf 'icmp failed'; exit 1")
	}

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	submission := checker.probeICMP(t.Context(), Monitor{
		ID:   "icmp-monitor",
		Type: MonitorTypeICMP,
		ICMP: MonitorICMPConfig{
			Host: "127.0.0.1",
		},
	})

	if submission.Success {
		t.Fatal("expected icmp probe failure")
	}
	if submission.FailureReason.String != "icmp failed" {
		t.Fatalf("expected icmp failure output, got %q", submission.FailureReason.String)
	}
}

func mustGenerateCertificateAuthority(t *testing.T) ([]byte, *x509.Certificate, *rsa.PrivateKey) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate CA private key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "Eyrie Test CA",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed to create CA certificate: %v", err)
	}

	certificate, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("failed to parse CA certificate: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), certificate, privateKey
}

func mustGenerateLeafCertificate(t *testing.T, caCert *x509.Certificate, caKey *rsa.PrivateKey, clientAuth bool, name string) ([]byte, []byte) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		t.Fatalf("failed to generate serial number: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: name,
		},
		NotBefore:      time.Now().Add(-time.Hour),
		NotAfter:       time.Now().Add(time.Hour),
		KeyUsage:       x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:       []string{name},
		IPAddresses:    nil,
		IsCA:           false,
		MaxPathLen:     0,
		MaxPathLenZero: false,
	}
	if clientAuth {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		template.DNSNames = nil
	} else if ip := net.ParseIP(name); ip != nil {
		template.DNSNames = nil
		template.IPAddresses = []net.IP{ip}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &privateKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	return certPEM, keyPEM
}

func mustEncryptPEMBlock(t *testing.T, keyPEM []byte, password string) []byte {
	t.Helper()

	block, _ := pem.Decode(keyPEM)
	if block == nil {
		t.Fatal("failed to decode PEM block")
	}

	encryptedBlock, err := x509.EncryptPEMBlock(rand.Reader, block.Type, block.Bytes, []byte(password), x509.PEMCipherAES256)
	if err != nil {
		t.Fatalf("failed to encrypt PEM block: %v", err)
	}

	return pem.EncodeToMemory(encryptedBlock)
}

func writeTempTLSFile(t *testing.T, pattern string, contents []byte) string {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), pattern)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer file.Close()

	if _, err := file.Write(contents); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	return file.Name()
}

func TestProbeDatabaseDrivers(t *testing.T) {
	original := openSQLDatabase
	t.Cleanup(func() {
		openSQLDatabase = original
	})

	type call struct {
		driver string
		dsn    string
	}
	var calls []call
	openSQLDatabase = func(driverName string, dsn string) (sqlPinger, error) {
		calls = append(calls, call{driver: driverName, dsn: dsn})
		return fakeSQLDB{}, nil
	}

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	tests := []struct {
		name           string
		monitor        Monitor
		expectedDriver string
		expectedDSN    string
	}{
		{
			name: "postgres",
			monitor: Monitor{
				ID:   "postgres-monitor",
				Type: MonitorTypePostgres,
				Postgres: MonitorPostgresConfig{
					DSN: "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
				},
			},
			expectedDriver: "pgx",
			expectedDSN:    "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
		},
		{
			name: "mysql",
			monitor: Monitor{
				ID:   "mysql-monitor",
				Type: MonitorTypeMySQL,
				MySQL: MonitorMySQLConfig{
					DSN: "root:password@tcp(127.0.0.1:3306)/eyrie",
				},
			},
			expectedDriver: "mysql",
			expectedDSN:    "root:password@tcp(127.0.0.1:3306)/eyrie",
		},
		{
			name: "mssql",
			monitor: Monitor{
				ID:   "mssql-monitor",
				Type: MonitorTypeMSSQL,
				MSSQL: MonitorMSSQLConfig{
					DSN: "sqlserver://sa:Password123!@127.0.0.1:1433?database=master",
				},
			},
			expectedDriver: "sqlserver",
			expectedDSN:    "sqlserver://sa:Password123!@127.0.0.1:1433?database=master",
		},
		{
			name: "clickhouse tcp",
			monitor: Monitor{
				ID:   "clickhouse-tcp-monitor",
				Type: MonitorTypeClickHouse,
				ClickHouse: MonitorClickHouseConfig{
					DSN: "clickhouse://default:@127.0.0.1:9000/default",
				},
			},
			expectedDriver: "clickhouse",
			expectedDSN:    "clickhouse://default:@127.0.0.1:9000/default",
		},
		{
			name: "clickhouse http",
			monitor: Monitor{
				ID:   "clickhouse-http-monitor",
				Type: MonitorTypeClickHouse,
				ClickHouse: MonitorClickHouseConfig{
					DSN: "clickhouse://default:@127.0.0.1:8123/default?protocol=http",
				},
			},
			expectedDriver: "clickhouse",
			expectedDSN:    "clickhouse://default:@127.0.0.1:8123/default?protocol=http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			submission := checker.probeMonitor(t.Context(), tt.monitor)
			if !submission.Success {
				t.Fatalf("expected probe success, got failure reason %q", submission.FailureReason.String)
			}
			if submission.ProbeType != string(tt.monitor.EffectiveType()) {
				t.Fatalf("expected probe type %q, got %q", tt.monitor.EffectiveType(), submission.ProbeType)
			}
		})
	}

	if len(calls) != len(tests) {
		t.Fatalf("expected %d database opens, got %d", len(tests), len(calls))
	}
	for idx, tt := range tests {
		if calls[idx].driver != tt.expectedDriver {
			t.Fatalf("call %d driver = %q, want %q", idx, calls[idx].driver, tt.expectedDriver)
		}
		if calls[idx].dsn != tt.expectedDSN {
			t.Fatalf("call %d dsn = %q, want %q", idx, calls[idx].dsn, tt.expectedDSN)
		}
	}
}

func TestProbeDatabaseFailure(t *testing.T) {
	original := openSQLDatabase
	t.Cleanup(func() {
		openSQLDatabase = original
	})

	openSQLDatabase = func(driverName string, dsn string) (sqlPinger, error) {
		return fakeSQLDB{pingErr: errors.New("dial failed")}, nil
	}

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	submission := checker.probeMySQL(t.Context(), Monitor{
		ID:   "mysql-monitor",
		Type: MonitorTypeMySQL,
		MySQL: MonitorMySQLConfig{
			DSN: "root:password@tcp(127.0.0.1:3306)/eyrie",
		},
	})

	if submission.Success {
		t.Fatal("expected mysql probe failure")
	}
	if submission.FailureReason.String != "dial failed" {
		t.Fatalf("expected mysql failure reason to be preserved, got %q", submission.FailureReason.String)
	}
}

func TestMonitorDatabaseConfigSupport(t *testing.T) {
	tests := []struct {
		name            string
		monitor         Monitor
		expectedType    MonitorType
		expectedTimeout time.Duration
	}{
		{
			name: "mysql autodetect",
			monitor: Monitor{
				ID: "mysql-monitor",
				MySQL: MonitorMySQLConfig{
					DSN:            "root:password@tcp(127.0.0.1:3306)/eyrie",
					TimeoutSeconds: null.IntFrom(8),
				},
			},
			expectedType:    MonitorTypeMySQL,
			expectedTimeout: 8 * time.Second,
		},
		{
			name: "mssql autodetect",
			monitor: Monitor{
				ID: "mssql-monitor",
				MSSQL: MonitorMSSQLConfig{
					DSN:            "sqlserver://sa:Password123!@127.0.0.1:1433?database=master",
					TimeoutSeconds: null.IntFrom(9),
				},
			},
			expectedType:    MonitorTypeMSSQL,
			expectedTimeout: 9 * time.Second,
		},
		{
			name: "clickhouse autodetect",
			monitor: Monitor{
				ID: "clickhouse-monitor",
				ClickHouse: MonitorClickHouseConfig{
					DSN:            "clickhouse://default:@127.0.0.1:8123/default?protocol=http",
					TimeoutSeconds: null.IntFrom(11),
				},
			},
			expectedType:    MonitorTypeClickHouse,
			expectedTimeout: 11 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.monitor.EffectiveType(); got != tt.expectedType {
				t.Fatalf("EffectiveType() = %q, want %q", got, tt.expectedType)
			}
			if got := tt.monitor.EffectiveTimeout(30 * time.Second); got != tt.expectedTimeout {
				t.Fatalf("EffectiveTimeout() = %s, want %s", got, tt.expectedTimeout)
			}
			if err := tt.monitor.Validate(); err != nil {
				t.Fatalf("Validate() returned error: %v", err)
			}
		})
	}
}

func TestMonitorValidateDatabaseErrors(t *testing.T) {
	tests := []struct {
		name      string
		monitor   Monitor
		wantError string
	}{
		{
			name: "mysql missing dsn",
			monitor: Monitor{
				ID:   "mysql-monitor",
				Type: MonitorTypeMySQL,
			},
			wantError: "mysql.dsn is required",
		},
		{
			name: "mssql missing dsn",
			monitor: Monitor{
				ID:   "mssql-monitor",
				Type: MonitorTypeMSSQL,
			},
			wantError: "mssql.dsn is required",
		},
		{
			name: "clickhouse missing dsn",
			monitor: Monitor{
				ID:   "clickhouse-monitor",
				Type: MonitorTypeClickHouse,
			},
			wantError: "clickhouse.dsn is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.monitor.Validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %q", tt.wantError, err.Error())
			}
		})
	}
}

func TestProbeDatabaseOpenFailure(t *testing.T) {
	original := openSQLDatabase
	t.Cleanup(func() {
		openSQLDatabase = original
	})

	openSQLDatabase = func(driverName string, dsn string) (sqlPinger, error) {
		return nil, fmt.Errorf("invalid dsn")
	}

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	submission := checker.probeClickHouse(t.Context(), Monitor{
		ID:   "clickhouse-monitor",
		Type: MonitorTypeClickHouse,
		ClickHouse: MonitorClickHouseConfig{
			DSN: "clickhouse://invalid",
		},
	})

	if submission.Success {
		t.Fatal("expected clickhouse probe failure")
	}
	if submission.FailureReason.String != "invalid dsn" {
		t.Fatalf("expected open failure to be preserved, got %q", submission.FailureReason.String)
	}
}
