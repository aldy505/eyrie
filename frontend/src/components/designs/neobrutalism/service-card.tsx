import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import { UptimeBars } from "@/components/uptime-bars";
import {
  getAvailabilityRatio,
  formatAvailability,
} from "@/lib/status-dashboard";
import { getTodayDowntimeMinutes } from "@/lib/design-config";
import { RegionRow } from "./region-row";

type ServiceCardProps = {
  service: UptimeData["monitors"][number];
  regionMap: Record<string, RegionData["monitors"]>;
  metadata: Metadata;
};

export function ServiceCard({ service, regionMap, metadata }: ServiceCardProps) {
  // Compute average availability across all monitor regions
  let totalDowntime = 0;
  let regionCount = 0;
  for (const monitor of service.monitors) {
    const regions = regionMap[monitor.id] ?? [];
    for (const region of regions) {
      totalDowntime += getTodayDowntimeMinutes(region);
      regionCount++;
    }
  }
  const avgDowntime = regionCount > 0 ? totalDowntime / regionCount : 0;
  const availability = formatAvailability(getAvailabilityRatio(avgDowntime));

  return (
    <div className="neobrutalism-service-card">
      <div className="neobrutalism-service-header">
        <h2 className="neobrutalism-service-name">{service.name}</h2>
        <span className="neobrutalism-service-uptime">{availability}</span>
      </div>

      {service.monitors.length === 0 ? (
        <p className="neobrutalism-empty" style={{ marginTop: 12 }}>No monitors configured</p>
      ) : (
        service.monitors.map((monitor) => {
          const regions = regionMap[monitor.id] ?? [];
          return (
            <div key={monitor.id} className="neobrutalism-monitor-block">
              <h3 className="neobrutalism-monitor-name">{monitor.name}</h3>
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
                className="neobrutalism-uptime-bars"
                barClassName="neobrutalism-uptime-bars-bar"
                noDataBarClassName="neobrutalism-uptime-bars-nodata"
                labelClassName="neobrutalism-uptime-bars-label"
              />
            </div>
          );
        })
      )}
    </div>
  );
}
