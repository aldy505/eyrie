import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import type { Incident } from "@/lib/status-dashboard";
import { NeobrutalismHeader } from "./neobrutalism-header";
import { ServiceCard } from "./service-card";
import "./neobrutalism.css";

type NeobrutalismDesignProps = {
  data: UptimeData;
  metadata: Metadata;
  incidents: Map<string, Incident>;
  regionMap: Record<string, RegionData["monitors"]>;
  isRefreshing: boolean;
  onRefresh: () => void;
};

export function NeobrutalismDesign({
  data,
  metadata,
  regionMap,
}: NeobrutalismDesignProps) {
  return (
    <div className="neobrutalism-shell">
      <div className="neobrutalism-container">
        <NeobrutalismHeader lastUpdated={data.last_updated ?? null} />

        {data.monitors.length === 0 ? (
          <div className="neobrutalism-empty">No services to monitor</div>
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
