import { useMemo } from "react";
import type { Incident, Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import { flattenServices } from "@/lib/status-dashboard";
import { ServiceCard } from "@/components/status/service-card";

type DenseStatusGridProps = {
  monitors: UptimeData["monitors"];
  metadata: Metadata;
  incidents: Map<string, Incident>;
  regionMap: Record<string, RegionData["monitors"]>;
};

export function DenseStatusGrid({
  monitors,
  metadata,
  incidents,
  regionMap,
}: DenseStatusGridProps) {
  const services = useMemo(
    () => flattenServices(monitors, incidents, regionMap),
    [incidents, monitors, regionMap],
  );

  return (
    <section className="grid gap-4 [grid-template-columns:repeat(auto-fill,minmax(300px,1fr))]">
      {services.map((service) => (
        <ServiceCard
          key={`${service.parent.id}:${service.monitor.id}`}
          service={service}
          metadata={metadata}
        />
      ))}
    </section>
  );
}
