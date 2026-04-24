import type { Metadata, SingleMonitor } from "@/lib/status-dashboard";
import {
  clamp,
  formatAvailability,
  formatDaysAgo,
  formatStatus,
  getAvailabilityRatio,
  getAvailabilityStatus,
  getStatusBarColor,
} from "@/lib/status-dashboard";

type UptimeSparklineProps = {
  monitor: SingleMonitor;
  metadata: Metadata;
};

export function UptimeSparkline({ monitor, metadata }: UptimeSparklineProps) {
  return (
    <div className="space-y-3">
      <div className="grid h-20 grid-flow-col auto-cols-fr items-end gap-[2px]">
        {Array.from({ length: metadata.retention_days }).map((_, index) => {
          const colorStartsAt = metadata.retention_days - monitor.age;
          if (index < colorStartsAt) {
            return (
              <div
                key={index}
                className="min-h-[10px] rounded-t-full bg-white/[0.08]"
                title="No data"
              />
            );
          }

          const downtimeIndex = metadata.retention_days - index - 1;
          const downtimeMinutes = monitor.downtimes[downtimeIndex]?.duration_minutes ?? 0;
          const availabilityRatio = getAvailabilityRatio(downtimeMinutes);
          const availabilityStatus = getAvailabilityStatus(downtimeMinutes, metadata);
          const title = [
            formatStatus(availabilityStatus),
            `Availability: ${formatAvailability(availabilityRatio)}`,
            `Downtime: ${downtimeMinutes} minutes`,
          ].join(" • ");

          return (
            <div
              key={index}
              className="min-h-[10px] rounded-t-full"
              style={{
                height: `${Math.round(clamp(availabilityRatio, 0.18, 1) * 100)}%`,
                backgroundColor: getStatusBarColor(availabilityStatus),
              }}
              title={title}
            />
          );
        })}
      </div>
      <div className="flex items-center justify-between text-[11px] text-slate-500">
        <span>{formatDaysAgo(metadata.retention_days)}</span>
        <span>Today</span>
      </div>
    </div>
  );
}
