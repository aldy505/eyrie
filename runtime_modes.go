package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	"github.com/getsentry/sentry-go"
	sentryhttpclient "github.com/getsentry/sentry-go/httpclient"
	sentryslog "github.com/getsentry/sentry-go/slog"
	"github.com/goccy/go-yaml"
	"github.com/kelseyhightower/envconfig"
	"gocloud.dev/pubsub"

	_ "gocloud.dev/pubsub/kafkapubsub"
	_ "gocloud.dev/pubsub/mempubsub"
	_ "gocloud.dev/pubsub/natspubsub"
	_ "gocloud.dev/pubsub/rabbitpubsub"
)

type modeRunner interface {
	Start() error
	Stop() error
	Close() error
}

type sentryRuntimeConfig struct {
	Dsn              string
	ErrorSampleRate  float64
	TracesSampleRate float64
	Debug            bool
}

type serverRuntimeConfig struct {
	ServerConfig  ServerConfig
	MonitorConfig MonitorConfig
}

type databaseRuntime struct {
	connector *duckdb.Connector
	db        *sql.DB
}

type queueResources struct {
	processorProducer   *pubsub.Topic
	ingesterProducer    *pubsub.Topic
	alerterProducer     *pubsub.Topic
	processorSubscriber *pubsub.Subscription
	ingesterSubscriber  *pubsub.Subscription
	alerterSubscriber   *pubsub.Subscription
}

type serverModeRunner struct {
	server    *Server
	queues    *queueResources
	database  *databaseRuntime
	stopOnce  sync.Once
	closeOnce sync.Once
}

type checkerModeRunner struct {
	checker   *Checker
	stopOnce  sync.Once
	closeOnce sync.Once
}

type ingesterModeRunner struct {
	worker    *IngesterWorker
	queues    *queueResources
	database  *databaseRuntime
	stopOnce  sync.Once
	closeOnce sync.Once
}

type workerModeRunner struct {
	worker    *ProcessorWorker
	queues    *queueResources
	database  *databaseRuntime
	stopOnce  sync.Once
	closeOnce sync.Once
}

type alerterModeRunner struct {
	worker    *AlerterWorker
	queues    *queueResources
	stopOnce  sync.Once
	closeOnce sync.Once
}

type allModeRunner struct {
	server          *Server
	ingesterWorker  *IngesterWorker
	processorWorker *ProcessorWorker
	alerterWorker   *AlerterWorker
	queues          *queueResources
	database        *databaseRuntime
	serverAddress   string
	stopOnce        sync.Once
	closeOnce       sync.Once
}

func runMode(ctx context.Context, cancel context.CancelFunc, exitSignal <-chan os.Signal, mode string, runner modeRunner) error {
	shutdownResult := make(chan error, 1)
	go func() {
		<-exitSignal
		cancel()
		shutdownResult <- runner.Stop()
	}()

	slog.Info("starting mode", slog.String("mode", mode))
	startErr := runner.Start()

	var shutdownErr error
	if ctx.Err() != nil {
		shutdownErr = <-shutdownResult
		slog.Info("graceful shutdown complete", slog.String("mode", mode))
	} else {
		shutdownErr = runner.Stop()
	}

	closeErr := runner.Close()
	if startErr == nil {
		slog.Info("shutting down mode", slog.String("mode", mode))
	}

	return errors.Join(startErr, shutdownErr, closeErr)
}

func loadServerRuntimeConfig(configPath string, monitorPath string) (serverRuntimeConfig, error) {
	serverConfig, err := loadServerConfig(configPath)
	if err != nil {
		return serverRuntimeConfig{}, err
	}

	monitorConfig, err := loadMonitorConfig(monitorPath, serverConfig.RegisteredCheckers)
	if err != nil {
		return serverRuntimeConfig{}, err
	}

	return serverRuntimeConfig{
		ServerConfig:  serverConfig,
		MonitorConfig: monitorConfig,
	}, nil
}

