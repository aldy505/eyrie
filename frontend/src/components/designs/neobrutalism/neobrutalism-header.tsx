import type { ThemeMode } from "@/hooks/use-theme";

type NeobrutalismHeaderProps = {
  lastUpdated: Date | null;
  theme: ThemeMode;
  onToggleTheme: () => void;
  allExpanded: boolean;
  onToggleAll: () => void;
};

export function NeobrutalismHeader({
  lastUpdated,
  theme,
  onToggleTheme,
  allExpanded,
  onToggleAll,
}: NeobrutalismHeaderProps) {
  return (
    <header className="neobrutalism-header">
      <h1>STATUS</h1>
      <div className="neobrutalism-header-right">
        {lastUpdated && (
          <time className="neobrutalism-last-scan" dateTime={lastUpdated.toISOString()}>
            Last scan: {lastUpdated.toLocaleString()}
          </time>
        )}
        <button className="neobrutalism-control" onClick={onToggleAll} type="button">
          [{allExpanded ? "COLLAPSE ALL" : "EXPAND ALL"}]
        </button>
        <button className="neobrutalism-control" onClick={onToggleTheme} type="button">
          {theme === "dark" ? "○ LIGHT" : "● DARK"}
        </button>
      </div>
    </header>
  );
}
