import { useTheme } from "@/hooks/use-theme";

type NewsroomHeaderProps = {
  lastUpdated: Date | null;
};

export function NewsroomHeader({ lastUpdated }: NewsroomHeaderProps) {
  const { theme, toggleTheme } = useTheme();

  const lastScanText = lastUpdated
    ? `${Math.round((Date.now() - lastUpdated.getTime()) / 60000)} min ago`
    : "loading...";

  return (
    <header className="newsroom-header">
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
        <div>
          <h1>UPTIME STATUS</h1>
          <p className="last-scan">Last scan: {lastScanText}</p>
        </div>
        <button
          className="newsroom-theme-toggle"
          onClick={toggleTheme}
          aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} mode`}
        >
          {theme === "dark" ? "Light mode" : "Dark mode"}
        </button>
      </div>
    </header>
  );
}
