package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	"github.com/guregu/null/v5"
	"gocloud.dev/pubsub"
)

const (
	rawIngestBatchSize   = 100
	rawIngestBatchWindow = 100 * time.Millisecond
)

// IngesterWorker is responsible for ingesting raw `monitor_historical`
// and creating hourly and daily aggregates.
type IngesterWorker struct {
	db            *PostgresStore
	subscriber    *pubsub.Subscription
	monitorConfig MonitorConfig
	datasetConfig DatasetConfig
	shutdown      chan struct{}

	dirtyAggregationMu       sync.Mutex
	dirtyAggregationTasks    map[string]aggregationTask
	dirtyHourAggregationTask map[string]hourAggregationTask
}

func NewIngesterWorker(db *PostgresStore, subscriber *pubsub.Subscription, monitorConfig MonitorConfig, datasetConfig DatasetConfig) *IngesterWorker {
	return &IngesterWorker{
		db:            db,
		subscriber:    subscriber,
		monitorConfig: monitorConfig,
		datasetConfig: datasetConfig,
		shutdown:      make(chan struct{}),

		dirtyAggregationTasks:    make(map[string]aggregationTask),
		dirtyHourAggregationTask: make(map[string]hourAggregationTask),
	}
}

type aggregationTask struct {
	monitorID string
	date      time.Time
}

type hourAggregationTask struct {
	monitorID string
	hour      time.Time
}

func (w *IngesterWorker) findMonitorConfig(monitorID string) (Monitor, bool) {
	for _, monitor := range w.monitorConfig.Monitors {
		if monitor.ID == monitorID {
			return monitor, true
		}
	}

	return Monitor{}, false
}

func (w *IngesterWorker) determineHistoricalSuccess(monitorID string, probeType string, submission CheckerSubmissionRequest) bool {
	if monitorConfig, found := w.findMonitorConfig(monitorID); found {
		return monitorConfig.IsSuccessfulStatus(submission.StatusCode, submission.Success)
	}

	if !submission.Success && (probeType == string(MonitorTypeHTTP) || probeType == "") {
		return submission.StatusCode >= 200 && submission.StatusCode < 400
	}

	return submission.Success
}

func (w *IngesterWorker) buildHistoricalRow(request CheckerSubmissionRequest, region string) MonitorHistorical {
	probeType := request.ProbeType
	if probeType == "" {
		probeType = string(MonitorTypeHTTP)
	}
	success := w.determineHistoricalSuccess(request.MonitorID, probeType, request)

	return MonitorHistorical{
		MonitorID:                 request.MonitorID,
		Region:                    region,
		ProbeType:                 probeType,
		Success:                   success,
		FailureReason:             request.FailureReason,
		StatusCode:                request.StatusCode,
		LatencyMs:                 int(request.LatencyMs),
		ResponseBody:              request.ResponseBody,
		TlsVersion:                request.TlsVersion,
		TlsCipher:                 request.TlsCipher,
		TlsExpiry:                 request.TlsExpiry,
		TlsCertNotBefore:          request.TlsCertInfo.NotBefore,
		TlsCertIssuer:             request.TlsCertInfo.Issuer,
		TlsCertSubject:            request.TlsCertInfo.Subject,
		TlsCertDN:                 request.TlsCertInfo.DN,
		TlsCertFingerprint:        request.TlsCertInfo.Fingerprint,
		TlsCertIsExpired:          request.TlsCertInfo.IsExpired,
		TimingConnAcquiredMs:      null.IntFrom(request.Timings.ConnAcquiredMs),
		TimingFirstResponseByteMs: null.IntFrom(request.Timings.FirstResponseByteMs),
		TimingDNSLookupStartMs:    null.IntFrom(request.Timings.DNSLookupStartMs),
		TimingDNSLookupDoneMs:     null.IntFrom(request.Timings.DNSLookupDoneMs),
		TimingTLSHandshakeStartMs: null.IntFrom(request.Timings.TLSHandshakeStartMs),
		TimingTLSHandshakeDoneMs:  null.IntFrom(request.Timings.TLSHandshakeDoneMs),
		CreatedAt:                 normalizeRawTimestamp(request.Timestamp),
	}
}