func loadServerConfig(configPath string) (ServerConfig, error) {
	serverConfigFile, err := os.ReadFile(configPath)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("reading config file: %w", err)
	}

	var serverConfig ServerConfig
	if err := envconfig.Process("", &serverConfig); err != nil {
		return ServerConfig{}, fmt.Errorf("processing server env config: %w", err)
	}
	if err := yaml.Unmarshal(serverConfigFile, &serverConfig); err != nil {
		return ServerConfig{}, fmt.Errorf("unmarshaling config file: %w", err)
	}
	if err := serverConfig.Validate(); err != nil {
		return ServerConfig{}, fmt.Errorf("validating server config: %w", err)
	}

	return serverConfig, nil
}

func loadMonitorConfig(monitorPath string, registeredCheckers []RegisteredChecker) (MonitorConfig, error) {
	monitorConfigFile, err := os.ReadFile(monitorPath)
	if err != nil {
		return MonitorConfig{}, fmt.Errorf("reading monitor file: %w", err)
	}

	var monitorConfig MonitorConfig
	if err := envconfig.Process("", &monitorConfig); err != nil {
		return MonitorConfig{}, fmt.Errorf("processing monitor env config: %w", err)
	}
	if err := yaml.Unmarshal(monitorConfigFile, &monitorConfig); err != nil {
		return MonitorConfig{}, fmt.Errorf("unmarshaling monitor file: %w", err)
	}
	if err := monitorConfig.Validate(registeredCheckers); err != nil {
		return MonitorConfig{}, fmt.Errorf("validating monitor config: %w", err)
	}

	return monitorConfig, nil
}

func loadCheckerConfig(configPath string) (CheckerConfig, error) {
	var checkerConfig CheckerConfig
	if err := envconfig.Process("", &checkerConfig); err != nil {
		return CheckerConfig{}, fmt.Errorf("processing checker env config: %w", err)
	}

	checkerConfigFile, err := os.ReadFile(configPath)
	if err == nil {
		if err := yaml.Unmarshal(checkerConfigFile, &checkerConfig); err != nil {
			return CheckerConfig{}, fmt.Errorf("unmarshaling config file: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return CheckerConfig{}, fmt.Errorf("reading config file: %w", err)
	}

	return checkerConfig, nil
}

func initSentryClient(config sentryRuntimeConfig) error {
	err := sentry.Init(sentry.ClientOptions{
		Dsn:                  config.Dsn,
		SampleRate:           config.ErrorSampleRate,
		EnableTracing:        true,
		TracesSampleRate:     config.TracesSampleRate,
		EnableLogs:           true,
		Debug:                config.Debug,
		Release:              Version,
		PropagateTraceparent: true,
	})

	ctx := context.Background()
	handler := sentryslog.Option{
		EventLevel: []slog.Level{slog.LevelError, sentryslog.LevelFatal}, // Only Error and Fatal as events
		LogLevel:   []slog.Level{slog.LevelWarn, slog.LevelInfo},         // Only Warn and Info as logs
	}.NewSentryHandler(ctx)

	slog.SetDefault(slog.New(slog.NewMultiHandler(handler, slog.NewTextHandler(nil, nil))))

	return err
}

func flushSentry() {
	sentry.Flush(2 * time.Second)
}

func openDatabaseRuntime(ctx context.Context, serverConfig ServerConfig) (*databaseRuntime, error) {
	connector, err := duckdb.NewConnector(serverConfig.Database.Path, nil)
	if err != nil {
		return nil, fmt.Errorf("creating duckdb connector: %w", duckDBStartupRecoveryHint(serverConfig.Database.Path, err))
	}

	db := sql.OpenDB(connector)
	if err := Migrate(db, ctx, true); err != nil {
		closeErr := closeDatabaseRuntime(&databaseRuntime{connector: connector, db: db})
		return nil, errors.Join(fmt.Errorf("migrating database: %w", err), closeErr)
	}

	return &databaseRuntime{
		connector: connector,
		db:        db,
	}, nil
}

func closeDatabaseRuntime(runtime *databaseRuntime) error {
	if runtime == nil {
		return nil
	}

	var err error
	if runtime.db != nil {
		err = errors.Join(err, wrapShutdownError("closing database", runtime.db.Close()))
	}
	if runtime.connector != nil {
		err = errors.Join(err, wrapShutdownError("closing database connector", runtime.connector.Close()))
	}
	return err
}

func openTopicResource(ctx context.Context, address string, name string) (*pubsub.Topic, error) {
	topic, err := pubsub.OpenTopic(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("opening %s producer: %w", name, err)
	}
	return topic, nil
}

func openSubscriptionResource(ctx context.Context, address string, name string) (*pubsub.Subscription, error) {
	subscription, err := pubsub.OpenSubscription(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("opening %s subscriber: %w", name, err)
	}
	return subscription, nil
}

func (r *queueResources) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}

	var err error
	if r.alerterProducer != nil {
		err = errors.Join(err, wrapShutdownError("shutting down alerter producer", r.alerterProducer.Shutdown(ctx)))
	}
	if r.ingesterProducer != nil {
		err = errors.Join(err, wrapShutdownError("shutting down ingester producer", r.ingesterProducer.Shutdown(ctx)))
	}
	if r.processorProducer != nil {
		err = errors.Join(err, wrapShutdownError("shutting down processor producer", r.processorProducer.Shutdown(ctx)))
	}
	if r.alerterSubscriber != nil {
		err = errors.Join(err, wrapShutdownError("shutting down alerter subscriber", r.alerterSubscriber.Shutdown(ctx)))
	}
	if r.processorSubscriber != nil {
		err = errors.Join(err, wrapShutdownError("shutting down processor subscriber", r.processorSubscriber.Shutdown(ctx)))
	}
	if r.ingesterSubscriber != nil {
		err = errors.Join(err, wrapShutdownError("shutting down ingester subscriber", r.ingesterSubscriber.Shutdown(ctx)))
	}
	return err
}

