import { useState, useRef, useEffect, useCallback } from "react";

const STORAGE_KEY = "eyrie_auto_refresh_enabled";
const AUTO_REFRESH_INTERVAL = 60 * 1000; // 60 seconds in milliseconds
const DOUBLE_CLICK_DELAY = 300; // milliseconds

export function useAutoRefresh(onRefresh: () => void, isRefreshing = false) {
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
  const onRefreshRef = useRef(onRefresh);

  // Keep ref in sync with the latest callback
  useEffect(() => {
    onRefreshRef.current = onRefresh;
  }, [onRefresh]);

  // Setup/cleanup auto-refresh interval
  useEffect(() => {
    if (isAutoRefreshEnabled) {
      autoRefreshIntervalRef.current = setInterval(() => {
        // Skip refresh if already refreshing
        if (!isRefreshing) {
          onRefreshRef.current();
        }
      }, AUTO_REFRESH_INTERVAL);
    }

    return () => {
      if (autoRefreshIntervalRef.current) {
        clearInterval(autoRefreshIntervalRef.current);
      }
    };
  }, [isAutoRefreshEnabled, isRefreshing]);

  // Persist state to localStorage
  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, String(isAutoRefreshEnabled));
    } catch {
      // localStorage might not be available, silently fail
    }
  }, [isAutoRefreshEnabled]);

  const handleRefreshClick = useCallback(() => {
    clickCountRef.current += 1;

    if (clickCountRef.current === 1) {
      // First click
      clickTimeoutRef.current = setTimeout(() => {
        if (clickCountRef.current === 1) {
          // Single click - perform manual refresh
          onRefreshRef.current();
        }
        // Reset for next interaction
        clickCountRef.current = 0;
      }, DOUBLE_CLICK_DELAY);
    } else if (clickCountRef.current === 2) {
      // Double click detected
      if (clickTimeoutRef.current) {
        clearTimeout(clickTimeoutRef.current);
        clickTimeoutRef.current = null;
      }
      // Toggle auto-refresh
      setIsAutoRefreshEnabled((prev) => !prev);
      // Reset for next interaction
      clickCountRef.current = 0;
    }
  }, []);

  // Cleanup pending timers on unmount
  useEffect(() => {
    return () => {
      if (clickTimeoutRef.current) {
        clearTimeout(clickTimeoutRef.current);
        clickTimeoutRef.current = null;
      }
    };
  }, []);

  return { isAutoRefreshEnabled, handleRefreshClick };
}

