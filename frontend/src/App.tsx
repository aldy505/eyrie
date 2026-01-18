import { z } from "zod";
import { useEffect, useState } from "react";
import { ChevronsUpDown, CircleQuestionMark } from "lucide-react";
import { Button } from "./components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./components/ui/collapsible";
import { Separator } from "./components/ui/separator";
import { Tooltip, TooltipContent, TooltipTrigger } from "./components/ui/tooltip";

const uptimeDataSchema = z.object({
  last_updated: z.coerce.date(),
  monitors: z.array(
    z.object({
      type: z.enum(["single", "group"]),
      id: z.string(),
      name: z.string(),
      description: z.string().nullish(),
      monitors: z.array(
        z.object({
          id: z.string(),
          name: z.string(),
          description: z.string().nullish(),
          response_time_ms: z.number(),
          age: z.number(),
          downtimes: z.record(
            z.string(),
            z.object({
              duration_minutes: z.number(),
            }),
          ),
        }),
      ),
    }),
  ),
});

type UptimeData = z.infer<typeof uptimeDataSchema>;

const metadataSchema = z.object({
  title: z.string().default("Status Page"),
  show_last_updated: z.boolean().default(true),
  retention_days: z.number(),
  degraded_threshold_minutes: z.number(),
  failure_threshold_minutes: z.number(),
});

type Metadata = z.infer<typeof metadataSchema>;

type BarDetail = {
  date: string;
  duration_minutes: number;
};

const BASE_URL = import.meta.env.VITE_BASE_URL ?? "";

function SingleMonitorCard({
  monitor,
  metadata,
  withoutShadow = false,
}: {
  monitor: UptimeData["monitors"][0]["monitors"][0];
  metadata: Metadata;
  withoutShadow?: boolean;
}) {
  const [barDetail, setBarDetail] = useState<BarDetail | null>(null);

  return (
    <div
      key={monitor.id}
      className={`p-4 rounded-lg ${withoutShadow ? "" : "shadow-sm border"} bg-white`}
    >
      <div className="flex flex-row justify-items-start content-center">
        <div className="flex-none">
          <h2 className="text-xl font-semibold mb-2">{monitor.name}</h2>
        </div>
        {monitor.description && (
          <div className="ml-1.5 flex-1">
            <Tooltip>
              <TooltipTrigger asChild>
                <CircleQuestionMark className="w-auto h-full max-h-5" color="#3e9392" />
              </TooltipTrigger>
              <TooltipContent>
                <p className="max-w-xs">{monitor.description}</p>
              </TooltipContent>
            </Tooltip>
          </div>
        )}
      </div>
      <p className="text-sm">Average latency: {monitor.response_time_ms} ms</p>
      <div className="mt-2 mx-auto">
        {Array.from({ length: metadata.retention_days }).map((_, index) => {
          const colorStartsAt = metadata.retention_days - monitor.age;
          if (index < colorStartsAt) {
            return (
              <div
                key={index}
                className="inline-block max-w-2 w-full h-6 mr-1 mb-1 rounded bg-gray-400"
                title="No data"
              ></div>
            );
          }

          // If the `index` is 0, the downtimeIndex should be `data.retention_days`.
          // If the `index` is 1, the downtimeIndex should be `data.retention_days - 1`, and so on.
          const downtimeIndex = metadata.retention_days - index - 1;

          const downtime = monitor.downtimes[downtimeIndex];

          let state: "downtime" | "degraded" | "up" = "up";
          let title: string = "No downtime";
          if (downtime?.duration_minutes > metadata.failure_threshold_minutes) {
            state = "downtime";
            title = `Service downtime: ${downtime.duration_minutes} minutes`;
          } else if (downtime?.duration_minutes > metadata.degraded_threshold_minutes) {
            state = "degraded";
            title = `Degraded performance: ${downtime.duration_minutes} minutes`;
          }

          return (
            <div
              onMouseEnter={(e) => {
                e.currentTarget.classList.add("scale-y-150");
                setBarDetail({
                  date: new Date(
                    Date.now() - downtimeIndex * 24 * 60 * 60 * 1000,
                  ).toLocaleDateString(undefined, { dateStyle: "long" }),
                  duration_minutes: downtime ? downtime.duration_minutes : 0,
                });
              }}
              onMouseLeave={(e) => {
                e.currentTarget.classList.remove("scale-y-150");
                setBarDetail(null);
              }}
              key={index}
              className={`inline-block max-w-2 w-full h-6 mr-1 mb-1 rounded ${state === "downtime" ? "bg-red-500" : state === "degraded" ? "bg-yellow-500" : "bg-green-500"}`}
              title={title}
            ></div>
          );
        })}
      </div>
      {barDetail != null && (
        <div className="mt-1 mb-3 p-2 border rounded bg-gray-100">
          <p className="text-sm">Date: {barDetail.date}</p>
          <p className="text-sm">Downtime Duration: {barDetail.duration_minutes} minutes</p>
          <p className="text-sm">
            Availability:{" "}
            {barDetail.duration_minutes > 0
              ? `${(100 - (barDetail.duration_minutes / 1440) * 100).toFixed(3)}%`
              : "100%"}
          </p>
        </div>
      )}

      <div className="flex justify-between">
        <p className="text-xs flex-1 text-left">{metadata.retention_days} days ago</p>
        <p className="text-xs flex-1 text-right">Today</p>
      </div>
    </div>
  );
}

