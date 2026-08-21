import { useTheme } from "@/hooks/use-theme";

type HealthmapHeaderProps = {
  lastUpdated: Date | null;
};

export function HealthmapHeader({ lastUpdated }: HealthmapHeaderProps) {
  const { theme, toggleTheme } = useTheme();

  const lastScanText = lastUpdated
    ? `${Math.round((Date.now() - lastUpdated.getTime()) / 60000)} min ago`
    : "loading...";

  return (
    <header className="healthmap-header">
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
        <div>
          <h1>SYSTEM HEALTH</h1>
          <p className="last-scan">Last scan: {lastScanText}</p>
        </div>
        <button
          className="healthmap-theme-toggle"
          onClick={toggleTheme}
          aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} mode`}
        >
          {theme === "dark" ? "Light mode" : "Dark mode"}
        </button>
      </div>
    </header>
  );
}
