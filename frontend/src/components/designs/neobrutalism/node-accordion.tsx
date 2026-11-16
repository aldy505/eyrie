import { useId } from "react";
import type { Metadata, RegionData, SingleMonitor } from "@/lib/status-dashboard";
import { UptimeBars } from "@/components/uptime-bars";
import { getResponseTimeTone } from "@/lib/design-config";
import { RegionRow } from "./region-row";
import { summarizeMonitor } from "./monitor-health";

type NodeAccordionProps = {
  monitor: SingleMonitor;
  regions: RegionData["monitors"];
  metadata: Metadata;
  isOpen: boolean;
  onToggle: () => void;
};

export function NodeAccordion({
  monitor,
  regions,
  metadata,
  isOpen,
  onToggle,
}: NodeAccordionProps) {
  const detailsId = useId();
  const health = summarizeMonitor(monitor, regions, metadata);

  return (
    <div className={`neobrutalism-node${isOpen ? " open" : ""}`}>
      <button
        type="button"
        className="neobrutalism-node-summary"
        aria-expanded={isOpen}
        aria-controls={detailsId}
        onClick={onToggle}
      >
        <span className="neobrutalism-node-heading">
          <span className="neobrutalism-node-toggle">[{isOpen ? "-" : "+"}]</span>
          <span className="neobrutalism-node-name">{monitor.name}</span>
        </span>

        <UptimeBars
          monitor={monitor}
          metadata={metadata}
          className="neobrutalism-node-spark"
          barClassName="neobrutalism-uptime-bars-bar"
          noDataBarClassName="neobrutalism-uptime-bars-nodata"
        />

        <span className="neobrutalism-node-meta">
          <span
            className={`neobrutalism-node-count ${health.healthy === health.total ? "ok" : "warn"}`}
          >
            {health.healthy}/{health.total} {health.healthy === health.total ? "OK" : "!"}
          </span>
          <span
            className={`neobrutalism-node-latency ${getResponseTimeTone(health.worstLatencyMs)}`}
          >
            {Math.round(health.worstLatencyMs)}ms
          </span>
          <span className={`neobrutalism-status-square ${health.worstStatus}`} />
        </span>
      </button>

      {isOpen && (
        <div className="neobrutalism-node-details" id={detailsId}>
          {regions.length === 0 ? (
            <div className="neobrutalism-node-empty">No regional probes reported</div>
          ) : (
            regions.map((region, index) => (
              <RegionRow
                key={region.region}
                region={region}
                metadata={metadata}
                isLast={index === regions.length - 1}
              />
            ))
          )}
        </div>
      )}
    </div>
  );
}
