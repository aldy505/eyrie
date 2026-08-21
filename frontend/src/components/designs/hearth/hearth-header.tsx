import { useTheme } from "@/hooks/use-theme";

type HearthHeaderProps = {
  lastUpdated: Date | null;
};

export function HearthHeader({ lastUpdated }: HearthHeaderProps) {
  const { theme, toggleTheme } = useTheme();

  return (
    <header className="hearth-header">
      <div className="hearth-header-row">
        <div>
          <h1>Home dashboard</h1>
          <p className="hearth-subtitle">All your services, at a glance</p>
        </div>
        <div className="hearth-header-right">
          {lastUpdated && (
            <div className="hearth-last-scan">
              Last checked: {lastUpdated.toLocaleString()}
            </div>
          )}
          <button
            className="hearth-theme-toggle"
            onClick={toggleTheme}
            type="button"
          >
            {theme === "dark" ? "☀ Light" : "● Dark"}
          </button>
        </div>
      </div>
    </header>
  );
}
