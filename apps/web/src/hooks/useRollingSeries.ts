"use client";

import { useEffect, useState } from "react";

/**
 * Polls `fetcher` on an interval and keeps a rolling window of snapshots,
 * each tagged with a wall-clock time label, for use in time-series charts.
 * `fetcher` should be a stable reference (e.g. a module-level function).
 */
export function useRollingSeries<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
  maxPoints: number,
) {
  const [history, setHistory] = useState<({ time: string } & { data: T })[]>(
    [],
  );
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function tick() {
      try {
        const data = await fetcher();
        if (cancelled) return;
        const time = new Date().toLocaleTimeString();
        setHistory((prev) => [...prev.slice(-(maxPoints - 1)), { time, data }]);
        setError(null);
      } catch {
        if (!cancelled) setError("Could not reach the data source.");
      }
    }

    tick();
    const id = setInterval(tick, intervalMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [fetcher, intervalMs, maxPoints]);

  return {
    latest: history.at(-1)?.data ?? null,
    history,
    error,
  };
}
