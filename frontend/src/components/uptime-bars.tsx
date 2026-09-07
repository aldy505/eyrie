import type { Metadata, SingleMonitor } from "@/lib/status-dashboard";
import {
  clamp,
  formatAvailability,
  formatDaysAgo,
  formatStatus,
  getAvailabilityRatio,
  getAvailabilityStatus,
} from "@/lib/status-dashboard";

type UptimeBarsProps = {
  monitor: SingleMonitor;
  metadata: Metadata;
  /** Outer wrapper (spacing, overflow behavior). */
  className?: string;
  /** Applied to every data bar; pair with a status class for coloring. */
  barClassName?: string;
  /** Applied to bars before the monitor existed. */
  noDataBarClassName?: string;
  /** Applied to the "N days ago / Today" label row. */
  labelClassName?: string;
};

export function UptimeBars({
  monitor,
  metadata,
  className,
  barClassName,
  noDataBarClassName,
  labelClassName,
}: UptimeBarsProps) {
  return (
    <div className={className}>
      <div
        style={{
          display: "grid",
          gridAutoFlow: "column",
          gridAutoColumns: "1fr",
          alignItems: "end",
          height: 80,
          columnGap: "var(--uptime-bars-gap, 2px)",
        }}
      >
        {Array.from({ length: metadata.retention_days }).map((_, index) => {
          const colorStartsAt = metadata.retention_days - monitor.age;
          if (index < colorStartsAt) {
            return (
              <div
                key={index}
                className={noDataBarClassName}
                style={{ minHeight: 10 }}
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
              className={barClassName ? `${barClassName} ${availabilityStatus}` : availabilityStatus}
              style={{
                height: `${Math.round(clamp(availabilityRatio, 0.18, 1) * 100)}%`,
                minHeight: 10,
              }}
              title={title}
            />
          );
        })}
      </div>
      <div
        className={labelClassName}
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginTop: 12,
        }}
      >
        <span>{formatDaysAgo(metadata.retention_days)}</span>
        <span>Today</span>
      </div>
    </div>
  );
}
