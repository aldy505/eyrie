package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/kelseyhightower/envconfig"
	"github.com/marcboeker/go-duckdb/v2"
	"gocloud.dev/pubsub"

	_ "gocloud.dev/pubsub/kafkapubsub"
	_ "gocloud.dev/pubsub/mempubsub"
	_ "gocloud.dev/pubsub/natspubsub"
	_ "gocloud.dev/pubsub/rabbitpubsub"
)

var Version string = "dev"

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

	exitSignal := make(chan os.Signal, 1)
	signal.Notify(exitSignal, os.Interrupt)
	ctx, cancel := context.WithCancel(context.Background())

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

		connector, err := duckdb.NewConnector(serverConfig.Database.Path, nil)
		if err != nil {
			slog.Error("failed to create duckdb connector", slog.String("error", err.Error()))
			os.Exit(1)
		}

		db := sql.OpenDB(connector)

		if err := Migrate(db, ctx, true); err != nil {
			slog.Error("failed to migrate database", slog.String("error", err.Error()))
			os.Exit(1)
		}

		ingesterProducer, err := pubsub.OpenTopic(ctx, serverConfig.TaskQueue.Ingester.ProducerAddress)
		if err != nil {
			slog.Error("failed to open ingester producer", slog.String("error", err.Error()))
			os.Exit(1)
		}

		processorProducer, err := pubsub.OpenTopic(ctx, serverConfig.TaskQueue.Processor.ProducerAddress)
		if err != nil {
			slog.Error("failed to open processor producer", slog.String("error", err.Error()))
			os.Exit(1)
		}

		alerterProducer, err := pubsub.OpenTopic(ctx, serverConfig.TaskQueue.Alerter.ProducerAddress)
		if err != nil {
			slog.Error("failed to open alerter producer", slog.String("error", err.Error()))
			os.Exit(1)
		}

		ingesterSubscriber, err := pubsub.OpenSubscription(ctx, serverConfig.TaskQueue.Ingester.ConsumerAddress)
		if err != nil {
			slog.Error("failed to open ingester subscriber", slog.String("error", err.Error()))
			os.Exit(1)
		}

		processorSubscriber, err := pubsub.OpenSubscription(ctx, serverConfig.TaskQueue.Processor.ConsumerAddress)
		if err != nil {
			slog.Error("failed to open processor subscriber", slog.String("error", err.Error()))
			os.Exit(1)
		}

		alerterSubscriber, err := pubsub.OpenSubscription(ctx, serverConfig.TaskQueue.Alerter.ConsumerAddress)
		if err != nil {
			slog.Error("failed to open alerter subscriber", slog.String("error", err.Error()))
			os.Exit(1)
		}

		ingesterWorker := NewIngesterWorker(db, ingesterSubscriber)
		processorWorker := NewProcessorWorker(db, processorSubscriber, alerterProducer, monitorConfig)
		alerterWorker := NewAlerterWorker(alerterSubscriber)

		srv, err := NewServer(ServerOptions{
			Database:          db,
			ServerConfig:      serverConfig,
			MonitorConfig:     monitorConfig,
			ProcessorProducer: processorProducer,
			IngesterProducer:  ingesterProducer,
		})
		if err != nil {
			slog.Error("failed to create server", slog.String("error", err.Error()))
			os.Exit(1)
		}

		go func() {
			<-exitSignal
			cancel()

			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second*10)
			defer shutdownCancel()

			if err := srv.Shutdown(shutdownCtx); err != nil {
				slog.Error("failed to shutdown server", slog.String("error", err.Error()))
			}

			if err := alerterWorker.Stop(); err != nil {
				slog.Error("failed to stop alerter worker", slog.String("error", err.Error()))
			}

			if err := processorWorker.Stop(); err != nil {
				slog.Error("failed to stop processor worker", slog.String("error", err.Error()))
			}

			if err := ingesterWorker.Stop(); err != nil {
				slog.Error("failed to stop ingester worker", slog.String("error", err.Error()))
			}

			if err := alerterProducer.Shutdown(shutdownCtx); err != nil {
				slog.Error("failed to shutdown alerter producer", slog.String("error", err.Error()))
			}

			if err := ingesterProducer.Shutdown(shutdownCtx); err != nil {
				slog.Error("failed to shutdown ingester producer", slog.String("error", err.Error()))
			}

			if err := processorProducer.Shutdown(shutdownCtx); err != nil {
				slog.Error("failed to shutdown processor producer", slog.String("error", err.Error()))
			}

			if err := alerterSubscriber.Shutdown(shutdownCtx); err != nil {
				slog.Error("failed to shutdown alerter subscriber", slog.String("error", err.Error()))
			}

			if err := processorSubscriber.Shutdown(shutdownCtx); err != nil {
				slog.Error("failed to shutdown processor subscriber", slog.String("error", err.Error()))
			}

			if err := ingesterSubscriber.Shutdown(shutdownCtx); err != nil {
				slog.Error("failed to shutdown ingester subscriber", slog.String("error", err.Error()))
			}

			if err := db.Close(); err != nil {
				slog.Error("failed to close database", slog.String("error", err.Error()))
			}

			if err := connector.Close(); err != nil {
				slog.Error("failed to close database connector", slog.String("error", err.Error()))
			}

			slog.Info("graceful shutdown complete")
		}()

		go func() {
			slog.Info("starting ingester worker")
			if err := ingesterWorker.Start(); err != nil {
				slog.Error("ingester worker error", slog.String("error", err.Error()))
				os.Exit(1)
			}
			slog.Info("shutting down ingester worker")
		}()

		go func() {
			slog.Info("starting processor worker")
			if err := processorWorker.Start(); err != nil {
				slog.Error("processor worker error", slog.String("error", err.Error()))
				os.Exit(1)
			}
			slog.Info("shutting down processor worker")
		}()

		go func() {
			slog.Info("starting alerter worker")
			if err := alerterWorker.Start(); err != nil {
				slog.Error("alerter worker error", slog.String("error", err.Error()))
				os.Exit(1)
			}
			slog.Info("shutting down alerter worker")
		}()

		slog.Info("starting server", "host", serverConfig.Server.Host, "port", serverConfig.Server.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", slog.String("error", err.Error()))
			os.Exit(1)
		}

		slog.Info("shutting down server")
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

		checker, err := NewChecker(CheckerOptions{
			CheckerConfig: checkerConfig,
			HttpClient:    http.DefaultClient,
		})
		if err != nil {
			slog.Error("failed to create checker", slog.String("error", err.Error()))
			os.Exit(1)
		}

		go func() {
			<-exitSignal
			cancel()

			if err := checker.Stop(); err != nil {
				slog.Error("failed to stop checker", slog.String("error", err.Error()))
			}

			slog.Info("graceful shutdown complete")
		}()

		slog.Info("starting checker")
		if err := checker.Start(); err != nil {
			slog.Error("checker error", slog.String("error", err.Error()))
			os.Exit(1)
		}

		slog.Info("shutting down checker")
	default:
		slog.Error("unknown mode", "mode", *mode)
		os.Exit(1)
	}
}