func wrapShutdownError(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}

func newServerModeRunner(ctx context.Context, configPath string, monitorPath string) (modeRunner, error) {
	runtimeConfig, err := loadServerRuntimeConfig(configPath, monitorPath)
	if err != nil {
		return nil, err
	}

	if err := initSentryClient(sentryRuntimeConfig{
		Dsn:              runtimeConfig.ServerConfig.Sentry.Dsn,
		ErrorSampleRate:  runtimeConfig.ServerConfig.Sentry.ErrorSampleRate,
		TracesSampleRate: runtimeConfig.ServerConfig.Sentry.TracesSampleRate,
		Debug:            runtimeConfig.ServerConfig.Sentry.Debug,
	}); err != nil {
		return nil, fmt.Errorf("initializing sentry: %w", err)
	}

	database, err := openDatabaseRuntime(ctx, runtimeConfig.ServerConfig)
	if err != nil {
		flushSentry()
		return nil, err
	}

	queues := &queueResources{}
	queues.ingesterProducer, err = openTopicResource(ctx, runtimeConfig.ServerConfig.TaskQueue.Ingester.ProducerAddress, "ingester")
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}
	queues.processorProducer, err = openTopicResource(ctx, runtimeConfig.ServerConfig.TaskQueue.Processor.ProducerAddress, "processor")
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}

	server, err := NewServer(ServerOptions{
		Database:          database.db,
		ServerConfig:      runtimeConfig.ServerConfig,
		MonitorConfig:     runtimeConfig.MonitorConfig,
		ProcessorProducer: queues.processorProducer,
		IngesterProducer:  queues.ingesterProducer,
	})
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(fmt.Errorf("creating server: %w", err), closeErr)
	}

	return &serverModeRunner{
		server:   server,
		queues:   queues,
		database: database,
	}, nil
}

func (r *serverModeRunner) Start() error {
	slog.Info("starting server", "host", r.server.serverConfig.Server.Host, "port", r.server.serverConfig.Server.Port)
	if err := r.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

func (r *serverModeRunner) Stop() error {
	var err error
	r.stopOnce.Do(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		err = errors.Join(
			wrapShutdownError("shutting down server", r.server.Shutdown(shutdownCtx)),
			wrapShutdownError("closing uptime caches", r.server.CloseCaches()),
		)
	})
	return err
}

