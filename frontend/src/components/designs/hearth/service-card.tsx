import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import { UptimeBars } from "@/components/uptime-bars";
import { getRegionStatus } from "@/lib/design-config";
import { getWorstStatus } from "@/lib/status-dashboard";
import { RegionRow } from "./region-row";

type ServiceCardProps = {
  service: UptimeData["monitors"][number];
  regionMap: Record<string, RegionData["monitors"]>;
  metadata: Metadata;
};

export function ServiceCard({ service, regionMap, metadata }: ServiceCardProps) {
  // Collect all regions and compute worst status
  const allRegions: RegionData["monitors"][number][] = [];
  for (const monitor of service.monitors) {
    const regions = regionMap[monitor.id];
    if (regions) {
      allRegions.push(...regions);
    }
  }

  const statuses = allRegions.map((r) => getRegionStatus(r, metadata));
  const worst = statuses.length > 0 ? getWorstStatus(statuses) : "healthy";

  const degradedCount = statuses.filter((s) => s === "degraded").length;
  const downCount = statuses.filter((s) => s === "down").length;

  let summary: string;
  if (downCount > 0) {
    summary = `Feeling a bit cold — ${downCount} region${downCount === 1 ? "" : "s"} down`;
  } else if (degradedCount > 0) {
    summary = `Running a little warm — ${degradedCount} region${degradedCount === 1 ? "" : "s"} degraded`;
  } else {
    summary = "Everything is cozy";
  }

  return (
    <div className="hearth-service-card">
      <div className="hearth-service-header">
        <span className={`hearth-status-dot ${worst}`} />
        <h2 className="hearth-service-name">{service.name}</h2>
      </div>
      <p className="hearth-service-summary">{summary}</p>

      {service.monitors.length === 0 ? (
        <p className="hearth-service-summary">No monitors configured</p>
      ) : (
        service.monitors.map((monitor) => {
          const regions = regionMap[monitor.id] ?? [];
          return (
            <div key={monitor.id} className="hearth-monitor-block">
              <h3 className="hearth-monitor-name">{monitor.name}</h3>
              {regions.map((region) => (
                <RegionRow
                  key={region.region}
                  region={region}
                  metadata={metadata}
                />
              ))}
              <UptimeBars
                monitor={monitor}
                metadata={metadata}
                className="hearth-uptime-bars"
                barClassName="hearth-uptime-bars-bar"
                noDataBarClassName="hearth-uptime-bars-nodata"
                labelClassName="hearth-uptime-bars-label"
              />
            </div>
          );
        })
      )}
    </div>
  );
}
