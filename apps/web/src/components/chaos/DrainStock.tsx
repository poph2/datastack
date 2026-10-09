"use client";

import { api } from "@/lib/api";
import type { DrainResponse } from "@/lib/types";
import { useState } from "react";
import { Button } from "../ui";

/** Inventory only: set the stock of N products to zero in one go. */
export function DrainStock({ onDrained }: { onDrained: (r: DrainResponse) => void }) {
  const [products, setProducts] = useState(20);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function drain() {
    setBusy(true);
    setError(null);
    try {
      const r = await api<DrainResponse>("/api/chaos/inventory/drain-stock", {
        method: "POST",
        body: JSON.stringify({ products }),
      });
      onDrained(r);
      const ids = r.product_ids.slice(0, 6).join(", ");
      setResult(
        `Drained ${r.drained}: ${ids}${r.product_ids.length > 6 ? ` +${r.product_ids.length - 6} more` : ""}`,
      );
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="rounded-lg border border-rose-500/30 bg-rose-500/5 p-3">
      <p className="mb-2 font-semibold text-rose-200">Drain stock</p>
      <div className="flex flex-wrap items-center gap-2">
        <input
          type="number"
          min={1}
          max={100}
          value={products}
          onChange={(e) => setProducts(Math.min(100, Math.max(1, Number(e.target.value) || 1)))}
          className="w-20 rounded-md border border-slate-700 bg-slate-950 px-2 py-1 text-right font-mono text-sm text-slate-100"
          aria-label="products to drain"
        />
        <span className="text-sm text-slate-400">products to zero</span>
        <Button variant="danger" disabled={busy} onClick={() => void drain()} className="ml-auto">
          {busy ? "Draining…" : "Drain"}
        </Button>
      </div>
      <p className="mt-2 text-xs text-slate-400">
        Sets stock_level to 0 on random products; checkouts for them fail with OUT_OF_STOCK
        until restock (pause it above to keep them empty).
      </p>
      {result && <p className="mt-1 text-sm text-emerald-300">{result}</p>}
      {error && <p className="mt-1 text-sm text-rose-300">{error}</p>}
    </div>
  );
}
