"use client";

import type { ServiceStatus } from "@/app/api/dashboard/services/route";
import { useRollingSeries } from "@/hooks/useRollingSeries";

async function fetchServices(): Promise<ServiceStatus[]> {
  const res = await fetch("/api/dashboard/services");
  if (!res.ok) throw new Error("failed");
  return res.json();
}

export function ServicesGrid() {
  const { latest, error } = useRollingSeries(fetchServices, 5_000, 1);

  return (
    <div className="rounded-lg border border-zinc-200 bg-white p-5 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
      <h2 className="mb-4 text-sm font-medium text-zinc-700 dark:text-zinc-300">
        Services
      </h2>
      {error && !latest && (
        <p className="text-sm text-zinc-400 dark:text-zinc-500">{error}</p>
      )}
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">
        {(latest ?? []).map((service) => (
          <div
            key={service.instance || service.job}
            className="flex items-center justify-between gap-2 rounded-md border border-zinc-100 px-3 py-2 dark:border-zinc-800"
          >
            <span className="truncate text-sm text-zinc-700 dark:text-zinc-300">
              {service.service}
            </span>
            <span
              className={`h-2 w-2 shrink-0 rounded-full ${
                service.up ? "bg-emerald-500" : "bg-red-500"
              }`}
              title={service.up ? "up" : "down"}
            />
          </div>
        ))}
      </div>
    </div>
  );
}
