import { z } from "zod";
import { useEffect, useState } from "react";

const uptimeDataSchema = z.object({
  last_updated: z.coerce.date(),
  monitors: z.array(
    z.object({
      id: z.string(),
      name: z.string(),
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
  retention_days: z.number(),
});

type UptimeData = z.infer<typeof uptimeDataSchema>;

type BarDetail = {
  date: string;
  duration_minutes: number;
};

const BASE_URL = "";

function MonitorCard({ monitor, data }: { monitor: UptimeData["monitors"][0]; data: UptimeData }) {
  const [barDetail, setBarDetail] = useState<BarDetail | null>(null);

  return (
    <div key={monitor.id} className="p-4 border rounded-lg shadow-sm bg-white">
      <h2 className="text-xl font-semibold mb-2">{monitor.name}</h2>
      <p className="text-sm">Average latency: {monitor.response_time_ms} ms</p>
      <div className="mt-2 mx-auto">
        {Array.from({ length: data.retention_days }).map((_, index) => {
          const colorStartsAt = data.retention_days - monitor.age;
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
          const downtimeIndex = data.retention_days - index - 1;

          const downtime = monitor.downtimes[downtimeIndex];

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
              className={`inline-block w-2 h-6 mr-1 mb-1 rounded ${downtime?.duration_minutes > 10 ? "bg-red-500" : downtime?.duration_minutes > 0 ? "bg-yellow-500" : "bg-green-500"}`}
              title={downtime ? `Downtime: ${downtime.duration_minutes} minutes` : "No downtime"}
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
        <p className="text-xs flex-1 text-left">{data.retention_days} days ago</p>
        <p className="text-xs flex-1 text-right">Today</p>
      </div>
    </div>
  );
}

function App() {
  const [data, setData] = useState<UptimeData | null>(null);
  const abortController = new AbortController();
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
      abortController.abort();
    };
  }, []);

  return (
    <>
      <div className="dark:bg-gray-950 dark:text-white mx-4 w-full lg:mx-auto lg:max-w-6xl py-8">
        <section className="text-center">
          <h1 className="text-3xl font-bold">Status Page</h1>
          <p className="mt-2 text-gray-600">
            Last Updated:{" "}
            {data
              ? data.last_updated.toLocaleString(undefined, {
                  dateStyle: "long",
                  timeStyle: "medium",
                })
              : "Loading..."}
          </p>
        </section>
        <section className="mt-8 ">
          {data != null && (
            <div className="grid grid-cols-1 gap-6">
              {data.monitors.map((monitor) => (
                <MonitorCard key={monitor.id} monitor={monitor} data={data} />
              ))}
            </div>
          )}
        </section>
      </div>
    </>
  );
}

export default App;
