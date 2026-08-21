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

  const statusLabel =
    status === "healthy" ? "✓ up" :
    status === "degraded" ? "⚠ slow" :
    status === "down" ? "✗ down" :
    "? unknown";

  return (
    <>
      <div className="newsroom-region-row">
        <span className="newsroom-region-name">{region.region}</span>
        <span className={`newsroom-region-time ${tone}`}>
          {Math.round(region.response_time_ms)}ms
        </span>
        <span className={`newsroom-region-status ${status}`}>
          {statusLabel}
        </span>
      </div>
      {downtime > 0 && (
        <div className="newsroom-downtime">
          ↓ {formatDowntime(downtime)} (last 24h)
        </div>
      )}
    </>
  );
}
