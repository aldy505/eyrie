import { z } from "zod";

const singleMonitorSchema = z.object({
  id: z.string(),
  name: z.string(),
  description: z.string().nullish(),
  response_time_ms: z.number(),
  age: z.number(),
  downtimes: z.record(
    z.string(),
    z.object({
      duration_minutes: z.number(),
    }),
  ),
});

export const uptimeDataSchema = z.object({
  last_updated: z.coerce.date(),
  monitors: z.array(
    z.object({
      type: z.enum(["single", "group"]),
      id: z.string(),
      name: z.string(),
      description: z.string().nullish(),
      monitors: z.array(singleMonitorSchema.nullable()),
    }),
  ),
});

export const metadataSchema = z.object({
  title: z.string().default("Status Page"),
  show_last_updated: z.boolean().default(true),
  retention_days: z.number(),
  degraded_threshold_minutes: z.number(),
  failure_threshold_minutes: z.number(),
});

export const incidentsSchema = z.object({
  last_updated: z.coerce.date(),
  incidents: z.array(
    z.object({
      monitor_id: z.string(),
      name: z.string(),
      probe_type: z.string(),
      status: z.string(),
      scope: z.string(),
      reason: z.string(),
      affected_regions: z.array(z.string()),
      failure_reasons_breakdown: z.record(z.string(), z.array(z.string())).optional(),
      last_transition_at: z.coerce.date().nullable().catch(null),
      updated_at: z.coerce.date().nullable().catch(null),
    }),
  ),
});

export const regionSchema = z.object({
  last_updated: z.coerce.date(),
  metadata: z.object({
    name: z.string(),
    description: z.string().nullish(),
  }),
  monitors: z.array(
    z.object({
      region: z.string(),
      response_time_ms: z.number(),
      age: z.number(),
      downtimes: z.record(
        z.string(),
        z.object({
          duration_minutes: z.number(),
        }),
      ),
    }),
  ),
});

type RawUptimeData = z.infer<typeof uptimeDataSchema>;
export type Metadata = z.infer<typeof metadataSchema>;
export type IncidentsData = z.infer<typeof incidentsSchema>;
export type RegionData = z.infer<typeof regionSchema>;
export type SingleMonitor = z.infer<typeof singleMonitorSchema>;
export type UptimeData = {
  last_updated: RawUptimeData["last_updated"];
  monitors: Array<{
    type: RawUptimeData["monitors"][number]["type"];
    id: string;
    name: string;
    description?: string | null;
    monitors: SingleMonitor[];
  }>;
};
export type MonitorGroup = UptimeData["monitors"][number];
export type Incident = IncidentsData["incidents"][number];
export type AvailabilityStatus = "healthy" | "degraded" | "down";
export type DashboardLayoutMode = "classic" | "grid";

export type SummaryStats = {
  healthy: number;
  degraded: number;
  down: number;
  incidents: number;
};

export type ServiceEntry = {
  parent: MonitorGroup;
  monitor: SingleMonitor;
  incident?: Incident;
  regions: RegionData["monitors"];
  groupLabel: string;
  isStandalone: boolean;
};

export const BASE_URL = import.meta.env.VITE_BASE_URL ?? "";
const MINUTES_PER_DAY = 24 * 60;

const ROSE_400: [number, number, number] = [251, 113, 133];
const AMBER_300: [number, number, number] = [252, 211, 77];
const EMERALD_400: [number, number, number] = [52, 211, 153];

export const statusTone: Record<string, string> = {
  healthy: "bg-emerald-500/15 text-emerald-300 ring-1 ring-emerald-400/20",
  degraded: "bg-amber-500/15 text-amber-200 ring-1 ring-amber-400/20",
  down: "bg-rose-500/15 text-rose-200 ring-1 ring-rose-400/20",
};

export const scopeTone: Record<string, string> = {
  healthy: "bg-slate-500/15 text-slate-200 ring-1 ring-white/10",
  local: "bg-sky-500/15 text-sky-200 ring-1 ring-sky-400/20",
  global: "bg-fuchsia-500/15 text-fuchsia-200 ring-1 ring-fuchsia-400/20",
};

export function normalizeUptimeData(data: RawUptimeData): UptimeData {
  return {
    last_updated: data.last_updated,
    monitors: data.monitors.map((group) => ({
      ...group,
      monitors: group.monitors.filter((monitor): monitor is SingleMonitor => monitor !== null),
    })),
  };
}

export function formatStatus(status: string) {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

export function formatScope(scope: string) {
  return scope.charAt(0).toUpperCase() + scope.slice(1);
}

export function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}

