import { DashboardHeader } from "@/components/status/dashboard-header";
import { ClassicStatusList } from "@/components/status/classic-status-list";
import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import type { Incident } from "@/lib/status-dashboard";

type MidnightDesignProps = {
  data: UptimeData;
  metadata: Metadata;
  incidents: Map<string, Incident>;
  regionMap: Record<string, RegionData["monitors"]>;
  isRefreshing: boolean;
  onRefresh: () => void;
};

export function MidnightDesign({
  data,
  metadata,
  incidents,
  regionMap,
  isRefreshing,
  onRefresh,
}: MidnightDesignProps) {
  return (
    <div className="min-h-screen bg-[radial-gradient(circle_at_top,_rgba(56,189,248,0.14),_transparent_24%),linear-gradient(180deg,#020617_0%,#08111d_48%,#020617_100%)] text-white">
      <DashboardHeader
        metadata={metadata}
        stats={{ healthy: 0, degraded: 0, down: 0, incidents: 0 }}
        isRefreshing={isRefreshing}
        lastUpdated={data.last_updated ?? null}
        onRefresh={onRefresh}
      />
      <main className="px-6 py-8 sm:px-8 xl:px-10">
        <ClassicStatusList
          monitors={data.monitors}
          metadata={metadata}
          incidents={incidents}
          regionMap={regionMap}
        />
      </main>
    </div>
  );
}