func (r *serverModeRunner) Close() error {
	var err error
	r.closeOnce.Do(func() {
		err = errors.Join(
			r.queues.Shutdown(context.Background()),
			closeDatabaseRuntime(r.database),
		)
		flushSentry()
	})
	return err
}

func newCheckerModeRunner(configPath string) (modeRunner, error) {
	checkerConfig, err := loadCheckerConfig(configPath)
	if err != nil {
		return nil, err
	}

	if err := initSentryClient(sentryRuntimeConfig{
		Dsn:              checkerConfig.Sentry.Dsn,
		ErrorSampleRate:  checkerConfig.Sentry.ErrorSampleRate,
		TracesSampleRate: checkerConfig.Sentry.TracesSampleRate,
		Debug:            checkerConfig.Sentry.Debug,
	}); err != nil {
		return nil, fmt.Errorf("initializing sentry: %w", err)
	}

	httpClient := http.DefaultClient
	if checkerConfig.Sentry.TraceOutgoingRequests {
		httpClient = &http.Client{
			Transport: sentryhttpclient.NewSentryRoundTripper(nil, nil),
		}
	}

	checker, err := NewChecker(CheckerOptions{
		CheckerConfig: checkerConfig,
		HttpClient:    httpClient,
	})
	if err != nil {
		flushSentry()
		return nil, fmt.Errorf("creating checker: %w", err)
	}

	return &checkerModeRunner{checker: checker}, nil
}

func (r *checkerModeRunner) Start() error {
	return r.checker.Start()
}

func (r *checkerModeRunner) Stop() error {
	var err error
	r.stopOnce.Do(func() {
		err = wrapShutdownError("stopping checker", r.checker.Stop())
	})
	return err
}

func (r *checkerModeRunner) Close() error {
	r.closeOnce.Do(flushSentry)
	return nil
}

func newIngesterModeRunner(ctx context.Context, configPath string, monitorPath string) (modeRunner, error) {
	runtimeConfig, err := loadServerRuntimeConfig(configPath, monitorPath)
	if err != nil {
		return nil, err
	}

	if err := initSentryClient(sentryRuntimeConfig{
		Dsn:              runtimeConfig.ServerConfig.Sentry.Dsn,
		ErrorSampleRate:  runtimeConfig.ServerConfig.Sentry.ErrorSampleRate,
		TracesSampleRate: runtimeConfig.ServerConfig.Sentry.TracesSampleRate,
		Debug:            runtimeConfig.ServerConfig.Sentry.Debug,
	}); err != nil {
		return nil, fmt.Errorf("initializing sentry: %w", err)
	}

	database, err := openDatabaseRuntime(ctx, runtimeConfig.ServerConfig)
	if err != nil {
		flushSentry()
		return nil, err
	}

	queues := &queueResources{}
	queues.ingesterSubscriber, err = openSubscriptionResource(ctx, runtimeConfig.ServerConfig.TaskQueue.Ingester.ConsumerAddress, "ingester")
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}

	return &ingesterModeRunner{
		worker:   NewIngesterWorker(database.db, queues.ingesterSubscriber, runtimeConfig.MonitorConfig),
		queues:   queues,
		database: database,
	}, nil
}

func (r *ingesterModeRunner) Start() error {
	return r.worker.Start()
}

func (r *ingesterModeRunner) Stop() error {
	var err error
	r.stopOnce.Do(func() {
		err = wrapShutdownError("stopping ingester worker", r.worker.Stop())
	})
	return err
}

func (r *ingesterModeRunner) Close() error {
	var err error
	r.closeOnce.Do(func() {
		err = errors.Join(
			r.queues.Shutdown(context.Background()),
			closeDatabaseRuntime(r.database),
		)
		flushSentry()
	})
	return err
}

