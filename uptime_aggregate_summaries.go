package main

import (
	"slices"
	"strings"
	"time"
)

const uptimeDataCacheTTL = 15 * time.Minute

func utcDayStart(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func daysAgo(now time.Time, date time.Time) int {
	return int(utcDayStart(now).Sub(utcDayStart(date)) / (24 * time.Hour))
}

func summarizeUptimeHistoricalAggregates(rows []MonitorHistoricalDailyAggregate) UptimeDataHistorical {
	summary := UptimeDataHistorical{
		DailyDowntimes: make(map[int]struct {
			DurationMinutes int `json:"duration_minutes"`
		}),
	}

	var totalLatency int64
	now := time.Now().UTC()
	for _, row := range rows {
		summary.MonitorAge++
		totalLatency += int64(row.AvgLatencyMs)
		summary.DailyDowntimes[daysAgo(now, row.Date)] = struct {
			DurationMinutes int `json:"duration_minutes"`
		}{
			DurationMinutes: (100 - row.SuccessRate) * 1440 / 100,
		}
	}

	if summary.MonitorAge > 0 {
		summary.LatencyMs = totalLatency / int64(summary.MonitorAge)
	}

	return summary
}

func summarizeRegionHistoricalAggregates(rows []MonitorHistoricalRegionDailyAggregate) []UptimeDataByRegionMonitor {
	monitorsByRegion := make(map[string]*UptimeDataByRegionMonitor)
	now := time.Now().UTC()

	for _, row := range rows {
		monitor, ok := monitorsByRegion[row.Region]
		if !ok {
			monitor = &UptimeDataByRegionMonitor{
				Region: row.Region,
				Downtimes: make(map[int]struct {
					DurationMinutes int `json:"duration_minutes"`
				}),
			}
			monitorsByRegion[row.Region] = monitor
		}

		monitor.Age++
		monitor.ResponseTimeMs += int64(row.AvgLatencyMs)
		monitor.Downtimes[daysAgo(now, row.Date)] = struct {
			DurationMinutes int `json:"duration_minutes"`
		}{
			DurationMinutes: (100 - row.SuccessRate) * 1440 / 100,
		}
	}

	monitors := make([]UptimeDataByRegionMonitor, 0, len(monitorsByRegion))
	for _, monitor := range monitorsByRegion {
		if monitor.Age > 0 {
			monitor.ResponseTimeMs /= int64(monitor.Age)
		}
		monitors = append(monitors, *monitor)
	}

	slices.SortStableFunc(monitors, func(a, b UptimeDataByRegionMonitor) int {
		return strings.Compare(a.Region, b.Region)
	})

	return monitors
}

type successPredicate func(statusCode int, explicitSuccess bool) bool

func (p successPredicate) IsSuccessfulStatus(statusCode int, explicitSuccess bool) bool {
	return p(statusCode, explicitSuccess)
}

func resolveSuccessPredicate(successSource []any) func(int, bool) bool {
	if len(successSource) == 0 || successSource[0] == nil {
		return func(_ int, explicitSuccess bool) bool { return explicitSuccess }
	}

	switch value := successSource[0].(type) {
	case Monitor:
		return value.IsSuccessfulStatus
	case *Monitor:
		if value == nil {
			return func(_ int, explicitSuccess bool) bool { return explicitSuccess }
		}
		return value.IsSuccessfulStatus
	case successPredicate:
		return value
	case func(int, bool) bool:
		return value
	default:
		return func(_ int, explicitSuccess bool) bool { return explicitSuccess }
	}
}

func summarizeUptimeRegionHistoricalRows(regionData map[string][]MonitorHistorical, successSource ...any) []UptimeDataByRegionMonitor {
	monitors := make([]UptimeDataByRegionMonitor, 0, len(regionData))
	now := time.Now().UTC()
	isSuccessful := resolveSuccessPredicate(successSource)

	for region, historicals := range regionData {
		dailyBucket := make(map[time.Time][]MonitorHistorical)
		for _, mh := range historicals {
			year, month, day := mh.CreatedAt.Date()
			bucketDate := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
			dailyBucket[bucketDate] = append(dailyBucket[bucketDate], mh)
		}

		regionMonitor := UptimeDataByRegionMonitor{
			Region: region,
			Downtimes: make(map[int]struct {
				DurationMinutes int `json:"duration_minutes"`
			}),
		}

		for date, dayHistoricals := range dailyBucket {
			var totalLatency int64
			var totalChecks int64
			var downtimeMinutes int

			for _, mh := range dayHistoricals {
				totalLatency += int64(mh.LatencyMs)
				totalChecks++
				if !isSuccessful(mh.StatusCode, mh.Success) {
					downtimeMinutes += 1
				}
			}

			if totalChecks == 0 {
				continue
			}

			regionMonitor.ResponseTimeMs += totalLatency / totalChecks
			regionMonitor.Downtimes[daysAgo(now, date)] = struct {
				DurationMinutes int `json:"duration_minutes"`
			}{
				DurationMinutes: downtimeMinutes,
			}
			regionMonitor.Age++
		}

		if regionMonitor.Age > 0 {
			regionMonitor.ResponseTimeMs /= int64(regionMonitor.Age)
		}

		monitors = append(monitors, regionMonitor)
	}

	slices.SortStableFunc(monitors, func(a, b UptimeDataByRegionMonitor) int {
		return strings.Compare(a.Region, b.Region)
	})

	return monitors
}
