"use client";

import { api } from "@/lib/api";
import { useState } from "react";
import { Button, PageHeader } from "../ui";
import { ChaosCard } from "./ChaosCard";

export function ChaosBoard({ services }: { services: readonly string[] }) {
  const [refreshKey, setRefreshKey] = useState(0);
  const [busy, setBusy] = useState(false);
  const [summary, setSummary] = useState<string | null>(null);

  async function resetAll() {
    setBusy(true);
    setSummary(null);
    const results = await Promise.allSettled(
      services.map((s) => api(`/api/chaos/${s}`, { method: "DELETE" })),
    );
    const failed = services.filter((_, i) => results[i].status === "rejected");
    setSummary(
      failed.length
        ? `Reset ${services.length - failed.length} of ${services.length}; failed: ${failed.join(", ")}`
        : `All ${services.length} services reset to defaults`,
    );
    setRefreshKey((k) => k + 1);
    setBusy(false);
  }

  return (
    <>
      <PageHeader
        title="Chaos"
        subtitle="Runtime fault knobs on each service. Changes take effect immediately and show up in traces and dashboards."
        actions={
          <>
            <Button onClick={() => setRefreshKey((k) => k + 1)}>Refresh</Button>
            <Button variant="danger" disabled={busy} onClick={() => void resetAll()}>
              {busy ? "Resetting…" : "Reset all chaos"}
            </Button>
          </>
        }
      />
      {summary && <p className="mb-4 text-sm text-slate-300">{summary}</p>}
      <div className="grid gap-5 md:grid-cols-2 xl:grid-cols-3">
        {services.map((s) => (
          <ChaosCard key={s} service={s} refreshKey={refreshKey} />
        ))}
      </div>
    </>
  );
}
