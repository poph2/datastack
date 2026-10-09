"use client";

import { api, ApiRequestError } from "@/lib/api";
import type { ChaosKnob, ChaosState, DrainResponse } from "@/lib/types";
import { useEffect, useState } from "react";
import { Badge, Button, Card, Spinner } from "../ui";
import { DrainStock } from "./DrainStock";

const POLL_MS = 5_000;

type Load =
  | { status: "loading" }
  | { status: "ready"; data: ChaosState }
  | { status: "missing"; message: string }
  | { status: "error"; message: string };

async function fetchChaos(service: string): Promise<Load> {
  try {
    return { status: "ready", data: await api<ChaosState>(`/api/chaos/${service}`) };
  } catch (err) {
    const e = err as ApiRequestError;
    return e.status === 404
      ? { status: "missing", message: e.message }
      : { status: "error", message: e.message };
  }
}

export function isToggle(knob: ChaosKnob): boolean {
  return knob.min === 0 && knob.max === 1 && /_(paused|enabled|disabled)$/.test(knob.name);
}

function stepFor(knob: ChaosKnob): number {
  const span = knob.max - knob.min;
  if (span <= 1) return 0.01;
  if (span <= 100) return 1;
  return Math.max(1, Math.round(span / 100));
}

function formatValue(knob: ChaosKnob, value: number): string {
  if (isToggle(knob)) return value ? "on" : "off";
  if (knob.name.endsWith("_ms")) return `${value.toLocaleString()} ms`;
  if (knob.name.endsWith("_rate")) return `${Math.round(value * 100)}%`;
  if (knob.name.endsWith("_factor")) return `×${value}`;
  return value.toLocaleString();
}

function label(name: string): string {
  return name.replace(/_ms$/, "").replace(/_/g, " ");
}

