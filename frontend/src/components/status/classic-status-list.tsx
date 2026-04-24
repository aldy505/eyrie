import { useState } from "react";
import {
  Activity,
  ChevronDown,
  ChevronUp,
  Clock3,
  Globe2,
  MapPin,
  Server,
} from "lucide-react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import type {
  AvailabilityStatus,
  Incident,
  Metadata,
  RegionData,
  SingleMonitor,
  UptimeData,
} from "@/lib/status-dashboard";
import {
  formatAvailability,
  formatFailureReasonsBreakdown,
  formatScope,
  formatStatus,
  friendlyFailureReasonName,
  getAvailabilityBarColor,
  getAvailabilityRatio,
  getAvailabilityStatus,
  getWorstStatus,
  scopeTone,
  statusTone,
} from "@/lib/status-dashboard";

function FailureReasonsBreakdown({ incident }: { incident: Incident }) {
  const breakdown = incident.failure_reasons_breakdown as Record<string, string[]> | undefined;
  const summaryText = formatFailureReasonsBreakdown(breakdown);

  if (!summaryText || !breakdown) {
    return null;
  }

  const categories = Object.entries(breakdown).sort(([a], [b]) => a.localeCompare(b));

  return (
    <Collapsible className="mt-3 space-y-2">
      <CollapsibleTrigger className="group flex items-center gap-2 text-sm text-slate-300 transition-colors hover:text-white">
        <ChevronDown className="h-4 w-4 transition-transform group-data-[state=open]:rotate-180" />
        <span>Failure details: {summaryText}</span>
      </CollapsibleTrigger>
      <CollapsibleContent className="ml-6 space-y-2 text-sm text-slate-400">
        {categories.map(([category, regions]) => (
          <div key={category}>
            <p className="font-medium text-slate-300">{friendlyFailureReasonName(category)}</p>
            <p className="ml-2 text-xs">{regions.join(", ")}</p>
          </div>
        ))}
      </CollapsibleContent>
    </Collapsible>
  );
}

