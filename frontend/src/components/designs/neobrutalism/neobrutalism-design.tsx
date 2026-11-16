import { useLayoutEffect, useMemo, useRef, useState } from "react";
import type { Metadata, RegionData, UptimeData } from "@/lib/status-dashboard";
import type { ThemeMode } from "@/hooks/use-theme";
import { NeobrutalismHeader } from "./neobrutalism-header";
import { ServiceCard } from "./service-card";
import { summarizeMonitor } from "./monitor-health";
import "./neobrutalism.css";

const MASONRY_MIN_COLUMN_WIDTH = 460;
const MASONRY_MAX_COLUMNS = 6;
const MASONRY_COLUMN_GAP = 20;
const MASONRY_FALLBACK_CARD_HEIGHT = 200;

type NeobrutalismDesignProps = {
  data: UptimeData;
  metadata: Metadata;
  regionMap: Record<string, RegionData["monitors"]>;
  theme: ThemeMode;
  onToggleTheme: () => void;
};

export function NeobrutalismDesign({
  data,
  metadata,
  regionMap,
  theme,
  onToggleTheme,
}: NeobrutalismDesignProps) {
  const monitorIds = useMemo(
    () => data.monitors.flatMap((group) => group.monitors.map((monitor) => monitor.id)),
    [data],
  );

  const defaultOpenIds = useMemo(() => {
    const ids = new Set<string>();
    for (const group of data.monitors) {
      for (const monitor of group.monitors) {
        const health = summarizeMonitor(monitor, regionMap[monitor.id] ?? [], metadata);
        if (health.isDegraded) ids.add(monitor.id);
      }
    }
    return ids;
  }, [data, metadata, regionMap]);

  const [openIds, setOpenIds] = useState<ReadonlySet<string>>(defaultOpenIds);
  const manuallyToggledIds = useRef<Set<string>>(new Set());

  // Apply smart defaults when data (re)arrives, but never override a row the
  // user has already clicked. Layout-phase so the expansion flushes before
  // paint and before the masonry measure below.
  useLayoutEffect(() => {
    setOpenIds((previous) => {
      const next = new Set(defaultOpenIds);
      for (const id of manuallyToggledIds.current) {
        if (previous.has(id)) {
          next.add(id);
        } else {
          next.delete(id);
        }
      }
      return next;
    });
  }, [defaultOpenIds]);

  const allExpanded =
    monitorIds.length > 0 && monitorIds.every((monitorId) => openIds.has(monitorId));

  const toggleNode = (monitorId: string) => {
    manuallyToggledIds.current.add(monitorId);
    setOpenIds((previous) => {
      const next = new Set(previous);
      if (next.has(monitorId)) {
        next.delete(monitorId);
      } else {
        next.add(monitorId);
      }
      return next;
    });
  };

  const toggleAll = () => {
    for (const monitorId of monitorIds) manuallyToggledIds.current.add(monitorId);
    setOpenIds(allExpanded ? new Set() : new Set(monitorIds));
  };

  const masonryRef = useRef<HTMLDivElement>(null);
  const cardHeights = useRef<Map<string, number>>(new Map());
  const packedLayoutKey = useRef("");
  const [columnCount, setColumnCount] = useState(1);
  const [columnAssignments, setColumnAssignments] = useState<number[]>([]);

  const serviceIds = useMemo(() => data.monitors.map((service) => service.id).join("|"), [data]);
  // Repack triggers: column count, service set, region arrival (region rows
  // change card heights), or a smart-default change — never a user toggle.
  const hasRegions = Object.keys(regionMap).length > 0;
  const defaultOpenKey = [...defaultOpenIds].sort().join(",");
  const layoutKey = `${columnCount}:${serviceIds}:${hasRegions ? "r" : "-"}:${defaultOpenKey}`;

  useLayoutEffect(() => {
    const element = masonryRef.current;
    if (!element) return;

    const updateColumnCount = () => {
      const next = Math.min(
        MASONRY_MAX_COLUMNS,
        Math.max(
          1,
          Math.floor(
            (element.clientWidth + MASONRY_COLUMN_GAP) /
              (MASONRY_MIN_COLUMN_WIDTH + MASONRY_COLUMN_GAP),
          ),
        ),
      );
      setColumnCount((previous) => (previous === next ? previous : next));
    };

    updateColumnCount();
    const observer = new ResizeObserver(updateColumnCount);
    observer.observe(element);
    return () => observer.disconnect();
  }, [data.monitors.length]);

  // Pack cards shortest-column-first, measuring the rendered card heights.
  // The measure runs in rAF so it sees the DOM after the layout-phase smart
  // default expansion above. Re-pack only when the column count, service set,
  // region arrival, or smart defaults change — never on user toggles, so
  // expanding a card grows its column instead of reshuffling every other card.
  useLayoutEffect(() => {
    if (data.monitors.length === 0 || packedLayoutKey.current === layoutKey) return;

    const frame = requestAnimationFrame(() => {
      for (const element of masonryRef.current?.querySelectorAll<HTMLElement>(
        ".neobrutalism-masonry-item[data-service-id]",
      ) ?? []) {
        const serviceId = element.dataset.serviceId;
        if (serviceId) cardHeights.current.set(serviceId, element.getBoundingClientRect().height);
      }

      const columnHeights = Array.from({ length: columnCount }, () => 0);
      const assignments: number[] = [];

      for (const service of data.monitors) {
        const cardHeight = cardHeights.current.get(service.id) ?? MASONRY_FALLBACK_CARD_HEIGHT;
        let target = 0;
        for (let column = 1; column < columnCount; column++) {
          if (columnHeights[column] < columnHeights[target]) target = column;
        }
        assignments.push(target);
        columnHeights[target] += cardHeight + MASONRY_COLUMN_GAP;
      }

      packedLayoutKey.current = layoutKey;
      setColumnAssignments(assignments);
    });

    return () => cancelAnimationFrame(frame);
  }, [columnCount, data, layoutKey]);

  // Keep the previous packing visible while a repack is pending (rAF), so
  // cards move once per repack instead of snapping to round-robin first.
  const hasValidAssignments =
    columnAssignments.length === data.monitors.length &&
    columnAssignments.every((column) => column < columnCount);
  const columnBuckets: Array<Array<{ service: UptimeData["monitors"][number]; index: number }>> =
    Array.from({ length: columnCount }, () => []);

  data.monitors.forEach((service, index) => {
    const column = hasValidAssignments ? columnAssignments[index] : index % columnCount;
    columnBuckets[column]?.push({ service, index });
  });

  return (
    <div className="neobrutalism-shell">
      <div className="neobrutalism-container">
        <NeobrutalismHeader
          lastUpdated={data.last_updated ?? null}
          theme={theme}
          onToggleTheme={onToggleTheme}
          allExpanded={allExpanded}
          onToggleAll={toggleAll}
        />

        {data.monitors.length === 0 ? (
          <div className="neobrutalism-empty neobrutalism-empty-page">No services to monitor</div>
        ) : (
          <main className="neobrutalism-grid" ref={masonryRef}>
            {columnBuckets.map((bucket, column) => (
              <div className="neobrutalism-column" key={column}>
                {bucket.map(({ service }) => (
                  <div
                    key={service.id}
                    className="neobrutalism-masonry-item"
                    data-service-id={service.id}
                  >
                    <ServiceCard
                      service={service}
                      regionMap={regionMap}
                      metadata={metadata}
                      openIds={openIds}
                      onToggleNode={toggleNode}
                    />
                  </div>
                ))}
              </div>
            ))}
          </main>
        )}
      </div>
    </div>
  );
}