// Start begins the ingestion process, listening for new messages.
// It is a blocking call.
func (w *IngesterWorker) Start() error {
	go w.runPeriodicAggregation()

	for {
		select {
		case <-w.shutdown:
			return nil
		default:
		}

		messages, err := w.receiveBatch()
		if err != nil {
			select {
			case <-w.shutdown:
				return nil
			default:
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			slog.Error("receiving messages for ingest queue", slog.String("error", err.Error()))
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if len(messages) == 0 {
			continue
		}

		w.processBatch(messages)
	}
}

func (w *IngesterWorker) Stop() error {
	close(w.shutdown)
	return nil
}

func (w *IngesterWorker) receiveBatch() ([]*pubsub.Message, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	message, err := w.subscriber.Receive(ctx)
	if err != nil {
		return nil, err
	}

	batch := []*pubsub.Message{message}
	timer := time.NewTimer(rawIngestBatchWindow)
	defer timer.Stop()

	for len(batch) < rawIngestBatchSize {
		select {
		case <-w.shutdown:
			return batch, nil
		case <-timer.C:
			return batch, nil
		default:
		}

		receiveCtx, receiveCancel := context.WithTimeout(ctx, rawIngestBatchWindow)
		next, err := w.subscriber.Receive(receiveCtx)
		receiveCancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return batch, nil
			}
			return batch, err
		}
		batch = append(batch, next)
	}

	return batch, nil
}

type pendingRawSubmission struct {
	message   *pubsub.Message
	row       MonitorHistorical
	probeType string
	region    string
}

func (w *IngesterWorker) processBatch(messages []*pubsub.Message) {
	ctx := sentry.SetHubOnContext(context.Background(), sentry.CurrentHub().Clone())
	span := sentry.StartTransaction(ctx, "ingester.process_batch", sentry.WithOpName("task.ingester.process_batch"), sentry.WithTransactionSource(sentry.SourceCustom))
	ctx = span.Context()
	defer span.Finish()

	span.SetData("eyrie.batch_size", len(messages))

	pending := make([]pendingRawSubmission, 0, len(messages))
	for _, message := range messages {
		var request CheckerSubmissionRequest
		if err := json.Unmarshal(message.Body, &request); err != nil {
			slog.ErrorContext(ctx, "unmarshaling ingest message", slog.String("error", err.Error()))
			if message.Nackable() {
				message.Nack()
			} else {
				message.Ack()
			}
			continue
		}

		region := "default"
		if message.Metadata != nil {
			if r, ok := message.Metadata["region"]; ok {
				region = r
			}
		}

		row := w.buildHistoricalRow(request, region)
		pending = append(pending, pendingRawSubmission{
			message:   message,
			row:       row,
			probeType: row.ProbeType,
			region:    region,
		})
	}

	if len(pending) == 0 {
		return
	}

	rows := make([]MonitorHistorical, len(pending))
	for i := range pending {
		rows[i] = pending[i].row
	}

	outcomes, err := w.db.InsertMonitorHistoricalBatch(ctx, rows, w.datasetConfig.PartitionLookaheadDays)
	if err != nil {
		if hub := sentry.GetHubFromContext(ctx); hub != nil {
			hub.CaptureException(fmt.Errorf("ingesting monitor historical batch: %w", err))
		}
		slog.ErrorContext(ctx, "ingesting monitor historical batch", slog.String("error", err.Error()), slog.Int("batch_size", len(pending)))
		for _, item := range pending {
			if item.message.Nackable() {
				item.message.Nack()
			} else {
				item.message.Ack()
			}
		}
		time.Sleep(100 * time.Millisecond)
		return
	}

	for i, item := range pending {
		switch outcomes[i] {
		case RawRejectedOld, RawRejectedFuture:
			slog.WarnContext(ctx, "rejecting historical submission with time drift",
				slog.String("monitor_id", item.row.MonitorID),
				slog.String("region", item.region),
				slog.Time("submitted_at", item.row.CreatedAt),
				slog.Duration("time_drift", time.Since(item.row.CreatedAt)))
		default:
			w.enqueueAggregationTask(item.row.MonitorID, item.row.CreatedAt)
			sentry.NewMeter(context.Background()).WithCtx(ctx).Count("eyrie.monitor.submissions.ingested", 1, sentry.WithAttributes(attribute.String("probe_type", item.probeType), attribute.String("region", item.region)))
			if item.row.LatencyMs > 0 {
				sentry.NewMeter(context.Background()).WithCtx(ctx).Distribution("eyrie.monitor.submissions.ingested_latency", float64(item.row.LatencyMs), sentry.WithUnit(sentry.UnitMillisecond), sentry.WithAttributes(attribute.String("probe_type", item.probeType), attribute.String("region", item.region)))
			}
		}

		item.message.Ack()
	}
}

