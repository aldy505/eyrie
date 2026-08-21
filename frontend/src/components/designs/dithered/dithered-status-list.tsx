import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import type { Incident } from "@/lib/status-dashboard";
import { getWorstStatus } from "@/lib/status-dashboard";
import {
  getRegionStatus,
  getResponseTimeTone,
  getTodayDowntimeMinutes,
} from "@/lib/design-config";
import { formatDowntime } from "@/lib/status-dashboard";

type DitheredStatusListProps = {
  monitors: UptimeData["monitors"];
  metadata: Metadata;
  incidents: Map<string, Incident>;
  regionMap: Record<string, RegionData["monitors"]>;
};

export function DitheredStatusList({
  monitors,
  metadata,
  regionMap,
}: DitheredStatusListProps) {
  if (monitors.length === 0) {
    return <div className="dithered-empty">No services to monitor</div>;
  }

  return (
    <>
      {monitors.map((service) => {
        // Compute worst status across all monitor regions
        const allStatuses: string[] = [];
        for (const monitor of service.monitors) {
          const regions = regionMap[monitor.id] ?? [];
          for (const region of regions) {
            allStatuses.push(getRegionStatus(region, metadata));
          }
        }
        const worst = allStatuses.length > 0
          ? getWorstStatus(allStatuses)
          : "healthy";

        return (
          <div key={service.id} className="dithered-service-card">
            <div className="dithered-service-header">
              <h2 className="dithered-service-name">{service.name}</h2>
              <span className={`dithered-status-pill ${worst}`}>
                {worst}
              </span>
            </div>

            {service.monitors.length === 0 ? (
              <p className="dithered-empty" style={{ marginTop: 12 }}>No monitors configured</p>
            ) : (
              service.monitors.map((monitor) => {
                const regions = regionMap[monitor.id] ?? [];
                return (
                  <div key={monitor.id} className="dithered-monitor-block">
                    <h3 className="dithered-monitor-name">{monitor.name}</h3>
                    {regions.map((region) => {
                      const tone = getResponseTimeTone(region.response_time_ms);
                      const downtime = getTodayDowntimeMinutes(region);
                      return (
                        <div key={region.region} className="dithered-region-row">
                          <span className="dithered-region-name">{region.region}</span>
                          <span className={`dithered-region-time ${tone}`}>
                            {Math.round(region.response_time_ms)}ms
                          </span>
                          <span className={`dithered-region-status ${getRegionStatus(region, metadata)}`}>
                            {getRegionStatus(region, metadata)}
                          </span>
                          {downtime > 0 && (
                            <span className="dithered-downtime">
                              ↓ {formatDowntime(downtime)}
                            </span>
                          )}
                        </div>
                      );
                    })}
                  </div>
                );
              })
            )}
          </div>
        );
      })}
    </>
  );
}
