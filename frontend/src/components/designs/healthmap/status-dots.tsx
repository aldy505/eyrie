import type { Metadata, RegionData } from "@/lib/status-dashboard";
import { getRegionStatus } from "@/lib/design-config";

type StatusDotsProps = {
  regions: RegionData["monitors"];
  metadata: Metadata;
};

export function StatusDots({ regions, metadata }: StatusDotsProps) {
  return (
    <div className="healthmap-status-dots">
      {regions.map((region) => {
        const status = getRegionStatus(region, metadata);
        return (
          <span
            key={region.region}
            className={`healthmap-dot ${status}`}
            title={region.region}
          />
        );
      })}
    </div>
  );
}
