import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import type { Incident } from "@/lib/status-dashboard";
import { DitheredHeader } from "./dithered-header";
import { DitheredStatusList } from "./dithered-status-list";
import "./dithered.css";

type DitheredDesignProps = {
  data: UptimeData;
  metadata: Metadata;
  incidents: Map<string, Incident>;
  regionMap: Record<string, RegionData["monitors"]>;
  isRefreshing: boolean;
  onRefresh: () => void;
};

export function DitheredDesign({
  data,
  metadata,
  incidents,
  regionMap,
}: DitheredDesignProps) {
  // Build incident counts from the map
  let healthy = 0;
  let degraded = 0;
  let down = 0;
  for (const incident of incidents.values()) {
    if (incident.status === "degraded") degraded++;
    else if (incident.status === "down") down++;
    else healthy++;
  }

  return (
    <div className="dithered-shell">
      <div className="dithered-container">
        <DitheredHeader
          lastUpdated={data.last_updated ?? null}
          stats={{ healthy, degraded, down, incidents: degraded + down }}
        />
        <DitheredStatusList
          monitors={data.monitors}
          metadata={metadata}
          incidents={incidents}
          regionMap={regionMap}
        />
      </div>
    </div>
  );
}