export function ChaosCard({ service, refreshKey }: { service: string; refreshKey: number }) {
  const [load, setLoad] = useState<Load>({ status: "loading" });
  const [draft, setDraft] = useState<Record<string, number>>({});
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ tone: "good" | "bad"; text: string } | null>(null);

  useEffect(() => {
    let cancelled = false;
    const tick = () =>
      fetchChaos(service).then((next) => {
        if (!cancelled) setLoad(next);
      });
    tick();
    const id = setInterval(tick, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [service, refreshKey]);

  async function send(method: "PUT" | "DELETE", body?: Record<string, number>) {
    setBusy(true);
    setNotice(null);
    try {
      const data = await api<ChaosState>(`/api/chaos/${service}`, {
        method,
        body: body ? JSON.stringify(body) : undefined,
      });
      setLoad({ status: "ready", data });
      setDraft({});
      setNotice({ tone: "good", text: method === "DELETE" ? "Reset to defaults" : "Applied" });
    } catch (err) {
      setNotice({ tone: "bad", text: (err as Error).message });
    } finally {
      setBusy(false);
    }
  }

  const knobs = load.status === "ready" ? load.data.knobs : [];
  const active = knobs.filter((k) => k.value !== k.default);
  const dirty = Object.entries(draft).filter(
    ([name, v]) => knobs.find((k) => k.name === name)?.value !== v,
  );

  return (
    <Card className={`flex flex-col gap-4 ${active.length ? "ring-1 ring-amber-500/50" : ""}`}>
      <header className="flex items-center justify-between gap-2">
        <h2 className="text-xl font-bold text-white capitalize">{service}</h2>
        {load.status === "ready" &&
          (active.length ? (
            <Badge tone="warn">⚠ chaos active</Badge>
          ) : (
            <Badge tone="good">✓ healthy defaults</Badge>
          ))}
        {load.status === "missing" && <Badge>not available</Badge>}
        {load.status === "error" && <Badge tone="bad">unreachable</Badge>}
      </header>

      {load.status === "loading" && (
        <p className="flex items-center gap-2 text-slate-400">
          <Spinner /> Loading knobs…
        </p>
      )}
      {load.status === "missing" && (
        <p className="text-sm text-slate-400">
          This service has no chaos endpoints yet (<code>/admin/chaos</code> answered 404).
          Redeploy it with the chaos build.
        </p>
      )}
      {load.status === "error" && (
        <p className="text-sm text-rose-300">{load.message}</p>
      )}

      {load.status === "ready" && (
        <>
          <ul className="flex flex-col gap-4">
            {knobs.map((knob) => {
              const value = draft[knob.name] ?? knob.value;
              const changed = knob.value !== knob.default;
              const pending = draft[knob.name] !== undefined && draft[knob.name] !== knob.value;
              const set = (v: number) => setDraft((d) => ({ ...d, [knob.name]: v }));
              return (
                <li key={knob.name}>
                  <div className="mb-1 flex items-baseline justify-between gap-2">
                    <span className="font-semibold text-slate-100 capitalize">
                      {label(knob.name)}
                      {pending && <span className="ml-1 text-sky-400" title="not applied yet">•</span>}
                    </span>
                    <span
                      className={`font-mono text-base tabular-nums ${changed ? "text-amber-300" : "text-slate-300"}`}
                    >
                      {formatValue(knob, value)}
                    </span>
                  </div>
                  {isToggle(knob) ? (
                    <button
                      type="button"
                      role="switch"
                      aria-checked={value === 1}
                      onClick={() => set(value === 1 ? 0 : 1)}
                      className={`relative h-7 w-12 rounded-full transition-colors ${
                        value === 1 ? "bg-amber-500" : "bg-slate-700"
                      }`}
                    >
                      <span
                        className={`absolute top-1 left-1 h-5 w-5 rounded-full bg-white transition-transform ${
                          value === 1 ? "translate-x-5" : ""
                        }`}
                      />
                    </button>
                  ) : (
                    <div className="flex items-center gap-3">
                      <input
                        type="range"
                        min={knob.min}
                        max={knob.max}
                        step={stepFor(knob)}
                        value={value}
                        onChange={(e) => set(Number(e.target.value))}
                        className="flex-1"
                        aria-label={knob.name}
                      />
                      <input
                        type="number"
                        min={knob.min}
                        max={knob.max}
                        step="any"
                        value={value}
                        onChange={(e) => {
                          const v = Number(e.target.value);
                          if (e.target.value !== "" && Number.isFinite(v)) set(v);
                        }}
                        className="w-24 rounded-md border border-slate-700 bg-slate-950 px-2 py-1 text-right font-mono text-sm text-slate-100"
                      />
                    </div>
                  )}
                  <p className="mt-1 text-xs text-slate-400">
                    {knob.description}{" "}
                    <span className="text-slate-500">
                      (default {formatValue(knob, knob.default)}, {knob.min}–{knob.max})
                    </span>
                  </p>
                </li>
              );
            })}
          </ul>

          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="primary"
              disabled={busy || dirty.length === 0}
              onClick={() => void send("PUT", Object.fromEntries(dirty))}
            >
              Apply{dirty.length ? ` (${dirty.length})` : ""}
            </Button>
            <Button disabled={busy || (active.length === 0 && dirty.length === 0)} onClick={() => void send("DELETE")}>
              Reset
            </Button>
            {dirty.length > 0 && (
              <Button variant="ghost" disabled={busy} onClick={() => setDraft({})}>
                Discard
              </Button>
            )}
            {busy && <Spinner />}
            {notice && (
              <span className={`text-sm ${notice.tone === "good" ? "text-emerald-300" : "text-rose-300"}`}>
                {notice.text}
              </span>
            )}
          </div>

          {service === "inventory" && (
            <DrainStock onDrained={(r: DrainResponse) => setNotice({ tone: "good", text: `Drained ${r.drained} products` })} />
          )}
        </>
      )}
    </Card>
  );
}
