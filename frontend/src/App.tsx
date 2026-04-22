import { useEffect, useMemo, useState } from "react";
import { z } from "zod";
import {
  Activity,
  ChevronDown,
  ChevronUp,
  Clock3,
  Globe2,
  MapPin,
  Radar,
  Server,
  ShieldAlert,
  ShieldCheck,
} from "lucide-react";
import { Button } from "./components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./components/ui/collapsible";

const uptimeDataSchema = z.object({
  last_updated: z.coerce.date(),
  monitors: z.array(
    z.object({
      type: z.enum(["single", "group"]),
      id: z.string(),
      name: z.string(),
      description: z.string().nullish(),
      monitors: z.array(
        z.object({
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
        }),
      ),
    }),
  ),
});

const metadataSchema = z.object({
  title: z.string().default("Status Page"),
  show_last_updated: z.boolean().default(true),
  retention_days: z.number(),
  degraded_threshold_minutes: z.number(),
  failure_threshold_minutes: z.number(),
});

const incidentsSchema = z.object({
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
      last_transition_at: z.coerce.date().nullable().catch(null),
      updated_at: z.coerce.date().nullable().catch(null),
    }),
  ),
});

const regionSchema = z.object({
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

type UptimeData = z.infer<typeof uptimeDataSchema>;
type Metadata = z.infer<typeof metadataSchema>;
type IncidentsData = z.infer<typeof incidentsSchema>;
type RegionData = z.infer<typeof regionSchema>;
type SingleMonitor = UptimeData["monitors"][number]["monitors"][number];
type Incident = IncidentsData["incidents"][number];

const BASE_URL = import.meta.env.VITE_BASE_URL ?? "";

const statusTone: Record<string, string> = {
  healthy: "bg-emerald-500/15 text-emerald-300 ring-1 ring-emerald-400/20",
  degraded: "bg-amber-500/15 text-amber-200 ring-1 ring-amber-400/20",
  down: "bg-rose-500/15 text-rose-200 ring-1 ring-rose-400/20",
};

const scopeTone: Record<string, string> = {
  healthy: "bg-slate-500/15 text-slate-200 ring-1 ring-white/10",
  local: "bg-sky-500/15 text-sky-200 ring-1 ring-sky-400/20",
  global: "bg-fuchsia-500/15 text-fuchsia-200 ring-1 ring-fuchsia-400/20",
};

function formatStatus(status: string) {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function formatScope(scope: string) {
  return scope.charAt(0).toUpperCase() + scope.slice(1);
}

function getWorstStatus(statuses: string[]) {
  if (statuses.includes("down")) return "down";
  if (statuses.includes("degraded")) return "degraded";
  return "healthy";
}

function getAvailabilityStatus(durationMinutes: number, metadata: Metadata) {
  if (durationMinutes > metadata.failure_threshold_minutes) return "down";
  if (durationMinutes > metadata.degraded_threshold_minutes) return "degraded";
  return "healthy";
}

function UptimeBars({ monitor, metadata }: { monitor: SingleMonitor; metadata: Metadata }) {
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-1.5">
        {Array.from({ length: metadata.retention_days }).map((_, index) => {
          const colorStartsAt = metadata.retention_days - monitor.age;
          if (index < colorStartsAt) {
            return (
              <div key={index} className="h-8 w-2.5 rounded-full bg-white/10" title="No data" />
            );
          }

          const downtimeIndex = metadata.retention_days - index - 1;
          const downtime = monitor.downtimes[downtimeIndex];
          const downtimeMinutes = downtime?.duration_minutes ?? 0;
          const availabilityStatus = getAvailabilityStatus(downtimeMinutes, metadata);

          let className = "bg-emerald-400";
          let title = "No downtime";
          if (availabilityStatus === "down") {
            className = "bg-rose-400";
            title = `Downtime: ${downtimeMinutes} minutes`;
          } else if (availabilityStatus === "degraded") {
            className = "bg-amber-300";
            title = `Degraded: ${downtimeMinutes} minutes`;
          }

          return (
            <div key={index} className={`h-8 w-2.5 rounded-full ${className}`} title={title} />
          );
        })}
      </div>
      <div className="flex justify-between text-[11px] uppercase tracking-[0.2em] text-slate-400">
        <span>{metadata.retention_days}d ago</span>
        <span>Today</span>
      </div>
    </div>
  );
}

function GroupUptimeBars({
  children,
  metadata,
}: {
  children: SingleMonitor[];
  metadata: Metadata;
}) {
  const dailySummaries = Array.from({ length: metadata.retention_days }).map((_, index) => {
    const summary = {
      healthy: 0,
      degraded: 0,
      down: 0,
      noData: 0,
    };

    for (const child of children) {
      const colorStartsAt = metadata.retention_days - child.age;
      if (index < colorStartsAt) {
        summary.noData += 1;
        continue;
      }

      const downtimeIndex = metadata.retention_days - index - 1;
      const downtimeMinutes = child.downtimes[downtimeIndex]?.duration_minutes ?? 0;
      const availabilityStatus = getAvailabilityStatus(downtimeMinutes, metadata);
      summary[availabilityStatus] += 1;
    }

    return summary;
  });

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-1.5">
        {dailySummaries.map((summary, index) => {
          const total = children.length || 1;
          const title = [
            `Healthy: ${summary.healthy}`,
            `Degraded: ${summary.degraded}`,
            `Down: ${summary.down}`,
            `No data: ${summary.noData}`,
          ].join(" • ");

          return (
            <div
              key={index}
              className="flex h-8 w-3 flex-col overflow-hidden rounded-full bg-white/10"
              title={title}
            >
              {summary.down > 0 && (
                <div className="bg-rose-400" style={{ height: `${(summary.down / total) * 100}%` }} />
              )}
              {summary.degraded > 0 && (
                <div
                  className="bg-amber-300"
                  style={{ height: `${(summary.degraded / total) * 100}%` }}
                />
              )}
              {summary.healthy > 0 && (
                <div
                  className="bg-emerald-400"
                  style={{ height: `${(summary.healthy / total) * 100}%` }}
                />
              )}
              {summary.noData > 0 && (
                <div
                  className="bg-white/15"
                  style={{ height: `${(summary.noData / total) * 100}%` }}
                />
              )}
            </div>
          );
        })}
      </div>
      <div className="flex justify-between text-[11px] uppercase tracking-[0.2em] text-slate-400">
        <span>{metadata.retention_days}d ago</span>
        <span>Today</span>
      </div>
      <div className="flex flex-wrap gap-3 text-xs text-slate-400">
        <span className="inline-flex items-center gap-2">
          <span className="h-2.5 w-2.5 rounded-full bg-emerald-400" />
          Healthy children
        </span>
        <span className="inline-flex items-center gap-2">
          <span className="h-2.5 w-2.5 rounded-full bg-amber-300" />
          Degraded children
        </span>
        <span className="inline-flex items-center gap-2">
          <span className="h-2.5 w-2.5 rounded-full bg-rose-400" />
          Down children
        </span>
        <span className="inline-flex items-center gap-2">
          <span className="h-2.5 w-2.5 rounded-full bg-white/15" />
          No data
        </span>
      </div>
    </div>
  );
}

function CollapsedGroupSummary({
  children,
  metadata,
  childStatusCounts,
  worstStatus,
}: {
  children: SingleMonitor[];
  metadata: Metadata;
  childStatusCounts: Record<"healthy" | "degraded" | "down", number>;
  worstStatus: string;
}) {
  return (
    <div className="mt-6 grid gap-5 border-t border-white/10 pt-6 xl:grid-cols-[1.15fr_0.85fr]">
      <div className="rounded-2xl border border-white/10 bg-slate-900/80 p-4">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <p className="text-xs uppercase tracking-[0.25em] text-slate-500">Group availability</p>
          <span className="text-xs text-slate-400">{children.length} child monitors</span>
        </div>
        <GroupUptimeBars children={children} metadata={metadata} />
      </div>
      <div className="rounded-2xl border border-white/10 bg-slate-900/80 p-4">
        <p className="mb-4 text-xs uppercase tracking-[0.25em] text-slate-500">
          Current child status
        </p>
        <div className="grid gap-3 sm:grid-cols-3">
          <div className="rounded-2xl border border-emerald-400/20 bg-emerald-500/10 px-4 py-3">
            <p className="text-xs uppercase tracking-[0.2em] text-emerald-100">Healthy</p>
            <p className="mt-2 text-2xl font-semibold text-white">{childStatusCounts.healthy}</p>
          </div>
          <div className="rounded-2xl border border-amber-400/20 bg-amber-500/10 px-4 py-3">
            <p className="text-xs uppercase tracking-[0.2em] text-amber-100">Degraded</p>
            <p className="mt-2 text-2xl font-semibold text-white">{childStatusCounts.degraded}</p>
          </div>
          <div className="rounded-2xl border border-rose-400/20 bg-rose-500/10 px-4 py-3">
            <p className="text-xs uppercase tracking-[0.2em] text-rose-100">Down</p>
            <p className="mt-2 text-2xl font-semibold text-white">{childStatusCounts.down}</p>
          </div>
        </div>
        <p className="mt-4 text-sm text-slate-300">
          Worst child status right now:{" "}
          <span className="font-medium text-white">{formatStatus(worstStatus)}</span>
        </p>
      </div>
    </div>
  );
}

function RegionList({
  regions,
  incident,
}: {
  regions: RegionData["monitors"];
  incident?: Incident;
}) {
  const affected = new Set(incident?.affected_regions ?? []);
  return (
    <div className="grid gap-3 md:grid-cols-2">
      {regions.map((region) => {
        const isAffected = affected.has(region.region);
        return (
          <div
            key={region.region}
            className={`rounded-2xl border px-4 py-3 ${isAffected ? "border-rose-400/30 bg-rose-500/10" : "border-white/10 bg-white/5"}`}
          >
            <div className="flex items-center justify-between gap-4">
              <div>
                <p className="text-sm font-semibold text-white">{region.region}</p>
                <p className="text-xs text-slate-400">Avg latency {region.response_time_ms} ms</p>
              </div>
              <span
                className={`rounded-full px-2.5 py-1 text-xs font-medium ${isAffected ? "bg-rose-400/15 text-rose-200" : "bg-emerald-400/15 text-emerald-200"}`}
              >
                {isAffected ? "Affected" : "Healthy"}
              </span>
            </div>
          </div>
        );
      })}
    </div>
  );
}

function MonitorCard({
  monitor,
  metadata,
  incidents,
  regionMap,
}: {
  monitor: UptimeData["monitors"][number];
  metadata: Metadata;
  incidents: Map<string, Incident>;
  regionMap: Record<string, RegionData["monitors"]>;
}) {
  const [isExpanded, setIsExpanded] = useState(true);
  const children = monitor.monitors;
  const isCollapsibleGroup = monitor.type === "group" && children.length > 1;
  const childIncidents = children
    .map((item) => incidents.get(item.id))
    .filter(Boolean) as Incident[];
  const worstStatus = getWorstStatus(childIncidents.map((item) => item.status));
  const globalIssue = childIncidents.find((item) => item.scope === "global");
  const scope =
    globalIssue?.scope ?? childIncidents.find((item) => item.scope === "local")?.scope ?? "healthy";
  const averageLatency =
    children.length > 0
      ? Math.round(children.reduce((sum, item) => sum + item.response_time_ms, 0) / children.length)
      : 0;
  const childStatusCounts = children.reduce<Record<"healthy" | "degraded" | "down", number>>(
    (counts, child) => {
      const status = incidents.get(child.id)?.status ?? "healthy";
      if (status === "down" || status === "degraded") {
        counts[status] += 1;
      } else {
        counts.healthy += 1;
      }
      return counts;
    },
    { healthy: 0, degraded: 0, down: 0 },
  );

  return (
    <section className="rounded-[28px] border border-white/10 bg-slate-950/80 p-6 shadow-[0_30px_80px_-40px_rgba(15,23,42,0.9)] backdrop-blur">
      <Collapsible open={isExpanded} onOpenChange={setIsExpanded}>
        <div className="flex flex-col gap-4 border-b border-white/10 pb-6 lg:flex-row lg:items-start lg:justify-between">
          <div className="space-y-2">
            <div className="flex flex-wrap items-center gap-3">
              <h2 className="text-2xl font-semibold text-white">{monitor.name}</h2>
              <span
                className={`rounded-full px-3 py-1 text-xs font-semibold ${statusTone[worstStatus] ?? statusTone.healthy}`}
              >
                {formatStatus(worstStatus)}
              </span>
              <span
                className={`rounded-full px-3 py-1 text-xs font-semibold ${scopeTone[scope] ?? scopeTone.healthy}`}
              >
                {formatScope(scope)}
              </span>
              {isCollapsibleGroup && (
                <CollapsibleTrigger asChild>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="rounded-full border border-white/10 bg-white/5 px-3 text-xs font-semibold text-slate-200 hover:bg-white/10 hover:text-white"
                  >
                    {isExpanded ? "Collapse" : "Expand"}
                    {isExpanded ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
                  </Button>
                </CollapsibleTrigger>
              )}
            </div>
            {monitor.description && (
              <p className="max-w-3xl text-sm text-slate-300">{monitor.description}</p>
            )}
          </div>
          <div className="grid grid-cols-2 gap-3 text-sm text-slate-300 md:grid-cols-3">
            <div className="rounded-2xl border border-white/10 bg-white/5 px-4 py-3">
              <p className="text-xs uppercase tracking-[0.2em] text-slate-500">Latency</p>
              <p className="mt-2 text-lg font-semibold text-white">{averageLatency} ms</p>
            </div>
            <div className="rounded-2xl border border-white/10 bg-white/5 px-4 py-3">
              <p className="text-xs uppercase tracking-[0.2em] text-slate-500">Monitors</p>
              <p className="mt-2 text-lg font-semibold text-white">{children.length}</p>
            </div>
            <div className="rounded-2xl border border-white/10 bg-white/5 px-4 py-3">
              <p className="text-xs uppercase tracking-[0.2em] text-slate-500">Scope</p>
              <p className="mt-2 text-lg font-semibold text-white">{formatScope(scope)}</p>
            </div>
          </div>
        </div>

        {isCollapsibleGroup && !isExpanded && (
          <CollapsedGroupSummary
            children={children}
            metadata={metadata}
            childStatusCounts={childStatusCounts}
            worstStatus={worstStatus}
          />
        )}

        <CollapsibleContent>
          <div className="mt-6 grid gap-5">
            {children.map((child) => {
              const incident = incidents.get(child.id);
              const regions = regionMap[child.id] ?? [];
              return (
                <article
                  key={child.id}
                  className="rounded-3xl border border-white/10 bg-white/[0.03] p-5"
                >
                  <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
                    <div className="space-y-3">
                      <div className="flex flex-wrap items-center gap-3">
                        <h3 className="text-lg font-semibold text-white">{child.name}</h3>
                        <span
                          className={`rounded-full px-3 py-1 text-xs font-semibold ${statusTone[incident?.status ?? "healthy"] ?? statusTone.healthy}`}
                        >
                          {formatStatus(incident?.status ?? "healthy")}
                        </span>
                        <span
                          className={`rounded-full px-3 py-1 text-xs font-semibold ${scopeTone[incident?.scope ?? "healthy"] ?? scopeTone.healthy}`}
                        >
                          {formatScope(incident?.scope ?? "healthy")}
                        </span>
                        <span className="rounded-full bg-white/5 px-3 py-1 text-xs font-semibold uppercase tracking-[0.2em] text-slate-300">
                          {incident?.probe_type ?? "http"}
                        </span>
                      </div>
                      {child.description && (
                        <p className="text-sm text-slate-300">{child.description}</p>
                      )}
                      <div className="flex flex-wrap gap-4 text-sm text-slate-300">
                        <span className="inline-flex items-center gap-2">
                          <Activity className="h-4 w-4 text-emerald-300" />
                          {child.response_time_ms} ms
                        </span>
                        <span className="inline-flex items-center gap-2">
                          <MapPin className="h-4 w-4 text-sky-300" />
                          {regions.length} regions
                        </span>
                        <span className="inline-flex items-center gap-2">
                          <Clock3 className="h-4 w-4 text-violet-300" />
                          {child.age} days tracked
                        </span>
                      </div>
                    </div>
                    <div className="max-w-xl space-y-2 text-sm text-slate-300">
                      <p className="font-medium text-white">
                        {incident?.reason || "No active incident."}
                      </p>
                      {incident?.affected_regions.length ? (
                        <p>Affected regions: {incident.affected_regions.join(", ")}</p>
                      ) : (
                        <p>All reporting regions are healthy.</p>
                      )}
                    </div>
                  </div>

                  <div className="mt-5 grid gap-5 xl:grid-cols-[1.15fr_0.85fr]">
                    <div className="rounded-2xl border border-white/10 bg-slate-900/80 p-4">
                      <p className="mb-4 text-xs uppercase tracking-[0.25em] text-slate-500">
                        Availability trend
                      </p>
                      <UptimeBars monitor={child} metadata={metadata} />
                    </div>
                    <div className="rounded-2xl border border-white/10 bg-slate-900/80 p-4">
                      <p className="mb-4 text-xs uppercase tracking-[0.25em] text-slate-500">
                        Regional health
                      </p>
                      <RegionList regions={regions} incident={incident} />
                    </div>
                  </div>
                </article>
              );
            })}
          </div>
        </CollapsibleContent>
      </Collapsible>
    </section>
  );
}

function App() {
  const [data, setData] = useState<UptimeData | null>(null);
  const [metadata, setMetadata] = useState<Metadata | null>(null);
  const [incidents, setIncidents] = useState<IncidentsData | null>(null);
  const [regionMap, setRegionMap] = useState<Record<string, RegionData["monitors"]>>({});
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    Promise.all([
      fetch(`${BASE_URL}/uptime-data`).then((response) => response.json()),
      fetch(`${BASE_URL}/config`).then((response) => response.json()),
      fetch(`${BASE_URL}/monitor-incidents`).then((response) => response.json()),
    ])
      .then(([uptimeJson, configJson, incidentsJson]) => {
        const parsedData = uptimeDataSchema.parse(uptimeJson);
        const parsedMetadata = metadataSchema.parse(configJson);
        const parsedIncidents = incidentsSchema.parse(incidentsJson);

        setData(parsedData);
        setMetadata(parsedMetadata);
        setIncidents(parsedIncidents);
        document.title = parsedMetadata.title;

        const uniqueMonitorIds = Array.from(
          new Set(
            parsedData.monitors.flatMap((group) => group.monitors.map((monitor) => monitor.id)),
          ),
        );

        return Promise.all(
          uniqueMonitorIds.map((monitorId) =>
            fetch(`${BASE_URL}/uptime-data-by-region?monitorId=${encodeURIComponent(monitorId)}`)
              .then((response) => response.json())
              .then((json) => ({ monitorId, payload: regionSchema.parse(json) })),
          ),
        );
      })
      .then((regions) => {
        const nextMap: Record<string, RegionData["monitors"]> = {};
        for (const item of regions) {
          nextMap[item.monitorId] = item.payload.monitors;
        }
        setRegionMap(nextMap);
      })
      .catch((caught) => {
        setError(caught instanceof Error ? caught.message : "Failed to load status data");
      });
  }, []);

  const incidentById = useMemo(() => {
    const map = new Map<string, Incident>();
    for (const incident of incidents?.incidents ?? []) {
      map.set(incident.monitor_id, incident);
    }
    return map;
  }, [incidents]);

  const stats = useMemo(() => {
    const values = incidents?.incidents ?? [];
    return {
      healthy: values.filter((incident) => incident.status === "healthy").length,
      degraded: values.filter((incident) => incident.status === "degraded").length,
      down: values.filter((incident) => incident.status === "down").length,
      global: values.filter((incident) => incident.scope === "global").length,
      local: values.filter((incident) => incident.scope === "local").length,
    };
  }, [incidents]);

  if (error) {
    return (
      <div className="min-h-screen bg-slate-950 px-6 py-10 text-white">
        <div className="mx-auto max-w-3xl rounded-3xl border border-rose-400/20 bg-rose-500/10 p-8">
          <h1 className="text-2xl font-semibold">Unable to load status data</h1>
          <p className="mt-3 text-slate-200">{error}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-[radial-gradient(circle_at_top,_rgba(56,189,248,0.18),_transparent_28%),linear-gradient(180deg,#020617_0%,#0f172a_45%,#020617_100%)] px-5 py-8 text-white lg:px-8">
      <div className="mx-auto max-w-7xl space-y-8">
        <header className="rounded-[32px] border border-white/10 bg-slate-950/75 p-8 shadow-[0_40px_100px_-40px_rgba(15,23,42,1)] backdrop-blur">
          <div className="flex flex-col gap-8 xl:flex-row xl:items-end xl:justify-between">
            <div className="space-y-4">
              <div className="inline-flex items-center gap-2 rounded-full bg-sky-400/10 px-4 py-2 text-xs font-semibold uppercase tracking-[0.3em] text-sky-200">
                <Radar className="h-4 w-4" />
                Eyrie multi-region status
              </div>
              <div>
                <h1 className="text-4xl font-semibold tracking-tight lg:text-5xl">
                  {metadata?.title ?? "Status Page"}
                </h1>
                <p className="mt-3 max-w-3xl text-base text-slate-300">
                  A region-aware overview of your monitors with local-vs-global incident context,
                  protocol coverage, and drilldowns for every reporting checker.
                </p>
              </div>
            </div>
            {metadata?.show_last_updated && (
              <div className="rounded-2xl border border-white/10 bg-white/5 px-5 py-4 text-sm text-slate-300">
                <p className="text-xs uppercase tracking-[0.2em] text-slate-500">Last updated</p>
                <p className="mt-2 font-medium text-white">
                  {data?.last_updated.toLocaleString(undefined, {
                    dateStyle: "long",
                    timeStyle: "medium",
                  }) ?? "Loading..."}
                </p>
              </div>
            )}
          </div>

          <div className="mt-8 grid gap-4 md:grid-cols-2 xl:grid-cols-5">
            <div className="rounded-3xl border border-emerald-400/20 bg-emerald-500/10 p-5">
              <div className="inline-flex items-center gap-2 text-sm font-medium text-emerald-200">
                <ShieldCheck className="h-4 w-4" />
                Healthy
              </div>
              <p className="mt-4 text-4xl font-semibold text-white">{stats.healthy}</p>
            </div>
            <div className="rounded-3xl border border-amber-400/20 bg-amber-500/10 p-5">
              <div className="inline-flex items-center gap-2 text-sm font-medium text-amber-100">
                <Activity className="h-4 w-4" />
                Degraded
              </div>
              <p className="mt-4 text-4xl font-semibold text-white">{stats.degraded}</p>
            </div>
            <div className="rounded-3xl border border-rose-400/20 bg-rose-500/10 p-5">
              <div className="inline-flex items-center gap-2 text-sm font-medium text-rose-100">
                <ShieldAlert className="h-4 w-4" />
                Down
              </div>
              <p className="mt-4 text-4xl font-semibold text-white">{stats.down}</p>
            </div>
            <div className="rounded-3xl border border-fuchsia-400/20 bg-fuchsia-500/10 p-5">
              <div className="inline-flex items-center gap-2 text-sm font-medium text-fuchsia-100">
                <Globe2 className="h-4 w-4" />
                Global incidents
              </div>
              <p className="mt-4 text-4xl font-semibold text-white">{stats.global}</p>
            </div>
            <div className="rounded-3xl border border-sky-400/20 bg-sky-500/10 p-5">
              <div className="inline-flex items-center gap-2 text-sm font-medium text-sky-100">
                <Server className="h-4 w-4" />
                Local incidents
              </div>
              <p className="mt-4 text-4xl font-semibold text-white">{stats.local}</p>
            </div>
          </div>
        </header>

        <main className="space-y-6">
          {data && metadata ? (
            data.monitors.map((monitor) => (
              <MonitorCard
                key={monitor.id}
                monitor={monitor}
                metadata={metadata}
                incidents={incidentById}
                regionMap={regionMap}
              />
            ))
          ) : (
            <div className="rounded-3xl border border-white/10 bg-slate-950/70 p-8 text-slate-300">
              Loading monitor data...
            </div>
          )}
        </main>
      </div>
    </div>
  );
}

export default App;
