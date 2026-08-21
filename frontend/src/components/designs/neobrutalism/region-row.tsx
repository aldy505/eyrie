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

  return (
    <>
      <div className="neobrutalism-region-row">
        <span className="neobrutalism-region-name">{region.region}</span>
        <span className={`neobrutalism-region-time ${tone}`}>
          {Math.round(region.response_time_ms)}ms
        </span>
        <span className={`neobrutalism-status-square ${status}`} />
      </div>
      {downtime > 0 && (
        <div className="neobrutalism-downtime">
          ↓ {formatDowntime(downtime)}
        </div>
      )}
    </>
  );
}
