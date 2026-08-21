import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import type { Incident } from "@/lib/status-dashboard";
import { HearthHeader } from "./hearth-header";
import { ServiceCard } from "./service-card";
import "./hearth.css";

type HearthDesignProps = {
  data: UptimeData;
  metadata: Metadata;
  incidents: Map<string, Incident>;
  regionMap: Record<string, RegionData["monitors"]>;
  isRefreshing: boolean;
  onRefresh: () => void;
};

export function HearthDesign({
  data,
  metadata,
  regionMap,
}: HearthDesignProps) {
  return (
    <div className="hearth-shell">
      <div className="hearth-container">
        <HearthHeader lastUpdated={data.last_updated ?? null} />

        {data.monitors.length === 0 ? (
          <div className="hearth-empty">No services to monitor</div>
        ) : (
          data.monitors.map((service) => (
            <ServiceCard
              key={service.id}
              service={service}
              regionMap={regionMap}
              metadata={metadata}
            />
          ))
        )}
      </div>
    </div>
  );
}
