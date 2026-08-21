import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import {
  formatAvailability,
  getAvailabilityRatio,
} from "@/lib/status-dashboard";
import { getTodayDowntimeMinutes } from "@/lib/design-config";
import { StatusDots } from "./status-dots";
import { RegionDetails } from "./region-details";

type ServiceBlockProps = {
  service: UptimeData["monitors"][number];
  regionMap: Record<string, RegionData["monitors"]>;
  metadata: Metadata;
};

export function ServiceBlock({ service, regionMap, metadata }: ServiceBlockProps) {
  // Collect all regions from child monitors
  const allRegions: RegionData["monitors"][number][] = [];
  for (const monitor of service.monitors) {
    const regions = regionMap[monitor.id];
    if (regions) {
      allRegions.push(...regions);
    }
  }

  // Calculate average uptime across all regions
  const uptimeRatio =
    allRegions.length > 0
      ? allRegions.reduce((sum, r) => sum + getAvailabilityRatio(getTodayDowntimeMinutes(r)), 0) /
        allRegions.length
      : 1;

  return (
    <div className="healthmap-service-block">
      <div className="healthmap-service-headline">
        <h3 className="healthmap-service-name">{service.name}</h3>
        <span className="healthmap-uptime">{formatAvailability(uptimeRatio)}</span>
      </div>

      <StatusDots regions={allRegions} metadata={metadata} />

      <RegionDetails regions={allRegions} />
    </div>
  );
}