func newWorkerModeRunner(ctx context.Context, configPath string, monitorPath string) (modeRunner, error) {
	runtimeConfig, err := loadServerRuntimeConfig(configPath, monitorPath)
	if err != nil {
		return nil, err
	}

	if err := initSentryClient(sentryRuntimeConfig{
		Dsn:              runtimeConfig.ServerConfig.Sentry.Dsn,
		ErrorSampleRate:  runtimeConfig.ServerConfig.Sentry.ErrorSampleRate,
		TracesSampleRate: runtimeConfig.ServerConfig.Sentry.TracesSampleRate,
		Debug:            runtimeConfig.ServerConfig.Sentry.Debug,
	}); err != nil {
		return nil, fmt.Errorf("initializing sentry: %w", err)
	}

	database, err := openDatabaseRuntime(ctx, runtimeConfig.ServerConfig)
	if err != nil {
		flushSentry()
		return nil, err
	}

	queues := &queueResources{}
	queues.alerterProducer, err = openTopicResource(ctx, runtimeConfig.ServerConfig.TaskQueue.Alerter.ProducerAddress, "alerter")
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}
	queues.processorSubscriber, err = openSubscriptionResource(ctx, runtimeConfig.ServerConfig.TaskQueue.Processor.ConsumerAddress, "processor")
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}

	return &workerModeRunner{
		worker:   NewProcessorWorker(database.db, queues.processorSubscriber, queues.alerterProducer, runtimeConfig.MonitorConfig, runtimeConfig.ServerConfig.Dataset),
		queues:   queues,
		database: database,
	}, nil
}

func (r *workerModeRunner) Start() error {
	return r.worker.Start()
}

func (r *workerModeRunner) Stop() error {
	var err error
	r.stopOnce.Do(func() {
		err = wrapShutdownError("stopping processor worker", r.worker.Stop())
	})
	return err
}

func (r *workerModeRunner) Close() error {
	var err error
	r.closeOnce.Do(func() {
		err = errors.Join(
			r.queues.Shutdown(context.Background()),
			closeDatabaseRuntime(r.database),
		)
		flushSentry()
	})
	return err
}

func newAlerterModeRunner(ctx context.Context, configPath string) (modeRunner, error) {
	serverConfig, err := loadServerConfig(configPath)
	if err != nil {
		return nil, err
	}

	if err := initSentryClient(sentryRuntimeConfig{
		Dsn:              serverConfig.Sentry.Dsn,
		ErrorSampleRate:  serverConfig.Sentry.ErrorSampleRate,
		TracesSampleRate: serverConfig.Sentry.TracesSampleRate,
		Debug:            serverConfig.Sentry.Debug,
	}); err != nil {
		return nil, fmt.Errorf("initializing sentry: %w", err)
	}

	queues := &queueResources{}
	queues.alerterSubscriber, err = openSubscriptionResource(ctx, serverConfig.TaskQueue.Alerter.ConsumerAddress, "alerter")
	if err != nil {
		closeErr := queues.Shutdown(context.Background())
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}

	return &alerterModeRunner{
		worker: NewAlerterWorker(queues.alerterSubscriber, BuildAlerters(serverConfig)),
		queues: queues,
	}, nil
}

func (r *alerterModeRunner) Start() error {
	return r.worker.Start()
}

func (r *alerterModeRunner) Stop() error {
	var err error
	r.stopOnce.Do(func() {
		err = wrapShutdownError("stopping alerter worker", r.worker.Stop())
	})
	return err
}

func (r *alerterModeRunner) Close() error {
	var err error
	r.closeOnce.Do(func() {
		err = r.queues.Shutdown(context.Background())
		flushSentry()
	})
	return err
}