// runPeriodicAggregation runs the aggregation process every minute.
func (w *IngesterWorker) runPeriodicAggregation() {
	const aggregationInterval = time.Minute
	ticker := time.NewTicker(aggregationInterval)
	defer ticker.Stop()

	w.seedCurrentAggregationTasks()
	w.aggregateDirtyMonitors()

	for {
		select {
		case <-w.shutdown:
			return
		case <-ticker.C:
			w.aggregateDirtyMonitors()
		}
	}
}

func normalizeAggregationDate(date time.Time) time.Time {
	utc := date.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

func aggregationTaskKey(monitorID string, date time.Time) string {
	return fmt.Sprintf("%s|%s", monitorID, normalizeAggregationDate(date).Format("2006-01-02"))
}

func hourAggregationTaskKey(monitorID string, hour time.Time) string {
	return fmt.Sprintf("%s|%s", monitorID, hour.UTC().Truncate(time.Hour).Format(time.RFC3339))
}

// seedCurrentAggregationTasks recomputes only the current hour and current day
// on startup. Missed windows after a longer outage are intentionally not
// backfilled; the service resumes with fresh data.
func (w *IngesterWorker) seedCurrentAggregationTasks() {
	now := time.Now().UTC()
	for _, monitor := range w.monitorConfig.Monitors {
		w.enqueueAggregationTask(monitor.ID, now)
	}
}

func (w *IngesterWorker) enqueueAggregationTask(monitorID string, date time.Time) {
	w.enqueueHourAggregationTask(monitorID, date)
	w.enqueueDayAggregationTask(monitorID, date)
}

func (w *IngesterWorker) enqueueHourAggregationTask(monitorID string, date time.Time) {
	task := hourAggregationTask{
		monitorID: monitorID,
		hour:      date.UTC().Truncate(time.Hour),
	}

	w.dirtyAggregationMu.Lock()
	defer w.dirtyAggregationMu.Unlock()

	if w.dirtyHourAggregationTask == nil {
		w.dirtyHourAggregationTask = make(map[string]hourAggregationTask)
	}
	w.dirtyHourAggregationTask[hourAggregationTaskKey(task.monitorID, task.hour)] = task
}

func (w *IngesterWorker) enqueueDayAggregationTask(monitorID string, date time.Time) {
	task := aggregationTask{
		monitorID: monitorID,
		date:      normalizeAggregationDate(date),
	}

	w.dirtyAggregationMu.Lock()
	defer w.dirtyAggregationMu.Unlock()

	if w.dirtyAggregationTasks == nil {
		w.dirtyAggregationTasks = make(map[string]aggregationTask)
	}
	w.dirtyAggregationTasks[aggregationTaskKey(task.monitorID, task.date)] = task
}

func (w *IngesterWorker) drainHourAggregationTasks() []hourAggregationTask {
	w.dirtyAggregationMu.Lock()
	defer w.dirtyAggregationMu.Unlock()

	if len(w.dirtyHourAggregationTask) == 0 {
		return nil
	}

	tasks := make([]hourAggregationTask, 0, len(w.dirtyHourAggregationTask))
	for _, task := range w.dirtyHourAggregationTask {
		tasks = append(tasks, task)
	}
	clear(w.dirtyHourAggregationTask)

	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].hour.Equal(tasks[j].hour) {
			return tasks[i].monitorID < tasks[j].monitorID
		}
		return tasks[i].hour.Before(tasks[j].hour)
	})

	return tasks
}

