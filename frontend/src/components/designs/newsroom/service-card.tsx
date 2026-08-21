import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import { getWorstStatus } from "@/lib/status-dashboard";
import { getRegionStatus } from "@/lib/design-config";
import { RegionRow } from "./region-row";

type NewsroomServiceCardProps = {
  service: UptimeData["monitors"][number];
  regionMap: Record<string, RegionData["monitors"]>;
  metadata: Metadata;
};

export function NewsroomServiceCard({ service, regionMap, metadata }: NewsroomServiceCardProps) {
  // Collect all regions from child monitors
  const allRegions: RegionData["monitors"][number][] = [];
  for (const monitor of service.monitors) {
    const regions = regionMap[monitor.id];
    if (regions) {
      allRegions.push(...regions);
    }
  }

  // Calculate worst status across all regions
  const statuses = allRegions.map((r) => getRegionStatus(r, metadata));
  const worst = statuses.length > 0 ? getWorstStatus(statuses) : "healthy";

  // Count degraded/down regions
  const degradedCount = statuses.filter((s) => s === "degraded").length;
  const downCount = statuses.filter((s) => s === "down").length;

  let summary: string;
  if (downCount > 0) {
    summary = `${downCount} region${downCount === 1 ? "" : "s"} down`;
  } else if (degradedCount > 0) {
    summary = `${degradedCount} region${degradedCount === 1 ? "" : "s"} degraded`;
  } else {
    summary = "All regions healthy";
  }

  return (
    <div className="newsroom-service-card">
      <div className="newsroom-service-header">
        <span className={`newsroom-status-dot ${worst}`} />
        <h2 className="newsroom-service-name">{service.name}</h2>
      </div>
      <p className="newsroom-service-summary">{summary}</p>

      {allRegions.map((region) => (
        <RegionRow
          key={region.region}
          region={region}
          metadata={metadata}
        />
      ))}
    </div>
  );
}
