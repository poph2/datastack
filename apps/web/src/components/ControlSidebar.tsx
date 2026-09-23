"use client";

import { backendUrl } from "@/lib/config";
import { useCallback, useEffect, useState } from "react";

type ControlStatus = {
  orchestratorRunning: boolean;
  ingestionRunning: boolean;
};

type ComponentKey = "orchestrator" | "ingestion";

const components: { key: ComponentKey; title: string }[] = [
  { key: "orchestrator", title: "Scenario Orchestrator" },
  { key: "ingestion", title: "Ingestion Worker" },
];

export function ControlSidebar() {
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
      setError("Backend unreachable");
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
      setError("Backend unreachable");
    } finally {
      setPending(null);
    }
  }

  const runningFor: Record<ComponentKey, boolean> = {
    orchestrator: status?.orchestratorRunning ?? false,
    ingestion: status?.ingestionRunning ?? false,
  };

  return (
    <aside className="flex w-full flex-col gap-4 rounded-lg border border-zinc-200 bg-white p-5 shadow-sm dark:border-zinc-800 dark:bg-zinc-900 lg:w-72">
      <h2 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">
        Control
      </h2>
      {error && (
        <p className="text-xs text-red-600 dark:text-red-400">{error}</p>
      )}
      {components.map((component) => {
        const running = runningFor[component.key];
        const busy = pending === component.key;
        return (
          <div
            key={component.key}
            className="flex flex-col gap-2 rounded-md border border-zinc-100 p-3 dark:border-zinc-800"
          >
            <div className="flex items-center justify-between gap-2">
              <span className="text-sm font-medium text-zinc-900 dark:text-zinc-50">
                {component.title}
              </span>
              <span
                className={`rounded-full px-2 py-0.5 text-xs font-medium ${
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
              className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors disabled:opacity-50 ${
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
    </aside>
  );
}