func (w *IngesterWorker) drainAggregationTasks() []aggregationTask {
	w.dirtyAggregationMu.Lock()
	defer w.dirtyAggregationMu.Unlock()

	if len(w.dirtyAggregationTasks) == 0 {
		return nil
	}

	tasks := make([]aggregationTask, 0, len(w.dirtyAggregationTasks))
	for _, task := range w.dirtyAggregationTasks {
		tasks = append(tasks, task)
	}
	clear(w.dirtyAggregationTasks)

	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].date.Equal(tasks[j].date) {
			return tasks[i].monitorID < tasks[j].monitorID
		}
		return tasks[i].date.Before(tasks[j].date)
	})

	return tasks
}

// aggregateDirtyMonitors aggregates only monitor/time pairs that changed since
// the last flush. Hourly tasks are processed before daily tasks so daily
// rollups are always derived from up-to-date hourly rows.
func (w *IngesterWorker) aggregateDirtyMonitors() {
	hourTasks := w.drainHourAggregationTasks()
	dayTasks := w.drainAggregationTasks()
	if len(hourTasks) == 0 && len(dayTasks) == 0 {
		return
	}

	ctx := sentry.SetHubOnContext(context.Background(), sentry.CurrentHub().Clone())
	aggregationStartedAt := time.Now()
	span := sentry.StartTransaction(ctx, "ingester.aggregate_dirty", sentry.WithOpName("task.ingester.aggregate"), sentry.WithTransactionSource(sentry.SourceCustom))
	ctx = span.Context()
	defer span.Finish()

	slog.InfoContext(ctx, "starting aggregation", slog.Int("hour_task_count", len(hourTasks)), slog.Int("day_task_count", len(dayTasks)))
	sentry.NewMeter(context.Background()).WithCtx(ctx).Gauge("eyrie.ingester.aggregation.hour_tasks", float64(len(hourTasks)))
	sentry.NewMeter(context.Background()).WithCtx(ctx).Gauge("eyrie.ingester.aggregation.tasks", float64(len(dayTasks)))

	for _, task := range hourTasks {
		if err := w.aggregateHourlyMonitorHistorical(ctx, task.monitorID, task.hour); err != nil {
			w.enqueueHourAggregationTask(task.monitorID, task.hour)
			w.enqueueDayAggregationTask(task.monitorID, task.hour)

			if hub := sentry.GetHubFromContext(ctx); hub != nil {
				hub.Scope().SetTag("eyrie.monitor_id", task.monitorID)
				hub.Scope().SetTag("eyrie.hour", task.hour.Format(time.RFC3339))
				hub.CaptureException(fmt.Errorf("aggregating hourly monitor historical: %w", err))
			}
			slog.ErrorContext(ctx, "aggregating hourly monitor historical",
				slog.String("monitor_id", task.monitorID),
				slog.String("hour", task.hour.Format(time.RFC3339)),
				slog.String("error", err.Error()))
		}
	}

	for _, task := range dayTasks {
		if err := w.aggregateDailyMonitorHistorical(ctx, task.monitorID, task.date); err != nil {
			w.enqueueDayAggregationTask(task.monitorID, task.date)

			if hub := sentry.GetHubFromContext(ctx); hub != nil {
				hub.Scope().SetTag("eyrie.monitor_id", task.monitorID)
				hub.Scope().SetTag("eyrie.date", task.date.Format("2006-01-02"))
				hub.CaptureException(fmt.Errorf("aggregating daily monitor historical: %w", err))
			}
			slog.ErrorContext(ctx, "aggregating daily monitor historical",
				slog.String("monitor_id", task.monitorID),
				slog.String("date", task.date.Format("2006-01-02")),
				slog.String("error", err.Error()))
		}
	}

	sentry.NewMeter(context.Background()).WithCtx(ctx).Distribution("eyrie.ingester.aggregation.duration", float64(time.Since(aggregationStartedAt).Milliseconds()), sentry.WithUnit(sentry.UnitMillisecond))
	slog.InfoContext(ctx, "aggregation completed", slog.Int("hour_task_count", len(hourTasks)), slog.Int("day_task_count", len(dayTasks)))
}