export function getWorstStatus(statuses: string[]) {
  if (statuses.includes("down")) return "down";
  if (statuses.includes("degraded")) return "degraded";
  return "healthy";
}

export function getAvailabilityStatus(
  durationMinutes: number,
  metadata: Metadata,
): AvailabilityStatus {
  if (durationMinutes > metadata.failure_threshold_minutes) return "down";
  if (durationMinutes > metadata.degraded_threshold_minutes) return "degraded";
  return "healthy";
}

export function getAvailabilityRatio(durationMinutes: number) {
  const downtime = clamp(durationMinutes, 0, MINUTES_PER_DAY);
  return 1 - downtime / MINUTES_PER_DAY;
}

function interpolateRgbColor(
  startColor: [number, number, number],
  endColor: [number, number, number],
  ratio: number,
) {
  const clampedRatio = clamp(ratio, 0, 1);
  const [red, green, blue] = startColor.map((channel, index) =>
    Math.round(channel + (endColor[index] - channel) * clampedRatio),
  );
  return `rgb(${red} ${green} ${blue})`;
}

export function getAvailabilityBarColor(durationMinutes: number) {
  return interpolateRgbColor(ROSE_400, EMERALD_400, getAvailabilityRatio(durationMinutes));
}

export function getStatusBarColor(status: AvailabilityStatus) {
  switch (status) {
    case "down":
      return `rgb(${ROSE_400.join(" ")})`;
    case "degraded":
      return `rgb(${AMBER_300.join(" ")})`;
    default:
      return `rgb(${EMERALD_400.join(" ")})`;
  }
}

export function formatAvailability(availabilityRatio: number) {
  const percentage = clamp(availabilityRatio * 100, 0, 100);
  if (percentage === 100) {
    return "100%";
  }

  return `${percentage.toFixed(2)}%`;
}

export function formatLatency(latencyMs: number) {
  return `~${Math.round(latencyMs)}ms`;
}

export function formatDaysAgo(days: number) {
  return `${days} day${days === 1 ? "" : "s"} ago`;
}

export function formatDowntime(durationMinutes: number) {
  if (durationMinutes === 0) {
    return "0 minutes";
  }

  const hours = Math.floor(durationMinutes / 60);
  const minutes = durationMinutes % 60;

  if (hours === 0) {
    return `${minutes} minute${minutes === 1 ? "" : "s"}`;
  }

  if (minutes === 0) {
    return `${hours} hour${hours === 1 ? "" : "s"}`;
  }

  return `${hours} hour${hours === 1 ? "" : "s"} ${minutes} minute${minutes === 1 ? "" : "s"}`;
}

export function friendlyFailureReasonName(category: string): string {
  switch (category) {
    case "network_unreachable":
      return "Network Unreachable";
    case "connection_refused":
      return "Connection Refused";
    case "timeout":
      return "Timeout";
    case "tls_error":
      return "TLS/Certificate Error";
    case "auth_error":
      return "Authentication Error";
    case "dns_error":
      return "DNS Error";
    case "http_error":
      return "HTTP Error";
    case "generic_error":
      return "Generic Error";
    default:
      return category;
  }
}

export function formatFailureReasonsBreakdown(
  breakdown: Record<string, string[]> | undefined,
): string | null {
  if (!breakdown || Object.keys(breakdown).length === 0) {
    return null;
  }

  return Object.entries(breakdown)
    .map(
      ([category, regions]) =>
        `${friendlyFailureReasonName(category)} (${regions.length} region${regions.length > 1 ? "s" : ""})`,
    )
    .sort()
    .join(" • ");
}

export function getSummaryStats(incidents?: IncidentsData | null): SummaryStats {
  const values = incidents?.incidents ?? [];
  const degraded = values.filter((incident) => incident.status === "degraded").length;
  const down = values.filter((incident) => incident.status === "down").length;

  return {
    healthy: values.filter((incident) => incident.status === "healthy").length,
    degraded,
    down,
    incidents: degraded + down,
  };
}

export function flattenServices(
  monitors: UptimeData["monitors"],
  incidents: Map<string, Incident>,
  regionMap: Record<string, RegionData["monitors"]>,
): ServiceEntry[] {
  return monitors.flatMap((parent) =>
    parent.monitors.map((monitor) => ({
      parent,
      monitor,
      incident: incidents.get(monitor.id),
      regions: regionMap[monitor.id] ?? [],
      groupLabel: parent.type === "group" ? parent.name : "Standalone service",
      isStandalone: parent.type === "single",
    })),
  );
}
