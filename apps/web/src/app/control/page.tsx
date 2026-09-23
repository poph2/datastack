"use client";

import { useCallback, useEffect, useState } from "react";
import { backendUrl } from "@/lib/config";

type ControlStatus = {
  orchestratorRunning: boolean;
  ingestionRunning: boolean;
};

type ComponentKey = "orchestrator" | "ingestion";

const components: {
  key: ComponentKey;
  title: string;
  description: string;
}[] = [
  {
    key: "orchestrator",
    title: "Scenario Orchestrator",
    description:
      "Drives realistic business scenarios across CRM, Orders, and Inventory simulators.",
  },
  {
    key: "ingestion",
    title: "Ingestion Worker",
    description:
      "Polls the Postgres source databases and loads new rows into ClickHouse / StarRocks.",
  },
];

export default function ControlPage() {
  const [status, setStatus] = useState<ControlStatus | null>(null);
  const [pending, setPending] = useState<ComponentKey | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const res = await fetch(`${backendUrl()}/api/control/status`);
      if (!res.ok) throw new Error(`status ${res.status}`);
      setStatus(await res.json());
      setError(null);
    } catch {
      setError("Could not reach the backend control API.");
    }
  }, []);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- initial poll on mount is intentional
    refresh();
    const id = setInterval(refresh, 5_000);
    return () => clearInterval(id);
  }, [refresh]);

  async function toggle(key: ComponentKey, running: boolean) {
    setPending(key);
    try {
      const action = running ? "stop" : "start";
      const res = await fetch(`${backendUrl()}/api/control/${key}/${action}`, {
        method: "POST",
      });
      if (!res.ok) throw new Error(`status ${res.status}`);
      setStatus(await res.json());
      setError(null);
    } catch {
      setError("Could not reach the backend control API.");
    } finally {
      setPending(null);
    }
  }

  const runningFor: Record<ComponentKey, boolean> = {
    orchestrator: status?.orchestratorRunning ?? false,
    ingestion: status?.ingestionRunning ?? false,
  };

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-zinc-900 dark:text-zinc-50">
          Control
        </h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">
          Start or stop the pipeline components that feed and move demo data.
        </p>
      </div>

      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-4 py-2 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/40 dark:text-red-400">
          {error}
        </div>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        {components.map((component) => {
          const running = runningFor[component.key];
          const busy = pending === component.key;
          return (
            <div
              key={component.key}
              className="flex flex-col gap-4 rounded-lg border border-zinc-200 bg-white p-5 shadow-sm dark:border-zinc-800 dark:bg-zinc-900"
            >
              <div className="flex items-start justify-between gap-2">
                <div>
                  <h2 className="font-medium text-zinc-900 dark:text-zinc-50">
                    {component.title}
                  </h2>
                  <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">
                    {component.description}
                  </p>
                </div>
                <span
                  className={`shrink-0 rounded-full px-2.5 py-1 text-xs font-medium ${
                    running
                      ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-400"
                      : "bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400"
                  }`}
                >
                  {running ? "Running" : "Stopped"}
                </span>
              </div>
              <button
                type="button"
                disabled={busy}
                onClick={() => toggle(component.key, running)}
                className={`self-start rounded-md px-4 py-2 text-sm font-medium transition-colors disabled:opacity-50 ${
                  running
                    ? "bg-red-600 text-white hover:bg-red-700"
                    : "bg-zinc-900 text-white hover:bg-zinc-700 dark:bg-zinc-50 dark:text-zinc-900 dark:hover:bg-zinc-200"
                }`}
              >
                {busy ? "Working…" : running ? "Stop" : "Start"}
              </button>
            </div>
          );
        })}
      </div>
    </div>
  );
}
