import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import type { Incident } from "@/lib/status-dashboard";
import { HealthmapHeader } from "./healthmap-header";
import { TierSection } from "./tier-section";
import "./healthmap.css";

type HealthmapDesignProps = {
  data: UptimeData;
  metadata: Metadata;
  incidents: Map<string, Incident>;
  regionMap: Record<string, RegionData["monitors"]>;
  isRefreshing: boolean;
  onRefresh: () => void;
};

export function HealthmapDesign({
  data,
  metadata,
  regionMap,
}: HealthmapDesignProps) {
  // Default all services to "critical" tier since API has no tier metadata
  const criticalServices = data.monitors;

  return (
    <div className="healthmap-container">
      <HealthmapHeader lastUpdated={data.last_updated ?? null} />

      {criticalServices.length === 0 ? (
        <div className="healthmap-empty">No services to monitor</div>
      ) : (
        <TierSection
          tier="critical"
          services={criticalServices}
          regionMap={regionMap}
          metadata={metadata}
        />
      )}
    </div>
  );
}
