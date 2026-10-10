"use client";

import { LAYERS, TOOLS, type Tool, toolsForLayer, WATCHED_WORKLOADS } from "@/lib/stack";
import { useEffect, useState } from "react";

type Health = Record<string, { ready: boolean; restarts: number }>;
const POLL_MS = 10_000;

/** A logo, or a monogram when the project has no mark of its own. */
function Mark({ tool }: { tool: Tool }) {
  if (!tool.logo) {
    const initials =
      tool.initials ??
      tool.name
        .replace(/[^A-Za-z0-9 /+.]/g, "")
        .split(/[ /]/)
        .filter(Boolean)
        .slice(0, 2)
        .map((w) => w[0]?.toUpperCase())
        .join("");
    return (
      <span
        aria-hidden
        className="flex h-12 w-12 items-center justify-center rounded-lg text-lg font-black tracking-tight"
        style={{ color: tool.brand, background: `${tool.brand}1f`, border: `1px solid ${tool.brand}40` }}
      >
        {initials}
      </span>
    );
  }
  if (tool.mode === "color") {
    // Artwork with its own colours, drawn as-is. These are official marks
    // designed for a white page (Tempo's and kind's wordmarks are near-black),
    // so they sit on a light plate rather than disappearing into the tile.
    // The plate is sized for a wide wordmark; object-contain keeps square
    // icons square inside it.
    return (
      <span aria-hidden className="flex h-12 w-28 items-center justify-center rounded-lg bg-slate-100 px-2 py-1">
        {/* Local static SVGs: next/image would add a loader for no benefit. */}
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={`/logos/${tool.logo}`} alt="" className="h-full w-full object-contain" />
      </span>
    );
  }
  // Single-path mark with no fill: paint it with the brand colour.
  return (
    <span
      aria-hidden
      className="block h-12 w-12"
      style={{
        backgroundColor: tool.brand,
        maskImage: `url(/logos/${tool.logo})`,
        WebkitMaskImage: `url(/logos/${tool.logo})`,
        maskRepeat: "no-repeat",
        WebkitMaskRepeat: "no-repeat",
        maskPosition: "center",
        WebkitMaskPosition: "center",
        maskSize: "contain",
        WebkitMaskSize: "contain",
      }}
    />
  );
}

function StatusDot({ state }: { state: "ready" | "down" | null }) {
  if (state === null) return null;
  const ready = state === "ready";
  return (
    <span
      title={ready ? "Running in the cluster" : "Not ready"}
      className={`absolute top-3 right-3 flex h-2.5 w-2.5 ${ready ? "" : "animate-pulse"}`}
    >
      <span
        className={`inline-flex h-2.5 w-2.5 rounded-full ring-2 ${
          ready ? "bg-emerald-400 ring-emerald-400/25" : "bg-rose-500 ring-rose-500/25"
        }`}
      />
      <span className="sr-only">{ready ? "running" : "not ready"}</span>
    </span>
  );
}

function ToolTile({ tool, health }: { tool: Tool; health: Health | null }) {
  const live =
    tool.workload && health ? (health[tool.workload]?.ready ? "ready" : "down") : null;
  return (
    <a
      href={tool.href}
      target="_blank"
      rel="noreferrer noopener"
      title={`${tool.name} — ${tool.role}`}
      className="group relative flex flex-col items-center gap-2 rounded-xl border border-slate-800 bg-slate-950/60 p-4 text-center transition-all hover:-translate-y-0.5 hover:border-slate-600 hover:bg-slate-900"
      style={{ boxShadow: `inset 0 1px 0 0 ${tool.brand}14` }}
    >
      <StatusDot state={live as "ready" | "down" | null} />
      <Mark tool={tool} />
      <span className="text-sm leading-tight font-semibold text-slate-100">{tool.name}</span>
      <span className="text-xs leading-snug text-slate-400 group-hover:text-slate-300">
        {tool.role}
      </span>
    </a>
  );
}

export function StackLandscape() {
  const [showLibraries, setShowLibraries] = useState(false);
  const [health, setHealth] = useState<Health | null>(null);
  const [kubeReason, setKubeReason] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    const load = async () => {
      try {
        const res = await fetch("/api/stack", { cache: "no-store" });
        const body = (await res.json()) as {
          available: boolean;
          reason?: string;
          workloads: Health;
        };
        if (!live) return;
        setHealth(body.available ? body.workloads : null);
        setKubeReason(body.available ? null : (body.reason ?? "Kubernetes is not reachable"));
      } catch {
        if (live) setHealth(null);
      }
    };
    void load();
    const timer = setInterval(load, POLL_MS);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, []);

  const shown = TOOLS.filter((t) => showLibraries || !t.library).length;
  // Denominator is what we watch, not what we found: a workload whose pod is
  // gone (k6 scaled to zero) should read as 8/9, not silently become 8/8.
  const running = health ? Object.values(health).filter((w) => w.ready).length : 0;
  const watched = WATCHED_WORKLOADS.length;

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-slate-800 bg-slate-900/60 px-5 py-3">
        <p className="text-sm text-slate-300">
          <span className="font-semibold text-slate-100">{shown} tools</span>
          {health ? (
            <>
              {" · "}
              <span className="font-semibold text-emerald-300">
                {running}/{watched} running
              </span>{" "}
              in the cluster right now
            </>
          ) : (
            <>
              {" · "}
              <span className="text-slate-400">
                {kubeReason ? "live status unavailable" : "checking the cluster…"}
              </span>
            </>
          )}
        </p>
        <label className="flex cursor-pointer items-center gap-3 text-sm font-semibold text-slate-200">
          Show libraries
          <button
            type="button"
            role="switch"
            aria-checked={showLibraries}
            aria-label="Show libraries"
            onClick={() => setShowLibraries((v) => !v)}
            className={`relative h-6 w-11 rounded-full transition-colors ${
              showLibraries ? "bg-sky-500" : "bg-slate-700"
            }`}
          >
            <span
              className={`absolute top-0.5 h-5 w-5 rounded-full bg-white transition-all ${
                showLibraries ? "left-[1.375rem]" : "left-0.5"
              }`}
            />
          </button>
        </label>
      </div>

      {LAYERS.map((layer) => {
        const tools = toolsForLayer(layer.id, showLibraries);
        if (tools.length === 0) return null;
        return (
          <section
            key={layer.id}
            className="rounded-xl border border-slate-800 bg-slate-900/40 p-4"
          >
            <header className="mb-3 flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-slate-800 pb-2">
              <h2 className="text-base font-bold tracking-widest text-sky-300 uppercase">
                {layer.name}
              </h2>
              <p className="text-sm text-slate-400">{layer.blurb}</p>
              <span className="ml-auto text-sm text-slate-500">{tools.length}</span>
            </header>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6">
              {tools.map((tool) => (
                <ToolTile key={tool.id} tool={tool} health={health} />
              ))}
            </div>
          </section>
        );
      })}
    </div>
  );
}