function ClassicUptimeBars({ monitor, metadata }: { monitor: SingleMonitor; metadata: Metadata }) {
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-1.5">
        {Array.from({ length: metadata.retention_days }).map((_, index) => {
          const colorStartsAt = metadata.retention_days - monitor.age;
          if (index < colorStartsAt) {
            return <div key={index} className="h-8 w-2.5 rounded-full bg-white/10" title="No data" />;
          }

          const downtimeIndex = metadata.retention_days - index - 1;
          const downtimeMinutes = monitor.downtimes[downtimeIndex]?.duration_minutes ?? 0;
          const availabilityRatio = getAvailabilityRatio(downtimeMinutes);
          const availabilityStatus = getAvailabilityStatus(downtimeMinutes, metadata);
          const backgroundColor = getAvailabilityBarColor(downtimeMinutes);
          const title = [
            formatStatus(availabilityStatus),
            `Availability: ${formatAvailability(availabilityRatio)}`,
            `Downtime: ${downtimeMinutes} minutes`,
          ].join(" • ");

          return (
            <div
              key={index}
              className="h-8 w-2.5 rounded-full"
              style={{ backgroundColor }}
              title={title}
            />
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
  childMonitors,
  metadata,
}: {
  childMonitors: SingleMonitor[];
  metadata: Metadata;
}) {
  const dailySummaries = Array.from({ length: metadata.retention_days }).map((_, index) => {
    const summary = {
      healthy: 0,
      degraded: 0,
      down: 0,
      noData: 0,
    };

    for (const child of childMonitors) {
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
          const total = childMonitors.length || 1;
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
  childMonitors,
  metadata,
  childStatusCounts,
  worstStatus,
}: {
  childMonitors: SingleMonitor[];
  metadata: Metadata;
  childStatusCounts: Record<AvailabilityStatus, number>;
  worstStatus: string;
}) {
  return (
    <div className="mt-6 grid gap-5 border-t border-white/10 pt-6 xl:grid-cols-[1.15fr_0.85fr]">
      <div className="rounded-2xl border border-white/10 bg-slate-900/80 p-4">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <p className="text-xs uppercase tracking-[0.25em] text-slate-500">Group availability</p>
          <span className="text-xs text-slate-400">{childMonitors.length} child monitors</span>
        </div>
        <GroupUptimeBars childMonitors={childMonitors} metadata={metadata} />
      </div>
      <div className="rounded-2xl border border-white/10 bg-slate-900/80 p-4">
        <p className="mb-4 text-xs uppercase tracking-[0.25em] text-slate-500">Current child status</p>
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
          Worst child status right now: <span className="font-medium text-white">{formatStatus(worstStatus)}</span>
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
    <div className="region-list-scrollbar flex gap-3 overflow-x-auto pb-2 pr-1">
      {regions.map((region) => {
        const isAffected = affected.has(region.region);
        return (
          <div
            key={region.region}
            className={`min-w-[180px] shrink-0 rounded-2xl border px-4 py-3 ${isAffected ? "border-rose-400/30 bg-rose-500/10" : "border-white/10 bg-white/5"}`}
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

function ClassicMonitorCard({
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
  const [isExpanded, setIsExpanded] = useState(false);
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
  const childStatusCounts = children.reduce<Record<AvailabilityStatus, number>>(
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
  const detailContent = (
    <div className="mt-6 grid gap-5">
      {children.map((child) => {
        const incident = incidents.get(child.id);
        const regions = regionMap[child.id] ?? [];
        return (
          <article
            key={child.id}
            className="min-w-0 overflow-hidden rounded-3xl border border-white/10 bg-white/[0.03] p-5"
          >
            <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between lg:gap-6">
              <div className="min-w-0 flex-1 space-y-3">
                <div className="flex flex-wrap items-center gap-3">
                  <h3 className="break-words text-lg font-semibold text-white">{child.name}</h3>
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
                {child.description && <p className="break-words text-sm text-slate-300">{child.description}</p>}
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
              <div className="min-w-0 w-full space-y-2 text-sm text-slate-300 lg:max-w-xl lg:flex-1">
                <p className="break-words font-medium text-white">{incident?.reason || "No active incident."}</p>
                {incident?.affected_regions.length ? (
                  <p className="break-words">Affected regions: {incident.affected_regions.join(", ")}</p>
                ) : (
                  <p>All reporting regions are healthy.</p>
                )}
                {incident && <FailureReasonsBreakdown incident={incident} />}
              </div>
            </div>

            <div className="mt-5 flex min-w-0 flex-col gap-5">
              <div className="min-w-0 rounded-2xl border border-white/10 bg-slate-900/80 p-4">
                <p className="mb-4 text-xs uppercase tracking-[0.25em] text-slate-500">
                  Availability trend
                </p>
                <ClassicUptimeBars monitor={child} metadata={metadata} />
              </div>
              <div className="min-w-0 rounded-2xl border border-white/10 bg-slate-900/80 p-4">
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
                <CollapsibleTrigger className="inline-flex items-center gap-2 rounded-full border border-white/10 bg-white/5 px-3 py-2 text-xs font-semibold text-slate-200 transition-colors hover:bg-white/10 hover:text-white">
                  {isExpanded ? "Collapse" : "Expand"}
                  {isExpanded ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
                </CollapsibleTrigger>
              )}
            </div>
            {monitor.description && <p className="max-w-3xl text-sm text-slate-300">{monitor.description}</p>}
          </div>
          <div className="flex flex-wrap items-center gap-4 text-sm text-slate-400">
            <span className="inline-flex items-center gap-1.5">
              <Activity className="h-3.5 w-3.5 text-emerald-300" />
              <span className="font-medium text-white">{averageLatency} ms</span>
            </span>
            <span className="text-white/20">•</span>
            <span className="inline-flex items-center gap-1.5">
              <Server className="h-3.5 w-3.5 text-sky-300" />
              <span className="font-medium text-white">{children.length}</span>
              <span>monitors</span>
            </span>
            <span className="text-white/20">•</span>
            <span className="inline-flex items-center gap-1.5">
              <Globe2 className="h-3.5 w-3.5 text-fuchsia-300" />
              <span className="font-medium text-white">{formatScope(scope)}</span>
            </span>
          </div>
        </div>

        {isCollapsibleGroup && !isExpanded && (
          <CollapsedGroupSummary
            childMonitors={children}
            metadata={metadata}
            childStatusCounts={childStatusCounts}
            worstStatus={worstStatus}
          />
        )}

        {isCollapsibleGroup ? <CollapsibleContent>{detailContent}</CollapsibleContent> : detailContent}
      </Collapsible>
    </section>
  );
}

type ClassicStatusListProps = {
  monitors: UptimeData["monitors"];
  metadata: Metadata;
  incidents: Map<string, Incident>;
  regionMap: Record<string, RegionData["monitors"]>;
};

export function ClassicStatusList({
  monitors,
  metadata,
  incidents,
  regionMap,
}: ClassicStatusListProps) {
  return (
    <section className="space-y-6">
      {monitors.map((monitor) => (
        <ClassicMonitorCard
          key={monitor.id}
          monitor={monitor}
          metadata={metadata}
          incidents={incidents}
          regionMap={regionMap}
        />
      ))}
    </section>
  );
}
