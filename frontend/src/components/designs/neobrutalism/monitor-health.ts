import type {
  AvailabilityStatus,
  Metadata,
  RegionData,
  SingleMonitor,
} from "@/lib/status-dashboard";
import { getAvailabilityStatus } from "@/lib/status-dashboard";
import { getTodayDowntimeMinutes, getRegionStatus } from "@/lib/design-config";

export const TIMEOUT_RESPONSE_TIME_MS = 30_000;

export type MonitorHealth = {
  total: number;
  healthy: number;
  worstStatus: AvailabilityStatus;
  worstLatencyMs: number;
  isDegraded: boolean;
};

function worseStatus(a: AvailabilityStatus, b: AvailabilityStatus): AvailabilityStatus {
  if (a === "down" || b === "down") return "down";
  if (a === "degraded" || b === "degraded") return "degraded";
  return "healthy";
}

export function summarizeMonitor(
  monitor: SingleMonitor,
  regions: RegionData["monitors"],
  metadata: Metadata,
): MonitorHealth {
  const probes =
    regions.length > 0
      ? regions.map((region) => ({
          latencyMs: region.response_time_ms,
          downtimeMinutes: getTodayDowntimeMinutes(region),
          status: getRegionStatus(region, metadata),
        }))
      : [
          {
            latencyMs: monitor.response_time_ms,
            downtimeMinutes: monitor.downtimes["0"]?.duration_minutes ?? 0,
            status: getAvailabilityStatus(monitor.downtimes["0"]?.duration_minutes ?? 0, metadata),
          },
        ];

  let healthy = 0;
  let worstStatus: AvailabilityStatus = "healthy";
  let worstLatencyMs = 0;
  let isDegraded = false;

  for (const probe of probes) {
    const timedOut = probe.latencyMs >= TIMEOUT_RESPONSE_TIME_MS;
    // A probe is only OK at 100% availability: no downtime today and no timeout.
    const status: AvailabilityStatus = timedOut
      ? "down"
      : probe.status === "down"
        ? "down"
        : probe.downtimeMinutes > 0
          ? "degraded"
          : "healthy";
    if (status === "healthy") healthy++;
    if (status !== "healthy") isDegraded = true;
    worstStatus = worseStatus(worstStatus, status);
    worstLatencyMs = Math.max(worstLatencyMs, probe.latencyMs);
  }

  return {
    total: probes.length,
    healthy,
    worstStatus,
    worstLatencyMs,
    isDegraded,
  };
}
