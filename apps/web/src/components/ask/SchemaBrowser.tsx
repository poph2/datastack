"use client";

import { api, ApiRequestError } from "@/lib/api";
import type { SchemaResponse } from "@/lib/types";
import { useState } from "react";
import { Spinner } from "../ui";

/** The schema the analyzer shows the LLM, loaded the first time it's opened. */
export function SchemaBrowser() {
  const [schema, setSchema] = useState<SchemaResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [filter, setFilter] = useState("");

  async function load() {
    if (schema || loading) return;
    setLoading(true);
    setError(null);
    try {
      setSchema(await api<SchemaResponse>("/api/schema"));
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }

  const needle = filter.trim().toLowerCase();
  const tables =
    schema?.tables.filter(
      (t) =>
        !needle ||
        t.name.toLowerCase().includes(needle) ||
        t.columns.some((c) => c.name.toLowerCase().includes(needle)),
    ) ?? [];

  return (
    <details
      className="group rounded-xl border border-slate-800 bg-slate-900/60"
      onToggle={(e) => (e.currentTarget.open ? load() : undefined)}
    >
      <summary className="flex cursor-pointer list-none items-center justify-between px-5 py-3 text-base font-semibold text-slate-200 select-none">
        <span>Schema the model sees</span>
        <span className="text-slate-500 transition-transform group-open:rotate-90">▶</span>
      </summary>
      <div className="border-t border-slate-800 px-5 py-4">
        {loading && (
          <p className="flex items-center gap-2 text-slate-400">
            <Spinner /> Loading schema…
          </p>
        )}
        {error && (
          <p className="text-rose-300">
            Couldn&apos;t load the schema: {error}{" "}
            <button className="underline" onClick={() => { setError(null); void load(); }}>
              Retry
            </button>
          </p>
        )}
        {schema && (
          <>
            <div className="mb-4 flex flex-wrap items-center gap-3">
              <input
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder="Filter tables and columns"
                className="w-64 rounded-md border border-slate-700 bg-slate-950 px-3 py-1.5 text-sm text-slate-100 placeholder:text-slate-500"
              />
              <span className="text-xs text-slate-500">
                {schema.tables.length} tables · loaded {new Date(schema.loaded_at).toLocaleTimeString()}
              </span>
            </div>
            <div className="grid gap-3 md:grid-cols-2">
              {tables.map((t) => (
                <details key={t.name} className="rounded-lg border border-slate-800 bg-slate-950">
                  <summary className="cursor-pointer px-4 py-2">
                    <span className="font-mono font-semibold text-sky-300">{t.name}</span>
                    <span className="ml-2 text-xs text-slate-500">{t.columns.length} columns</span>
                    <p className="mt-1 line-clamp-2 text-sm text-slate-400">{t.comment}</p>
                  </summary>
                  <ul className="divide-y divide-slate-800 border-t border-slate-800 text-sm">
                    {t.columns.map((c) => (
                      <li key={c.name} className="px-4 py-2">
                        <span className="font-mono text-slate-100">{c.name}</span>
                        <span className="ml-2 font-mono text-xs text-amber-300/80">{c.type}</span>
                        {c.comment && <p className="mt-0.5 text-slate-400">{c.comment}</p>}
                      </li>
                    ))}
                  </ul>
                </details>
              ))}
            </div>
            {schema.joins.length > 0 && (
              <div className="mt-4">
                <h3 className="mb-2 text-sm font-semibold text-slate-300">Joins</h3>
                <ul className="flex flex-wrap gap-2 font-mono text-xs">
                  {schema.joins.map((j) => (
                    <li
                      key={`${j.from}-${j.to}`}
                      className="rounded-md bg-slate-800 px-2 py-1 text-slate-300"
                    >
                      {j.from} → {j.to}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </>
        )}
      </div>
    </details>
  );
}
