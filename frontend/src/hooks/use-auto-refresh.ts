import { useState, useRef, useEffect } from "react";

const STORAGE_KEY = "eyrie_auto_refresh_enabled";
const AUTO_REFRESH_INTERVAL = 60 * 1000; // 60 seconds in milliseconds
const DOUBLE_CLICK_DELAY = 300; // milliseconds

export function useAutoRefresh(onRefresh: () => void) {
  const [isAutoRefreshEnabled, setIsAutoRefreshEnabled] = useState(() => {
    try {
      const stored = localStorage.getItem(STORAGE_KEY);
      return stored === "true";
    } catch {
      return false;
    }
  });

  const clickCountRef = useRef(0);
  const clickTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const autoRefreshIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Setup/cleanup auto-refresh interval
  useEffect(() => {
    if (isAutoRefreshEnabled) {
      autoRefreshIntervalRef.current = setInterval(() => {
        onRefresh();
      }, AUTO_REFRESH_INTERVAL);
    }

    return () => {
      if (autoRefreshIntervalRef.current) {
        clearInterval(autoRefreshIntervalRef.current);
      }
    };
  }, [isAutoRefreshEnabled, onRefresh]);

  // Persist state to localStorage
  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, String(isAutoRefreshEnabled));
    } catch {
      // localStorage might not be available, silently fail
    }
  }, [isAutoRefreshEnabled]);

  const handleRefreshClick = () => {
    clickCountRef.current += 1;

    if (clickCountRef.current === 1) {
      // First click
      clickTimeoutRef.current = setTimeout(() => {
        if (clickCountRef.current === 1) {
          // Single click - perform manual refresh
          onRefresh();
        }
        // Reset for next interaction
        clickCountRef.current = 0;
      }, DOUBLE_CLICK_DELAY);
    } else if (clickCountRef.current === 2) {
      // Double click detected
      if (clickTimeoutRef.current) {
        clearTimeout(clickTimeoutRef.current);
      }
      // Toggle auto-refresh
      setIsAutoRefreshEnabled((prev) => !prev);
      // Reset for next interaction
      clickCountRef.current = 0;
    }
  };

  return { isAutoRefreshEnabled, handleRefreshClick };
}

