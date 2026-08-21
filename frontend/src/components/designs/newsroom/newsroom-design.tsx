import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import type { Incident } from "@/lib/status-dashboard";
import { NewsroomHeader } from "./newsroom-header";
import { NewsroomServiceCard } from "./service-card";
import "./newsroom.css";

type NewsroomDesignProps = {
  data: UptimeData;
  metadata: Metadata;
  incidents: Map<string, Incident>;
  regionMap: Record<string, RegionData["monitors"]>;
  isRefreshing: boolean;
  onRefresh: () => void;
};

export function NewsroomDesign({
  data,
  metadata,
  regionMap,
}: NewsroomDesignProps) {
  return (
    <div className="newsroom-container">
      <NewsroomHeader lastUpdated={data.last_updated ?? null} />

      {data.monitors.length === 0 ? (
        <div className="newsroom-empty">No services to monitor</div>
      ) : (
        data.monitors.map((service) => (
          <NewsroomServiceCard
            key={service.id}
            service={service}
            regionMap={regionMap}
            metadata={metadata}
          />
        ))
      )}
    </div>
  );
}
