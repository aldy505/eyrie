import { Activity, Radar, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useAutoRefresh } from "@/hooks/use-auto-refresh";
import type {
  DashboardLayoutMode,
  Metadata,
  SummaryStats,
} from "@/lib/status-dashboard";
import { cn } from "@/lib/utils";

type DashboardHeaderProps = {
  metadata: Metadata | null;
  stats: SummaryStats;
  layoutMode: DashboardLayoutMode;
  isRefreshing: boolean;
  lastUpdated: Date | null;
  onRefresh: () => void;
  onToggleLayout: () => void;
};

const statItems: Array<{
  key: keyof SummaryStats;
  label: string;
  tone: string;
}> = [
  { key: "healthy", label: "Healthy", tone: "text-emerald-200" },
  { key: "degraded", label: "Degraded", tone: "text-amber-200" },
  { key: "down", label: "Down", tone: "text-rose-200" },
  { key: "incidents", label: "Incidents", tone: "text-sky-200" },
];

export function DashboardHeader({
  metadata,
  stats,
  layoutMode,
  isRefreshing,
  lastUpdated,
  onRefresh,
  onToggleLayout,
}: DashboardHeaderProps) {
  const { isAutoRefreshEnabled, handleRefreshClick } = useAutoRefresh(onRefresh, isRefreshing);
  const subtitle =
    layoutMode === "grid"
      ? "Dense pulse view for large monitor fleets."
      : "Detailed grouped view for monitor drilldowns.";

  return (
    <header className="border-b border-white/10 bg-black/15 backdrop-blur">
      <div className="px-6 py-4 sm:px-8 xl:px-10">
        <nav className="flex items-center justify-between gap-4">
          <div className="inline-flex items-center gap-3 rounded-full border border-white/10 bg-white/[0.03] px-4 py-2">
            <span className="flex h-8 w-8 items-center justify-center rounded-full bg-sky-400/10 text-sky-200">
              <Radar className="h-4 w-4" />
            </span>
            <div>
              <p className="text-sm font-medium text-white">Eyrie</p>
              <p className="text-[11px] uppercase tracking-[0.22em] text-slate-500">
                status surface
              </p>
            </div>
          </div>
          <div className="hidden text-[11px] uppercase tracking-[0.25em] text-slate-500 md:block">
            {metadata?.title ?? "Status Page"}
          </div>
        </nav>

        <div className="mt-6 flex flex-col gap-6 xl:flex-row xl:items-start xl:justify-between">
          <div className="space-y-3">
            <div>
              <h1 className="text-3xl font-semibold tracking-tight text-white sm:text-4xl">Eyrie</h1>
              <p className="mt-2 max-w-2xl text-sm text-slate-400 sm:text-[15px]">{subtitle}</p>
            </div>
          </div>

          <div className="flex items-center gap-2 self-start">
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={onToggleLayout}
              aria-label={layoutMode === "grid" ? "Switch to classic layout" : "Switch to pulse grid"}
              aria-pressed={layoutMode === "grid"}
              title={layoutMode === "grid" ? "Switch to classic layout" : "Switch to pulse grid"}
              className={cn(
                "rounded-full border border-white/10 bg-white/[0.03] text-slate-300 hover:bg-white/[0.08] hover:text-white",
                layoutMode === "grid" && "border-emerald-400/30 bg-emerald-500/10 text-emerald-200",
              )}
            >
              <Activity className={cn("h-4 w-4", layoutMode === "grid" && "animate-pulse")} />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={handleRefreshClick}
              aria-label={isAutoRefreshEnabled ? "Auto-refresh enabled (double-click to disable)" : "Single-click to refresh, double-click for auto-refresh"}
              disabled={isRefreshing}
              title={isAutoRefreshEnabled ? "Auto-refresh enabled (double-click to disable)" : "Single-click to refresh, double-click for auto-refresh"}
              className={cn(
                "rounded-full border border-white/10 bg-white/[0.03] text-slate-300 hover:bg-white/[0.08] hover:text-white transition-colors",
                isAutoRefreshEnabled && "border-sky-400/30 bg-sky-500/10 text-sky-200",
              )}
            >
              <RefreshCw className={cn("h-4 w-4", isRefreshing && "animate-spin")} />
            </Button>
          </div>
        </div>

        <div className="mt-6 flex flex-wrap items-center gap-3 rounded-[20px] border border-white/10 bg-[#0d1117]/80 px-4 py-3">
          {statItems.map((item) => (
            <span
              key={item.key}
              className={cn("inline-flex items-center gap-2 text-sm text-slate-300", item.tone)}
            >
              <span className="font-medium">{item.label}</span>
              <span className="text-white">{stats[item.key]}</span>
            </span>
          ))}
          {metadata?.show_last_updated && (
            <span className="ml-auto text-xs text-slate-500">
              Last updated{" "}
              <span className="text-slate-300">
                {lastUpdated
                  ? lastUpdated.toLocaleString(undefined, {
                      dateStyle: "medium",
                      timeStyle: "short",
                    })
                  : "Loading..."}
              </span>
            </span>
          )}
        </div>
      </div>
    </header>
  );
}
