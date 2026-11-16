import { useEffect, useState } from "react";
import { NeobrutalismDesign } from "@/components/designs/neobrutalism/neobrutalism-design";
import { useTheme } from "@/hooks/use-theme";
import {
  BASE_URL,
  metadataSchema,
  normalizeUptimeData,
  regionSchema,
  type Metadata,
  type RegionData,
  type UptimeData,
  uptimeDataSchema,
} from "@/lib/status-dashboard";

const REGION_FETCH_JITTER_MAX_MS = 5000;

const REQUEST_TIMEOUT_MS = 60_000;

async function fetchJson(url: string) {
  const response = await fetch(url, { signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS) });
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

type StatusStateProps = {
  tone: "loading" | "error";
  label: string;
  title: string;
  message?: string;
};

function StatusState({ tone, label, title, message }: StatusStateProps) {
  return (
    <div className="neobrutalism-shell">
      <main className="neobrutalism-container neobrutalism-state-container">
        <section
          className={`neobrutalism-state-card ${tone}`}
          role={tone === "error" ? "alert" : "status"}
        >
          <span className="neobrutalism-state-label">[{label}]</span>
          <h1 className="neobrutalism-state-title">{title}</h1>
          {message && <p className="neobrutalism-state-message">{message}</p>}
        </section>
      </main>
    </div>
  );
}

function App() {
  const { theme, toggleTheme } = useTheme();
  const [data, setData] = useState<UptimeData | null>(null);
  const [metadata, setMetadata] = useState<Metadata | null>(null);
  const [regionMap, setRegionMap] = useState<Record<string, RegionData["monitors"]>>({});
  const [error, setError] = useState<string | null>(null);

  function loadData() {
    setError(null);
    Promise.all([fetchJson(`${BASE_URL}/uptime-data`), fetchJson(`${BASE_URL}/config`)])
      .then(([uptimeJson, configJson]) => {
        const parsedData = normalizeUptimeData(uptimeDataSchema.parse(uptimeJson));
        const parsedMetadata = metadataSchema.parse(configJson);

        setData(parsedData);
        setMetadata(parsedMetadata);
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
      });
  }

  useEffect(() => {
    loadData();
  }, []);

  if (error) {
    return (
      <StatusState tone="error" label="ERROR" title="Unable to load status data" message={error} />
    );
  }

  if (!data || !metadata) {
    return <StatusState tone="loading" label="LOADING" title="Loading monitor data..." />;
  }

  return (
    <NeobrutalismDesign
      data={data}
      metadata={metadata}
      regionMap={regionMap}
      theme={theme}
      onToggleTheme={toggleTheme}
    />
  );
}

export default App;
