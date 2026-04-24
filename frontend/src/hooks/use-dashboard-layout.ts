import { useEffect, useState } from "react";
import type { DashboardLayoutMode } from "@/lib/status-dashboard";

const STORAGE_KEY = "eyrie.dashboard.layout";

function readStoredLayout(): DashboardLayoutMode | null {
  if (typeof window === "undefined") {
    return null;
  }

  const stored = window.localStorage.getItem(STORAGE_KEY);
  return stored === "grid" || stored === "classic" ? stored : null;
}

export function useDashboardLayout(defaultMode: DashboardLayoutMode = "classic") {
  const [layoutMode, setLayoutMode] = useState<DashboardLayoutMode>(
    () => readStoredLayout() ?? defaultMode,
  );

  useEffect(() => {
    if (typeof window !== "undefined") {
      window.localStorage.setItem(STORAGE_KEY, layoutMode);
    }
  }, [layoutMode]);

  return {
    layoutMode,
    setLayoutMode,
    toggleLayout: () => {
      setLayoutMode((current) => (current === "classic" ? "grid" : "classic"));
    },
  };
}
