import type { Metadata, RegionData } from "@/lib/status-dashboard";
import {
  getTodayDowntimeMinutes,
  getRegionStatus,
  getResponseTimeTone,
} from "@/lib/design-config";
import { formatDowntime } from "@/lib/status-dashboard";

type RegionRowProps = {
  region: RegionData["monitors"][number];
  metadata: Metadata;
};

export function RegionRow({ region, metadata }: RegionRowProps) {
  const status = getRegionStatus(region, metadata);
  const tone = getResponseTimeTone(region.response_time_ms);
  const downtime = getTodayDowntimeMinutes(region);

  const friendlyStatus =
    status === "healthy" ? "✓ cozy" :
    status === "degraded" ? "⚠ warm" :
    status === "down" ? "✗ cold" :
    "? quiet";

  return (
    <>
      <div className="hearth-region-row">
        <span className="hearth-region-name">{region.region}</span>
        <span className={`hearth-region-status ${status}`}>
          {friendlyStatus}
        </span>
        <span className={`hearth-region-time ${tone}`}>
          {Math.round(region.response_time_ms)}ms
        </span>
      </div>
      {downtime > 0 && (
        <div className="hearth-downtime">
          ↓ napped {formatDowntime(downtime)}
        </div>
      )}
    </>
  );
}
