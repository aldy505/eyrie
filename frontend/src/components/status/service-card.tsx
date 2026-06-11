import { cn } from "@/lib/utils";
import type { Metadata, ServiceEntry, TLSInfo } from "@/lib/status-dashboard";
import { formatLatency, formatStatus, statusTone } from "@/lib/status-dashboard";
import { UptimeSparkline } from "@/components/status/uptime-sparkline";
import { AlertTriangle, Lock, Unlock } from "lucide-react";

type ServiceCardProps = {
  service: ServiceEntry;
  metadata: Metadata;
};

const STATUS_BG_CLASSES = {
  healthy: "bg-[#0d1117]",
  degraded: "bg-amber-500/10",
  down: "bg-rose-500/10",
} as const;

const STATUS_BORDER_CLASSES = {
  healthy: "border-white/10",
  degraded: "border-amber-400/20",
  down: "border-rose-400/20",
} as const;

type IncidentStatus = keyof typeof STATUS_BG_CLASSES;

function formatCertExpiry(dateStr: string | undefined): string {
  if (!dateStr) return "Unknown";
  try {
    const date = new Date(dateStr);
    return date.toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" });
  } catch {
    return dateStr;
  }
}

function TLSIndicator({ tls }: { tls: TLSInfo | null | undefined }) {
  if (!tls?.is_https) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-slate-500/20 px-2 py-0.5 text-[10px] font-medium text-slate-400">
        <Unlock className="h-3 w-3" />
        HTTP
      </span>
    );
  }

  if (tls.is_expired) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-rose-500/20 px-2 py-0.5 text-[10px] font-medium text-rose-300">
        <AlertTriangle className="h-3 w-3" />
        CERT EXPIRED
      </span>
    );
  }

  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-emerald-500/20 px-2 py-0.5 text-[10px] font-medium text-emerald-300">
      <Lock className="h-3 w-3" />
      HTTPS
    </span>
  );
}

function TLSCertDetails({ tls }: { tls: TLSInfo | null | undefined }) {
  if (!tls?.is_https || !tls.subject) return null;

  return (
    <div className="mt-3 space-y-1 text-[10px] text-slate-500">
      <p className="truncate">
        <span className="text-slate-400">Subject:</span> {tls.subject}
      </p>
      {tls.issuer && tls.issuer !== tls.subject && (
        <p className="truncate">
          <span className="text-slate-400">Issuer:</span> {tls.issuer}
        </p>
      )}
      {tls.not_after && (
        <p className={`truncate ${tls.is_expired ? "text-rose-400" : "text-slate-400"}`}>
          <span className="text-slate-400">Expires:</span> {formatCertExpiry(tls.not_after)}
          {tls.is_expired && " (EXPIRED)"}
        </p>
      )}
    </div>
  );
}

export function ServiceCard({ service, metadata }: ServiceCardProps) {
  const status: IncidentStatus = (service.incident?.status ?? "healthy") as IncidentStatus;
  const probeType = (service.incident?.probe_type ?? "http").toUpperCase();
  const secondaryLine = service.monitor.description || service.monitor.id;
  const tls = service.monitor.tls;

  const bgClass = STATUS_BG_CLASSES[status] ?? STATUS_BG_CLASSES.healthy;
  const borderClass = STATUS_BORDER_CLASSES[status] ?? STATUS_BORDER_CLASSES.healthy;

  return (
    <article className={`flex min-h-[290px] flex-col rounded-[24px] border ${borderClass} ${bgClass} p-5 shadow-[0_20px_50px_-35px_rgba(0,0,0,0.85)] transition-colors hover:border-white/15`}>
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h2 className="break-words text-[15px] font-medium text-white">{service.monitor.name}</h2>
        </div>
        <div className="flex items-center gap-2">
          <TLSIndicator tls={tls} />
          <span
            className={cn(
              "shrink-0 rounded-full px-2.5 py-1 text-[12px] font-medium",
              statusTone[status] ?? statusTone.healthy,
            )}
          >
            {formatStatus(status)}
          </span>
        </div>
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
        <TLSCertDetails tls={tls} />
      </div>
    </article>
  );
}
