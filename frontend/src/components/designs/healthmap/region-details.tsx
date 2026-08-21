import type { RegionData } from "@/lib/status-dashboard";
import { formatDowntime } from "@/lib/status-dashboard";
import { getTodayDowntimeMinutes, getResponseTimeTone } from "@/lib/design-config";

type RegionDetailsProps = {
  regions: RegionData["monitors"];
};

export function RegionDetails({ regions }: RegionDetailsProps) {
  return (
    <div>
      {regions.map((region) => {
        const tone = getResponseTimeTone(region.response_time_ms);
        const downtime = getTodayDowntimeMinutes(region);

        let timeClass = "normal";
        if (tone === "slow") timeClass = "amber";
        else if (tone === "critical") timeClass = "red";
        else if (tone === "normal") timeClass = "secondary";

        return (
          <div key={region.region}>
            <div className="healthmap-region-row">
              <span className="healthmap-region-name">{region.region}</span>
              <span className={`healthmap-region-time ${timeClass}`}>
                {Math.round(region.response_time_ms)}ms
              </span>
            </div>
            {downtime > 0 && (
              <div className="healthmap-downtime">
                ↓ {formatDowntime(downtime)} (last 24h)
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
