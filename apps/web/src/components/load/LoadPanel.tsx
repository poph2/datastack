"use client";

import { api, ApiRequestError } from "@/lib/api";
import { RATE_MAX, RATE_MIN, type LoadState } from "@/lib/types";
import { useEffect, useState } from "react";
import { Badge, Button, Card, ErrorBox, Spinner } from "../ui";

const POLL_MS = 3_000;
const PRESETS = [1, 5, 10, 25, 50, 100];

type View =
  | { status: "loading" }
  | { status: "ready"; state: LoadState }
  | { status: "error"; message: string };

async function fetchLoad(): Promise<View> {
  try {
    return { status: "ready", state: await api<LoadState>("/api/load") };
  } catch (err) {
    return { status: "error", message: (err as Error).message };
  }
}

export function LoadPanel() {
  const [view, setView] = useState<View>({ status: "loading" });
  const [rate, setRate] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ tone: "good" | "bad"; text: string } | null>(null);

  useEffect(() => {
    let cancelled = false;
    const tick = () =>
      fetchLoad().then((next) => {
        if (!cancelled) setView(next);
      });
    tick();
    const id = setInterval(tick, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  async function change(body: { rate?: number; running?: boolean }, done: string) {
    setBusy(true);
    setNotice(null);
    try {
      const state = await api<LoadState>("/api/load", { method: "PUT", body: JSON.stringify(body) });
      setView({ status: "ready", state });
      if (body.rate !== undefined) setRate(null);
      setNotice({ tone: "good", text: done });
    } catch (err) {
      setNotice({
        tone: "bad",
        text: err instanceof ApiRequestError ? err.message : String(err),
      });
    } finally {
      setBusy(false);
    }
  }

  if (view.status === "loading") {
    return (
      <Card>
        <p className="flex items-center gap-2 text-slate-400">
          <Spinner /> Reading the k6 Deployment…
        </p>
      </Card>
    );
  }
  if (view.status === "error") {
    return <ErrorBox title="Couldn't read the k6 Deployment">{view.message}</ErrorBox>;
  }

  const { state } = view;
  if (!state.available) {
    return (
      <Card>
        <h2 className="mb-2 text-xl font-semibold text-white">Kubernetes isn&apos;t reachable from here</h2>
        <p className="text-slate-300">{state.reason}</p>
        <p className="mt-3 text-sm text-slate-400">
          In the cluster this page uses the <code>web</code> ServiceAccount. Locally, run{" "}
          <code className="rounded bg-slate-800 px-1.5 py-0.5">kubectl --context kind-datastack proxy</code>{" "}
          and start the app with <code className="rounded bg-slate-800 px-1.5 py-0.5">K8S_API_URL=http://127.0.0.1:8001</code>.
        </p>
      </Card>
    );
  }
  if (!state.exists) {
    return (
      <Card>
        <h2 className="mb-2 text-xl font-semibold text-white">No k6 Deployment yet</h2>
        <p className="text-slate-300">
          Run <code className="rounded bg-slate-800 px-1.5 py-0.5">make load</code> once to create
          the <code>{state.deployment}</code> Deployment and its script ConfigMap. After that, this
          page can start, stop and re-rate it.
        </p>
      </Card>
    );
  }

  const running = state.replicas > 0;
  const shownRate = rate ?? state.rate ?? 5;
  const rateDirty = rate !== null && rate !== state.rate;

  return (
    <div className="flex flex-col gap-5">
      <div className="grid gap-5 md:grid-cols-3">
        <Card>
          <p className="text-sm font-semibold tracking-wide text-slate-400 uppercase">Status</p>
          <p className={`mt-2 text-4xl font-bold ${running ? "text-emerald-300" : "text-slate-300"}`}>
            {running ? "Running" : "Stopped"}
          </p>
          <p className="mt-2 text-sm text-slate-400">
            {state.readyReplicas}/{state.replicas} pod{state.replicas === 1 ? "" : "s"} ready
            {running && state.readyReplicas < state.replicas && " · starting…"}
          </p>
        </Card>
        <Card>
          <p className="text-sm font-semibold tracking-wide text-slate-400 uppercase">Rate</p>
          <p className="mt-2 text-4xl font-bold text-white tabular-nums">
            {state.rate ?? "?"}
            <span className="ml-2 text-lg font-medium text-slate-400">journeys / s</span>
          </p>
          <p className="mt-2 text-sm text-slate-400">RATE env on the k6 container</p>
        </Card>
        <Card className="flex flex-col justify-between gap-3">
          <p className="text-sm font-semibold tracking-wide text-slate-400 uppercase">Control</p>
          {running ? (
            <Button
              variant="danger"
              className="py-3 text-lg"
              disabled={busy}
              onClick={() => void change({ running: false }, "Load stopped (replicas 0)")}
            >
              ■ Stop load
            </Button>
          ) : (
            <Button
              variant="primary"
              className="py-3 text-lg"
              disabled={busy}
              onClick={() => void change({ running: true }, "Load started (replicas 1)")}
            >
              ▶ Start load
            </Button>
          )}
          <p className="text-sm text-slate-400">
            Scales <code>deploy/{state.deployment}</code> between 0 and 1 replicas.
          </p>
        </Card>
      </div>

      <Card className="flex flex-col gap-4">
        <div className="flex items-baseline justify-between">
          <h2 className="text-xl font-semibold text-white">Set the rate</h2>
          <span className="font-mono text-2xl text-sky-300 tabular-nums">{shownRate}/s</span>
        </div>
        <input
          type="range"
          min={RATE_MIN}
          max={RATE_MAX}
          step={1}
          value={shownRate}
          onChange={(e) => setRate(Number(e.target.value))}
          aria-label="journeys per second"
        />
        <div className="flex flex-wrap items-center gap-2">
          {PRESETS.map((p) => (
            <button
              key={p}
              type="button"
              onClick={() => setRate(p)}
              className={`rounded-full px-3 py-1 text-sm font-semibold ring-1 ${
                shownRate === p
                  ? "bg-sky-500/20 text-sky-200 ring-sky-500/60"
                  : "bg-slate-800 text-slate-300 ring-slate-700 hover:bg-slate-700"
              }`}
            >
              {p}/s
            </button>
          ))}
          <input
            type="number"
            min={RATE_MIN}
            max={RATE_MAX}
            value={shownRate}
            onChange={(e) => {
              const v = Math.round(Number(e.target.value));
              if (Number.isFinite(v)) setRate(Math.min(RATE_MAX, Math.max(RATE_MIN, v)));
            }}
            className="w-20 rounded-md border border-slate-700 bg-slate-950 px-2 py-1 text-right font-mono text-slate-100"
            aria-label="rate"
          />
          <Button
            variant="primary"
            className="ml-auto"
            disabled={busy || !rateDirty}
            onClick={() => void change({ rate: shownRate }, `Rate set to ${shownRate}/s; k6 is restarting with it`)}
          >
            Apply rate
          </Button>
        </div>
        <p className="text-sm text-slate-400">
          Changing the rate rolls the k6 pod, so there is a gap of a few seconds in traffic.
          {!running && " Load is stopped: the new rate is used the next time you start it."}
        </p>
      </Card>

      <div className="flex min-h-6 items-center gap-3">
        {busy && <Spinner />}
        {notice && <Badge tone={notice.tone}>{notice.text}</Badge>}
        {state.image && <span className="ml-auto text-sm text-slate-400">image {state.image}</span>}
      </div>
    </div>
  );
}