func parseAggregationDate(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02", time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported aggregation date format %q", raw)
}

func (w *IngesterWorker) ingestMonitorHistorical(ctx context.Context, submission CheckerSubmissionRequest, region string) error {
	span := sentry.StartSpan(ctx, "db.write", sentry.WithDescription("Ingest Monitor Historical"), sentry.WithSpanOrigin(sentry.SpanOriginManual))
	ctx = span.Context()
	defer span.Finish()

	row := w.buildHistoricalRow(submission, region)
	outcome, err := w.db.InsertMonitorHistorical(ctx, row, w.datasetConfig.PartitionLookaheadDays)
	if err != nil {
		return fmt.Errorf("inserting monitor historical: %w", err)
	}

	switch outcome {
	case RawRejectedOld, RawRejectedFuture:
		slog.WarnContext(ctx, "rejecting historical submission with time drift",
			slog.String("monitor_id", row.MonitorID),
			slog.String("region", region),
			slog.Time("submitted_at", row.CreatedAt),
			slog.Duration("time_drift", time.Since(row.CreatedAt)))
		return nil
	}

	w.enqueueAggregationTask(row.MonitorID, row.CreatedAt)

	sentry.NewMeter(context.Background()).WithCtx(ctx).Count("eyrie.monitor.submissions.ingested", 1, sentry.WithAttributes(attribute.String("probe_type", row.ProbeType), attribute.String("region", region)))
	if row.LatencyMs > 0 {
		sentry.NewMeter(context.Background()).WithCtx(ctx).Distribution("eyrie.monitor.submissions.ingested_latency", float64(row.LatencyMs), sentry.WithUnit(sentry.UnitMillisecond), sentry.WithAttributes(attribute.String("probe_type", row.ProbeType), attribute.String("region", region)))
	}
	return nil
}

func (w *IngesterWorker) aggregateHourlyMonitorHistorical(ctx context.Context, monitorID string, hour time.Time) error {
	monitorConfig, found := w.findMonitorConfig(monitorID)
	if !found {
		return nil
	}

	hour = hour.UTC().Truncate(time.Hour)
	if err := w.db.AggregateHourly(ctx, monitorID, hour, hour.Add(time.Hour), successCriteriaForMonitor(monitorConfig)); err != nil {
		return fmt.Errorf("aggregating hourly monitor historical: %w", err)
	}
	return nil
}

func (w *IngesterWorker) aggregateDailyMonitorHistorical(ctx context.Context, monitorID string, date time.Time) error {
	span := sentry.StartSpan(ctx, "db.aggregate", sentry.WithDescription("Aggregate Daily Monitor Historical"), sentry.WithSpanOrigin(sentry.SpanOriginManual))
	ctx = span.Context()
	defer span.Finish()

	monitorConfig, found := w.findMonitorConfig(monitorID)
	if !found {
		// Historical data can outlive monitor configuration after a monitor is
		// removed or renamed. Skip stale monitor IDs to avoid noisy periodic errors.
		return nil
	}

	day := utcDayStart(date)
	if err := w.db.AggregateHourly(ctx, monitorID, day, day.AddDate(0, 0, 1), successCriteriaForMonitor(monitorConfig)); err != nil {
		return fmt.Errorf("aggregating hourly monitor historical for day: %w", err)
	}
	if err := w.db.AggregateDaily(ctx, monitorID, day); err != nil {
		return fmt.Errorf("aggregating daily monitor historical: %w", err)
	}
	return nil
}
