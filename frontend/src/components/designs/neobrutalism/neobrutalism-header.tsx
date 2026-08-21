import { useTheme } from "@/hooks/use-theme";

type NeobrutalismHeaderProps = {
  lastUpdated: Date | null;
};

export function NeobrutalismHeader({ lastUpdated }: NeobrutalismHeaderProps) {
  const { theme, toggleTheme } = useTheme();

  return (
    <header className="neobrutalism-header">
      <h1>STATUS</h1>
      <div className="neobrutalism-header-right">
        {lastUpdated && (
          <div className="neobrutalism-last-scan">
            Last scan: {lastUpdated.toLocaleString()}
          </div>
        )}
        <button
          className="neobrutalism-theme-toggle"
          onClick={toggleTheme}
          type="button"
        >
          {theme === "dark" ? "☀ Light" : "● Dark"}
        </button>
      </div>
    </header>
  );
}
