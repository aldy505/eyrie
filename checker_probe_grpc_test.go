package main

import (
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/guregu/null/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func startGRPCHealthServer(t *testing.T, serverOptions ...grpc.ServerOption) (string, *health.Server) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create grpc listener: %v", err)
	}

	healthServer := health.NewServer()
	grpcServer := grpc.NewServer(serverOptions...)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = grpcServer.Serve(listener)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		<-serveDone
	})

	return listener.Addr().String(), healthServer
}

func TestProbeGRPCHealthCheck(t *testing.T) {
	address, healthServer := startGRPCHealthServer(t)
	healthServer.SetServingStatus("svc-ok", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("svc-down", healthpb.HealthCheckResponse_NOT_SERVING)

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	tests := []struct {
		name                 string
		service              string
		expectedSuccess      bool
		expectedResponseBody string
		expectedFailure      string
	}{
		{
			name:                 "default service serving",
			expectedSuccess:      true,
			expectedResponseBody: healthpb.HealthCheckResponse_SERVING.String(),
		},
		{
			name:                 "named service serving",
			service:              "svc-ok",
			expectedSuccess:      true,
			expectedResponseBody: healthpb.HealthCheckResponse_SERVING.String(),
		},
		{
			name:                 "named service not serving",
			service:              "svc-down",
			expectedResponseBody: healthpb.HealthCheckResponse_NOT_SERVING.String(),
			expectedFailure:      "grpc health check returned status NOT_SERVING",
		},
		{
			name:            "unknown service",
			service:         "svc-missing",
			expectedFailure: "NotFound",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			submission := checker.probeGRPC(t.Context(), Monitor{
				ID:   "grpc-monitor",
				Type: MonitorTypeGRPC,
				GRPC: MonitorGRPCConfig{
					Address: address,
					Service: tt.service,
				},
			})

			if submission.Success != tt.expectedSuccess {
				t.Fatalf("expected success %t, got %t (failure reason %q)", tt.expectedSuccess, submission.Success, submission.FailureReason.String)
			}
			if submission.ProbeType != string(MonitorTypeGRPC) {
				t.Fatalf("expected probe type %q, got %q", MonitorTypeGRPC, submission.ProbeType)
			}
			if submission.ResponseBody.String != tt.expectedResponseBody {
				t.Fatalf("expected response body %q, got %q", tt.expectedResponseBody, submission.ResponseBody.String)
			}
			if tt.expectedFailure == "" {
				if submission.FailureReason.Valid {
					t.Fatalf("expected no failure reason, got %q", submission.FailureReason.String)
				}
				return
			}
			if !strings.Contains(submission.FailureReason.String, tt.expectedFailure) {
				t.Fatalf("expected failure reason containing %q, got %q", tt.expectedFailure, submission.FailureReason.String)
			}
		})
	}
}

func TestProbeGRPCTLS(t *testing.T) {
	_, caCert, caKey := mustGenerateCertificateAuthority(t)
	serverCertPEM, serverKeyPEM := mustGenerateLeafCertificate(t, caCert, caKey, false, "127.0.0.1")

	serverCertificate, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("failed to load server certificate: %v", err)
	}

	address, _ := startGRPCHealthServer(t, grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCertificate},
	})))

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	successSubmission := checker.probeGRPC(t.Context(), Monitor{
		ID:   "grpc-monitor",
		Type: MonitorTypeGRPC,
		GRPC: MonitorGRPCConfig{
			Address:       address,
			UseTLS:        true,
			SkipTLSVerify: true,
		},
	})
	if !successSubmission.Success {
		t.Fatalf("expected grpc TLS probe success, got failure reason %q", successSubmission.FailureReason.String)
	}

	verifySubmission := checker.probeGRPC(t.Context(), Monitor{
		ID:   "grpc-monitor",
		Type: MonitorTypeGRPC,
		GRPC: MonitorGRPCConfig{
			Address: address,
			UseTLS:  true,
		},
	})
	if verifySubmission.Success {
		t.Fatal("expected grpc TLS probe to reject an untrusted certificate")
	}
	if !strings.Contains(verifySubmission.FailureReason.String, "certificate") {
		t.Fatalf("expected certificate verification failure, got %q", verifySubmission.FailureReason.String)
	}
}

func TestProbeGRPCConnectionFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}

	checker, err := NewChecker(CheckerOptions{})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	submission := checker.probeGRPC(t.Context(), Monitor{
		ID:   "grpc-monitor",
		Type: MonitorTypeGRPC,
		GRPC: MonitorGRPCConfig{
			Address:        address,
			TimeoutSeconds: null.IntFrom(2),
		},
	})

	if submission.Success {
		t.Fatal("expected grpc probe failure for unreachable server")
	}
	if !submission.FailureReason.Valid || submission.FailureReason.String == "" {
		t.Fatal("expected grpc probe failure reason")
	}
}

func TestMonitorGRPCConfigSupport(t *testing.T) {
	monitor := Monitor{
		ID: "grpc-monitor",
		GRPC: MonitorGRPCConfig{
			Address:        "127.0.0.1:50051",
			TimeoutSeconds: null.IntFrom(7),
		},
	}

	if got := monitor.EffectiveType(); got != MonitorTypeGRPC {
		t.Fatalf("EffectiveType() = %q, want %q", got, MonitorTypeGRPC)
	}
	if got := monitor.EffectiveTimeout(30 * time.Second); got != 7*time.Second {
		t.Fatalf("EffectiveTimeout() = %s, want %s", got, 7*time.Second)
	}
	if err := monitor.Validate(); err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
}

func TestMonitorValidateGRPCErrors(t *testing.T) {
	monitor := Monitor{
		ID:   "grpc-monitor",
		Type: MonitorTypeGRPC,
	}

	err := monitor.Validate()
	if err == nil || !strings.Contains(err.Error(), "grpc.address is required") {
		t.Fatalf("expected grpc address validation error, got %v", err)
	}
}
