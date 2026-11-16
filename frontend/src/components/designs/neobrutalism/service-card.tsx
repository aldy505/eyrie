import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import { formatAvailability, getAvailabilityRatio } from "@/lib/status-dashboard";
import { getTodayDowntimeMinutes } from "@/lib/design-config";
import { NodeAccordion } from "./node-accordion";

type ServiceCardProps = {
  service: UptimeData["monitors"][number];
  regionMap: Record<string, RegionData["monitors"]>;
  metadata: Metadata;
  openIds: ReadonlySet<string>;
  onToggleNode: (monitorId: string) => void;
};

export function ServiceCard({
  service,
  regionMap,
  metadata,
  openIds,
  onToggleNode,
}: ServiceCardProps) {
  let totalDowntime = 0;
  let probeCount = 0;

  for (const monitor of service.monitors) {
    const regions = regionMap[monitor.id] ?? [];
    if (regions.length === 0) {
      totalDowntime += monitor.downtimes["0"]?.duration_minutes ?? 0;
      probeCount++;
      continue;
    }

    for (const region of regions) {
      totalDowntime += getTodayDowntimeMinutes(region);
      probeCount++;
    }
  }

  const avgDowntime = probeCount > 0 ? totalDowntime / probeCount : 0;
  const availability = formatAvailability(getAvailabilityRatio(avgDowntime));

  return (
    <section className="neobrutalism-service-card">
      <header className="neobrutalism-service-header">
        <h2 className="neobrutalism-service-name">{service.name}</h2>
        <span className="neobrutalism-service-uptime">{availability}</span>
      </header>

      {service.monitors.length === 0 ? (
        <p className="neobrutalism-empty">No monitors configured</p>
      ) : (
        service.monitors.map((monitor) => (
          <NodeAccordion
            key={monitor.id}
            monitor={monitor}
            regions={regionMap[monitor.id] ?? []}
            metadata={metadata}
            isOpen={openIds.has(monitor.id)}
            onToggle={() => onToggleNode(monitor.id)}
          />
        ))
      )}
    </section>
  );
}
