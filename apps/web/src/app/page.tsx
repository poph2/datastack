"use client";

import { StatusBadge } from "@/components/StatusBadge";
import { backendUrl, grafanaUrl } from "@/lib/config";
import { useEffect, useState } from "react";

export default function OverviewPage() {
  const [backendOk, setBackendOk] = useState<boolean | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function check() {
      try {
        const res = await fetch(`${backendUrl()}/healthz`);
        if (!cancelled) setBackendOk(res.ok);
      } catch {
        if (!cancelled) setBackendOk(false);
      }
    }

    check();
    const id = setInterval(check, 10_000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  const dashboardSrc = `${grafanaUrl()}/d/datastack-overview/datastack-overview?orgId=1&kiosk&theme=light&refresh=10s`;

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-zinc-900 dark:text-zinc-50">
          Overview
        </h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">
          Live status of every service, database, and pipeline in the stack.
        </p>
      </div>

      <div className="flex flex-wrap gap-2">
        <StatusBadge ok={backendOk} label="Backend API" />
      </div>

      <div className="overflow-hidden rounded-lg border border-zinc-200 bg-white shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
        <div className="flex items-center justify-between border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
          <h2 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">
            Grafana &middot; DataStack Overview
          </h2>
          <a
            href={`${grafanaUrl()}/d/datastack-overview/datastack-overview`}
            target="_blank"
            rel="noopener noreferrer"
            className="text-xs font-medium text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100"
          >
            Open in Grafana ↗
          </a>
        </div>
        <iframe
          src={dashboardSrc}
          title="DataStack Grafana overview dashboard"
          className="h-[900px] w-full"
        />
      </div>
    </div>
  );
}
