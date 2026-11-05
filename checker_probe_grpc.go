package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/guregu/null/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func (c *Checker) probeGRPC(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	start := time.Now()
	timeout := monitor.EffectiveTimeout(10 * time.Second)
	submission := CheckerSubmissionRequest{
		MonitorID: monitor.ID,
		ProbeType: string(monitor.EffectiveType()),
		Timestamp: time.Now().UTC(),
	}

	transportCredentials := insecure.NewCredentials()
	if monitor.GRPC.UseTLS {
		transportCredentials = credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: monitor.GRPC.SkipTLSVerify,
			ServerName:         serverNameForAddress(monitor.GRPC.Address),
		})
	}

	conn, err := grpc.NewClient(monitor.GRPC.Address, grpc.WithTransportCredentials(transportCredentials))
	if err != nil {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}
	defer conn.Close()

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	response, err := grpc_health_v1.NewHealthClient(conn).Check(probeCtx, &grpc_health_v1.HealthCheckRequest{
		Service: monitor.GRPC.Service,
	})
	if err != nil {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}

	status := response.GetStatus()
	submission.ResponseBody = null.StringFrom(status.String())
	if status != grpc_health_v1.HealthCheckResponse_SERVING {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(fmt.Sprintf("grpc health check returned status %s", status.String()))
		return submission
	}

	submission.LatencyMs = time.Since(start).Milliseconds()
	submission.Success = true
	return submission
}
