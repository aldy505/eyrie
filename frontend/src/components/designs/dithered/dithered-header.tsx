import type { SummaryStats } from "@/lib/status-dashboard";

type DitheredHeaderProps = {
  lastUpdated: Date | null;
  stats: SummaryStats;
};

export function DitheredHeader({ lastUpdated, stats }: DitheredHeaderProps) {
  return (
    <header className="dithered-header">
      <h1>SYSTEM STATUS</h1>
      {lastUpdated && (
        <div className="dithered-last-scan">
          Last scan: {lastUpdated.toLocaleString()}
        </div>
      )}
      <div className="dithered-stats">
        <div className="dithered-stat-pill healthy">
          <span className="dithered-stat-value">{stats.healthy}</span>
          <span className="dithered-stat-label">Healthy</span>
        </div>
        <div className="dithered-stat-pill degraded">
          <span className="dithered-stat-value">{stats.degraded}</span>
          <span className="dithered-stat-label">Degraded</span>
        </div>
        <div className="dithered-stat-pill down">
          <span className="dithered-stat-value">{stats.down}</span>
          <span className="dithered-stat-label">Down</span>
        </div>
        <div className="dithered-stat-pill incidents">
          <span className="dithered-stat-value">{stats.incidents}</span>
          <span className="dithered-stat-label">Incidents</span>
        </div>
      </div>
    </header>
  );
}
