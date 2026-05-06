package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

var Version string = "dev"

func main() {
	mode := flag.String("mode", "server", "The mode of the current process, possible values are: server, checker, ingester, worker, alerter, all")
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	monitorPath := flag.String("monitor", "monitor.yaml", "Path to monitor file (required for server, ingester, worker, and all modes)")
	flag.Parse()

	if mode == nil {
		slog.Error("mode flag is required")
		os.Exit(1)
		return
	}

	exitSignal := make(chan os.Signal, 1)
	signal.Notify(exitSignal, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(exitSignal)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		runner modeRunner
		err    error
	)

	switch *mode {
	case "server":
		runner, err = newServerModeRunner(ctx, *configPath, *monitorPath)
	case "checker":
		runner, err = newCheckerModeRunner(*configPath)
	case "ingester":
		runner, err = newIngesterModeRunner(ctx, *configPath, *monitorPath)
	case "worker":
		runner, err = newWorkerModeRunner(ctx, *configPath, *monitorPath)
	case "alerter":
		runner, err = newAlerterModeRunner(ctx, *configPath)
	case "all":
		runner, err = newAllModeRunner(ctx, *configPath, *monitorPath)
	default:
		slog.Error("unknown mode", "mode", *mode)
		os.Exit(1)
		return
	}
	if err != nil {
		slog.Error("failed to initialize mode", slog.String("mode", *mode), slog.String("error", err.Error()))
		os.Exit(1)
		return
	}

	if err := runMode(ctx, cancel, exitSignal, *mode, runner); err != nil {
		slog.Error("mode error", slog.String("mode", *mode), slog.String("error", err.Error()))
		os.Exit(1)
	}
}