function MonitorCard({
  monitor,
  metadata,
}: {
  monitor: UptimeData["monitors"][0];
  metadata: Metadata;
}) {
  if (monitor.type === "group") {
    const [showExpanded, setShowExpanded] = useState<boolean>(false);
    const averageLatency = Math.round(
      monitor.monitors.reduce((acc, curr) => acc + curr.response_time_ms, 0) /
        monitor.monitors.length,
    );

    return (
      <div key={monitor.id} className="p-4 border rounded-lg shadow-sm bg-white">
        <Collapsible open={showExpanded} onOpenChange={setShowExpanded}>
          <div className="flex items-center justify-between gap-4">
            <div className="flex-1">
              <h2 className="text-xl font-semibold mb-2">{monitor.name}</h2>
              <p className="text-sm">Average latency: {averageLatency} ms</p>
            </div>
            <CollapsibleTrigger asChild>
              <Button variant="ghost" size="icon" className="size-8">
                <ChevronsUpDown />
                <span className="sr-only">Toggle</span>
              </Button>
            </CollapsibleTrigger>
          </div>

          <div className="mt-2 mx-auto">
            {Array.from({ length: metadata.retention_days }).map((_, index) => {
              // If the `index` is 0, the downtimeIndex should be `data.retention_days`.
              // If the `index` is 1, the downtimeIndex should be `data.retention_days - 1`, and so on.
              const downtimeIndex = metadata.retention_days - index - 1;

              let downtimeMinutesSum = 0;
              let colorStartsAt: number = 0;
              for (const m of monitor.monitors) {
                const monitorColorStartsAt = metadata.retention_days - m.age;
                if (monitorColorStartsAt > colorStartsAt) {
                  colorStartsAt = monitorColorStartsAt;
                }

                const downtime = m.downtimes[downtimeIndex];
                if (downtime?.duration_minutes) {
                  downtimeMinutesSum += downtime.duration_minutes;
                }
              }

              if (index < colorStartsAt) {
                return (
                  <div
                    key={index}
                    className="inline-block max-w-2 w-full h-6 mr-1 mb-1 rounded bg-gray-400"
                    title="No data"
                  ></div>
                );
              }

              const monitorCount = monitor.monitors.length;
              const downtime = monitorCount === 0 ? 0 : downtimeMinutesSum / monitorCount;

              let state: "downtime" | "degraded" | "up" = "up";
              let title: string = "No downtime";
              if (downtime > metadata.failure_threshold_minutes) {
                state = "downtime";
                title = `Service downtime: ${downtime} minutes`;
              } else if (downtime > metadata.degraded_threshold_minutes) {
                state = "degraded";
                title = `Degraded performance: ${downtime} minutes`;
              }

              return (
                <div
                  key={index}
                  className={`inline-block w-2 h-6 mr-1 mb-1 rounded ${state === "downtime" ? "bg-red-500" : state === "degraded" ? "bg-yellow-500" : "bg-green-500"}`}
                  title={title}
                ></div>
              );
            })}
          </div>
          <CollapsibleContent className="pt-4 grid grid-cols-1 gap-4">
            <Separator />
            {monitor.monitors.map((subMonitor) => (
              <SingleMonitorCard
                key={subMonitor.id}
                monitor={subMonitor}
                metadata={metadata}
                withoutShadow={true}
              />
            ))}
          </CollapsibleContent>
        </Collapsible>
      </div>
    );
  } else if (monitor.type === "single") {
    const singleMonitor = monitor.monitors.at(0);
    if (!singleMonitor) {
      return null;
    }

    return <SingleMonitorCard monitor={singleMonitor} metadata={metadata} />;
  } else {
    return null;
  }
}

function App() {
  const [data, setData] = useState<UptimeData | null>(null);
  const [metadata, setMetadata] = useState<Metadata | null>(null);

  const uptimeDataAbortController = new AbortController();
  useEffect(() => {
    fetch(BASE_URL + "/uptime-data", { signal: undefined })
      .then((response) => response.json())
      .then((json) => {
        const parsedData = uptimeDataSchema.parse(json);
        setData(parsedData);
      })
      .catch((error) => {
        if (error.name === "AbortError") {
          console.log("Fetch aborted");
        } else {
          console.error("Error fetching uptime data:", error);
        }
      });

    return () => {
      uptimeDataAbortController.abort();
    };
  }, []);

  const metadataAbortController = new AbortController();
  useEffect(() => {
    fetch(BASE_URL + "/config", { signal: undefined })
      .then((response) => response.json())
      .then((json) => {
        const parsedMetadata = metadataSchema.parse(json);
        setMetadata(parsedMetadata);
        document.title = parsedMetadata.title;
      })
      .catch((error) => {
        if (error.name === "AbortError") {
          console.log("Fetch aborted");
        } else {
          console.error("Error fetching metadata:", error);
        }
      });

    return () => {
      metadataAbortController.abort();
    };
  }, []);

  return (
    <>
      <div className="dark:bg-gray-950 dark:text-white mx-4 w-full lg:mx-auto lg:max-w-6xl py-8">
        <section className="text-center">
          <h1 className="text-3xl font-bold">{metadata ? metadata.title : "Status Page"}</h1>
          {metadata?.show_last_updated && (
            <p className="mt-2 text-gray-600">
              Last Updated:{" "}
              {data
                ? data.last_updated.toLocaleString(undefined, {
                    dateStyle: "long",
                    timeStyle: "medium",
                  })
                : "Loading..."}
            </p>
          )}
        </section>
        <section className="mt-8 ">
          {data != null && metadata != null && (
            <div className="grid grid-cols-1 gap-6">
              {data.monitors.map((monitor) => (
                <MonitorCard key={monitor.id} monitor={monitor} metadata={metadata} />
              ))}
            </div>
          )}
        </section>
      </div>
    </>
  );
}

export default App;
