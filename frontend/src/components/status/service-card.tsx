import { cn } from "@/lib/utils";
import type { Metadata, ServiceEntry } from "@/lib/status-dashboard";
import { formatLatency, formatStatus, statusTone } from "@/lib/status-dashboard";
import { UptimeSparkline } from "@/components/status/uptime-sparkline";

type ServiceCardProps = {
  service: ServiceEntry;
  metadata: Metadata;
};

export function ServiceCard({ service, metadata }: ServiceCardProps) {
  const status = service.incident?.status ?? "healthy";
  const probeType = (service.incident?.probe_type ?? "http").toUpperCase();
  const secondaryLine = service.monitor.description || service.monitor.id;

  const bgClass = {
    healthy: "bg-[#0d1117]",
    degraded: "bg-amber-500/10",
    down: "bg-rose-500/10",
  }[status];

  const borderClass = {
    healthy: "border-white/10",
    degraded: "border-amber-400/20",
    down: "border-rose-400/20",
  }[status];

  return (
    <article className={`flex min-h-[290px] flex-col rounded-[24px] border ${borderClass} ${bgClass} p-5 shadow-[0_20px_50px_-35px_rgba(0,0,0,0.85)] transition-colors hover:border-white/15`}>
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h2 className="break-words text-[15px] font-medium text-white">{service.monitor.name}</h2>
        </div>
        <span
          className={cn(
            "shrink-0 rounded-full px-2.5 py-1 text-[12px] font-medium",
            statusTone[status] ?? statusTone.healthy,
          )}
        >
          {formatStatus(status)}
        </span>
      </div>

      <div className="mt-3 space-y-1.5 text-[12px] leading-relaxed text-slate-400">
        <p className="truncate">{service.groupLabel} • {probeType}</p>
        <p className="truncate text-slate-500">{secondaryLine}</p>
      </div>

      <div className="mt-5 flex-1 rounded-[20px] border border-white/[0.06] bg-black/20 p-4">
        <div className="mb-3 flex items-center justify-between gap-3 text-[11px] uppercase tracking-[0.18em] text-slate-500">
          <span>{service.regions.length > 0 ? `${service.regions.length} regions` : "Region sync pending"}</span>
          <span className="tracking-normal text-[12px] font-medium text-slate-200">
            {formatLatency(service.monitor.response_time_ms)}
          </span>
        </div>
        <UptimeSparkline monitor={service.monitor} metadata={metadata} />
      </div>
    </article>
  );
}
