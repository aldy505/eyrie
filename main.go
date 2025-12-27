package main

import (
	"errors"
	"flag"
	"log/slog"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/kelseyhightower/envconfig"
)

func main() {
	mode := flag.String("mode", "server", "The mode of the current process, possible values are: server, checker")
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	monitorPath := flag.String("monitor", "monitor.yaml", "Path to monitor file (only for server mode)")
	flag.Parse()

	if mode == nil {
		slog.Error("mode flag is required")
		os.Exit(1)
		return
	}

	switch *mode {
	case "server":
		serverConfigFile, err := os.ReadFile(*configPath)
		if err != nil {
			slog.Error("failed to read config file", slog.String("error", err.Error()))
			os.Exit(1)
		}

		var serverConfig ServerConfig
		if err := yaml.Unmarshal(serverConfigFile, &serverConfig); err != nil {
			slog.Error("failed to unmarshal config file", slog.String("error", err.Error()))
			os.Exit(1)
		}

		monitorConfigFile, err := os.ReadFile(*monitorPath)
		if err != nil {
			slog.Error("failed to read monitor file", slog.String("error", err.Error()))
			os.Exit(1)
		}

		var monitorConfig MonitorConfig
		if err := yaml.Unmarshal(monitorConfigFile, &monitorConfig); err != nil {
			slog.Error("failed to unmarshal monitor file", slog.String("error", err.Error()))
			os.Exit(1)
		}

		NewServer(serverConfig, monitorConfig)
	case "checker":
		var checkerConfig CheckerConfig
		envconfig.Process("", &checkerConfig)
		checkerConfigFile, err := os.ReadFile(*configPath)
		if err == nil {
			if err := yaml.Unmarshal(checkerConfigFile, &checkerConfig); err != nil {
				slog.Error("failed to unmarshal config file", slog.String("error", err.Error()))
				os.Exit(1)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			slog.Error("failed to read config file", slog.String("error", err.Error()))
			os.Exit(1)
		}

	default:
		slog.Error("unknown mode", "mode", *mode)
		os.Exit(1)
	}
}
