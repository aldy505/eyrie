import { useEffect, useMemo, useState } from "react";
import { ClassicStatusList } from "@/components/status/classic-status-list";
import { DashboardHeader } from "@/components/status/dashboard-header";
import { DenseStatusGrid } from "@/components/status/dense-status-grid";
import { useDashboardLayout } from "@/hooks/use-dashboard-layout";
import {
  BASE_URL,
  getSummaryStats,
  incidentsSchema,
  metadataSchema,
  normalizeUptimeData,
  regionSchema,
  type IncidentsData,
  type Incident,
  type Metadata,
  type RegionData,
  type UptimeData,
  uptimeDataSchema,
} from "@/lib/status-dashboard";

const REGION_FETCH_JITTER_MAX_MS = 5000;

async function fetchJson(url: string) {
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`Request failed for ${url}: ${response.status} ${response.statusText}`);
  }

  return response.json();
}

function getRegionFetchJitterMs() {
  return Math.floor(Math.random() * (REGION_FETCH_JITTER_MAX_MS + 1));
}

function waitFor(ms: number) {
  return new Promise<void>((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

function App() {
  const [data, setData] = useState<UptimeData | null>(null);
  const [metadata, setMetadata] = useState<Metadata | null>(null);
  const [incidents, setIncidents] = useState<IncidentsData | null>(null);
  const [regionMap, setRegionMap] = useState<Record<string, RegionData["monitors"]>>({});
  const [error, setError] = useState<string | null>(null);
  const [isRefreshing, setIsRefreshing] = useState(false);
  const { layoutMode, toggleLayout } = useDashboardLayout("classic");

  function loadData() {
    setIsRefreshing(true);
    setError(null);
    Promise.all([
      fetchJson(`${BASE_URL}/uptime-data`),
      fetchJson(`${BASE_URL}/config`),
      fetchJson(`${BASE_URL}/monitor-incidents`),
    ])
      .then(([uptimeJson, configJson, incidentsJson]) => {
        const parsedData = normalizeUptimeData(uptimeDataSchema.parse(uptimeJson));
        const parsedMetadata = metadataSchema.parse(configJson);
        const parsedIncidents = incidentsSchema.parse(incidentsJson);

        setData(parsedData);
        setMetadata(parsedMetadata);
        setIncidents(parsedIncidents);
        document.title = parsedMetadata.title;

        const uniqueMonitorIds = Array.from(
          new Set(
            parsedData.monitors.flatMap((group) => group.monitors.map((monitor) => monitor.id)),
          ),
        );

        return Promise.allSettled(
          uniqueMonitorIds.map(async (monitorId) => {
            await waitFor(getRegionFetchJitterMs());
            const json = await fetchJson(
              `${BASE_URL}/uptime-data-by-region?monitorId=${encodeURIComponent(monitorId)}`,
            );
            return { monitorId, payload: regionSchema.parse(json) };
          }),
        );
      })
      .then((regions) => {
        const nextMap: Record<string, RegionData["monitors"]> = {};
        for (const item of regions) {
          if (item.status === "fulfilled") {
            nextMap[item.value.monitorId] = item.value.payload.monitors;
            continue;
          }

          console.error("Failed to load region data", item.reason);
        }
        setRegionMap(nextMap);
      })
      .catch((caught) => {
        setError(caught instanceof Error ? caught.message : "Failed to load status data");
      })
      .finally(() => {
        setIsRefreshing(false);
      });
  }

  useEffect(() => {
    loadData();
  }, []);

  const incidentById = useMemo(() => {
    const map = new Map<string, Incident>();
    for (const incident of incidents?.incidents ?? []) {
      map.set(incident.monitor_id, incident);
    }
    return map;
  }, [incidents]);

  const stats = useMemo(() => getSummaryStats(incidents), [incidents]);

  return (
    <div className="min-h-screen bg-[radial-gradient(circle_at_top,_rgba(56,189,248,0.14),_transparent_24%),linear-gradient(180deg,#020617_0%,#08111d_48%,#020617_100%)] text-white">
      <DashboardHeader
        metadata={metadata}
        stats={stats}
        layoutMode={layoutMode}
        isRefreshing={isRefreshing}
        lastUpdated={data?.last_updated ?? null}
        onRefresh={loadData}
        onToggleLayout={toggleLayout}
      />

      <main className="px-6 py-8 sm:px-8 xl:px-10">
        {error ? (
          <div className="rounded-[28px] border border-rose-400/20 bg-rose-500/10 p-8">
            <h2 className="text-2xl font-semibold text-white">Unable to load status data</h2>
            <p className="mt-3 text-slate-200">{error}</p>
          </div>
        ) : data && metadata ? (
          layoutMode === "grid" ? (
            <DenseStatusGrid
              monitors={data.monitors}
              metadata={metadata}
              incidents={incidentById}
              regionMap={regionMap}
            />
          ) : (
            <ClassicStatusList
              monitors={data.monitors}
              metadata={metadata}
              incidents={incidentById}
              regionMap={regionMap}
            />
          )
        ) : (
          <div className="rounded-[28px] border border-white/10 bg-[#0d1117]/80 p-8 text-slate-300">
            Loading monitor data...
          </div>
        )}
      </main>
    </div>
  );
}

export default App;
