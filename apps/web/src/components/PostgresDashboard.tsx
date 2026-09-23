"use client";

import type { PostgresDatabase } from "@/app/api/dashboard/postgres/route";
import {
  DatabaseLineChart,
  type ChartPoint,
} from "@/components/DatabaseLineChart";
import { useRollingSeries } from "@/hooks/useRollingSeries";

async function fetchPostgres(): Promise<PostgresDatabase[]> {
  const res = await fetch("/api/dashboard/postgres");
  if (!res.ok) throw new Error("failed");
  return res.json();
}

export function PostgresDashboard() {
  const { latest, history, error } = useRollingSeries(fetchPostgres, 5_000, 30);

  const databases = (latest ?? []).map((db) => db.database);

  const connectionsData: ChartPoint[] = history.map(({ time, data }) => {
    const point: ChartPoint = { time };
    for (const db of data) point[db.database] = db.connections;
    return point;
  });

  const sizeData: ChartPoint[] = history.map(({ time, data }) => {
    const point: ChartPoint = { time };
    for (const db of data) point[db.database] = db.sizeBytes / (1024 * 1024);
    return point;
  });

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">
          Postgres &middot; per database
        </h2>
        {error && !latest && (
          <span className="text-xs text-zinc-400 dark:text-zinc-500">
            {error}
          </span>
        )}
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        <DatabaseLineChart
          title="Active connections"
          data={connectionsData}
          seriesKeys={databases}
        />
        <DatabaseLineChart
          title="Database size (MB)"
          data={sizeData}
          seriesKeys={databases}
          valueFormatter={(v) => v.toFixed(1)}
        />
      </div>
    </div>
  );
}
