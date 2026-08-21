import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import { ServiceBlock } from "./service-block";

type TierSectionProps = {
  tier: "critical" | "supporting" | "auxiliary";
  services: UptimeData["monitors"];
  regionMap: Record<string, RegionData["monitors"]>;
  metadata: Metadata;
};

const tierLabels: Record<TierSectionProps["tier"], string> = {
  critical: "Critical tier",
  supporting: "Supporting tier",
  auxiliary: "Auxiliary tier",
};

export function TierSection({ tier, services, regionMap, metadata }: TierSectionProps) {
  if (services.length === 0) return null;

  return (
    <div className="healthmap-tier-section">
      <h2 className="healthmap-tier-label">{tierLabels[tier]}</h2>
      {services.map((service) => (
        <ServiceBlock
          key={service.id}
          service={service}
          regionMap={regionMap}
          metadata={metadata}
        />
      ))}
    </div>
  );
}
