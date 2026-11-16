import type { Metadata, SingleMonitor } from "@/lib/status-dashboard";
import {
  clamp,
  formatAvailability,
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
};

export function UptimeBars({
  monitor,
  metadata,
  className,
  barClassName,
  noDataBarClassName,
}: UptimeBarsProps) {
  return (
    <div
      className={className}
      style={{
        display: "grid",
        gridAutoFlow: "column",
        gridAutoColumns: "1fr",
        alignItems: "end",
        height: 16,
        columnGap: "var(--uptime-bars-gap, 1px)",
      }}
    >
      {Array.from({ length: metadata.retention_days }).map((_, index) => {
        const colorStartsAt = metadata.retention_days - monitor.age;
        if (index < colorStartsAt) {
          return (
            <div
              key={index}
              className={noDataBarClassName}
              style={{ minHeight: 4 }}
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
              height: `${Math.round(clamp(availabilityRatio, 0.25, 1) * 100)}%`,
              minHeight: 4,
            }}
            title={title}
          />
        );
      })}
    </div>
  );
}