func newAllModeRunner(ctx context.Context, configPath string, monitorPath string) (modeRunner, error) {
	runtimeConfig, err := loadServerRuntimeConfig(configPath, monitorPath)
	if err != nil {
		return nil, err
	}

	if err := initSentryClient(sentryRuntimeConfig{
		Dsn:              runtimeConfig.ServerConfig.Sentry.Dsn,
		ErrorSampleRate:  runtimeConfig.ServerConfig.Sentry.ErrorSampleRate,
		TracesSampleRate: runtimeConfig.ServerConfig.Sentry.TracesSampleRate,
		Debug:            runtimeConfig.ServerConfig.Sentry.Debug,
	}); err != nil {
		return nil, fmt.Errorf("initializing sentry: %w", err)
	}

	database, err := openDatabaseRuntime(ctx, runtimeConfig.ServerConfig)
	if err != nil {
		flushSentry()
		return nil, err
	}

	queues := &queueResources{}
	queues.ingesterProducer, err = openTopicResource(ctx, runtimeConfig.ServerConfig.TaskQueue.Ingester.ProducerAddress, "ingester")
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}
	queues.processorProducer, err = openTopicResource(ctx, runtimeConfig.ServerConfig.TaskQueue.Processor.ProducerAddress, "processor")
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}
	queues.alerterProducer, err = openTopicResource(ctx, runtimeConfig.ServerConfig.TaskQueue.Alerter.ProducerAddress, "alerter")
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}
	queues.ingesterSubscriber, err = openSubscriptionResource(ctx, runtimeConfig.ServerConfig.TaskQueue.Ingester.ConsumerAddress, "ingester")
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}
	queues.processorSubscriber, err = openSubscriptionResource(ctx, runtimeConfig.ServerConfig.TaskQueue.Processor.ConsumerAddress, "processor")
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}
	queues.alerterSubscriber, err = openSubscriptionResource(ctx, runtimeConfig.ServerConfig.TaskQueue.Alerter.ConsumerAddress, "alerter")
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(err, closeErr)
	}

	server, err := NewServer(ServerOptions{
		Database:          database.db,
		ServerConfig:      runtimeConfig.ServerConfig,
		MonitorConfig:     runtimeConfig.MonitorConfig,
		ProcessorProducer: queues.processorProducer,
		IngesterProducer:  queues.ingesterProducer,
	})
	if err != nil {
		closeErr := errors.Join(queues.Shutdown(context.Background()), closeDatabaseRuntime(database))
		flushSentry()
		return nil, errors.Join(fmt.Errorf("creating server: %w", err), closeErr)
	}

	return &allModeRunner{
		server:          server,
		ingesterWorker:  NewIngesterWorker(database.db, queues.ingesterSubscriber, runtimeConfig.MonitorConfig),
		processorWorker: NewProcessorWorker(database.db, queues.processorSubscriber, queues.alerterProducer, runtimeConfig.MonitorConfig, runtimeConfig.ServerConfig.Dataset),
		alerterWorker:   NewAlerterWorker(queues.alerterSubscriber, BuildAlerters(runtimeConfig.ServerConfig)),
		queues:          queues,
		database:        database,
		serverAddress:   fmt.Sprintf("%s:%d", runtimeConfig.ServerConfig.Server.Host, runtimeConfig.ServerConfig.Server.Port),
	}, nil
}

func (r *allModeRunner) Start() error {
	const componentCount = 4
	errCh := make(chan error, componentCount)

	startBackground := func(name string, run func() error) {
		go func() {
			slog.Info("starting component", slog.String("component", name))
			if err := run(); err != nil {
				errCh <- fmt.Errorf("%s error: %w", name, err)
				return
			}
			slog.Info("shutting down component", slog.String("component", name))
			errCh <- nil
		}()
	}

	startBackground("ingester", r.ingesterWorker.Start)
	startBackground("worker", r.processorWorker.Start)
	startBackground("alerter", r.alerterWorker.Start)
	startBackground("server", func() error {
		slog.Info("starting server", slog.String("address", r.serverAddress))
		if err := r.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	for range componentCount {
		if err := <-errCh; err != nil {
			return err
		}
	}

	return nil
}

func (r *allModeRunner) Stop() error {
	var err error
	r.stopOnce.Do(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		err = errors.Join(
			wrapShutdownError("shutting down server", r.server.Shutdown(shutdownCtx)),
			wrapShutdownError("closing uptime caches", r.server.CloseCaches()),
			wrapShutdownError("stopping alerter worker", r.alerterWorker.Stop()),
			wrapShutdownError("stopping processor worker", r.processorWorker.Stop()),
			wrapShutdownError("stopping ingester worker", r.ingesterWorker.Stop()),
		)
	})
	return err
}

func (r *allModeRunner) Close() error {
	var err error
	r.closeOnce.Do(func() {
		err = errors.Join(
			r.queues.Shutdown(context.Background()),
			closeDatabaseRuntime(r.database),
		)
		flushSentry()
	})
	return err
}
