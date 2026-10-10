"use client";

import { api, ApiRequestError } from "@/lib/api";
import { traceUrl } from "@/lib/grafana";
import type { ApiError, AskResponse, ModelsResponse } from "@/lib/types";
import { useEffect, useRef, useState } from "react";
import { useConfig } from "../ConfigContext";
import { Badge, Button, Card, ErrorBox, Spinner } from "../ui";
import { ResultChart } from "./ResultChart";
import { ResultTable } from "./ResultTable";
import { SchemaBrowser } from "./SchemaBrowser";
import { SqlBlock } from "./SqlBlock";

const EXAMPLES = [
  "What is total revenue by product category?",
  "Which utm_source has the best return on ad spend?",
  "How many checkouts failed in the last hour, by failure reason?",
  "What is the payment decline rate by payment method?",
  "What is the average time to ship in minutes, by carrier?",
  "Top 10 products by revenue",
  "How much revenue did we lose to out-of-stock failures, by category?",
  "Revenue by customer loyalty tier",
];

const ERROR_TITLES: Record<string, string> = {
  UNSAFE_SQL: "The model wrote SQL that isn't a read-only query",
  QUERY_FAILED: "StarRocks rejected the generated SQL",
  NO_SQL: "The model didn't return any SQL",
  LLM_UNAVAILABLE: "The language model is unavailable",
  INVALID_REQUEST: "Invalid question",
  UPSTREAM_UNAVAILABLE: "The analyzer service is unreachable",
  UPSTREAM_TIMEOUT: "The analyzer took too long",
};

type ModelsState =
  | { status: "loading" }
  | { status: "ready"; data: ModelsResponse }
  | { status: "error"; message: string };

export function AskPanel() {
  const { grafanaUrl } = useConfig();
  const [question, setQuestion] = useState("");
  const [models, setModels] = useState<ModelsState>({ status: "loading" });
  const [model, setModel] = useState("");
  const [pending, setPending] = useState<{ question: string; startedAt: number } | null>(null);
  const [elapsed, setElapsed] = useState(0);
  const [result, setResult] = useState<AskResponse | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    let cancelled = false;
    api<ModelsResponse>("/api/models")
      .then((data) => {
        if (cancelled) return;
        setModels({ status: "ready", data });
        setModel((m) => m || data.default);
      })
      .catch((err: unknown) => {
        if (!cancelled) setModels({ status: "error", message: String((err as Error).message) });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!pending) return;
    const id = setInterval(() => setElapsed(Date.now() - pending.startedAt), 250);
    return () => clearInterval(id);
  }, [pending]);

  async function ask(text: string) {
    const q = text.trim();
    if (!q || pending) return;
    setQuestion(q);
    setError(null);
    setElapsed(0);
    setPending({ question: q, startedAt: Date.now() });
    const controller = new AbortController();
    abortRef.current = controller;
    try {
      const data = await api<AskResponse>("/api/ask", {
        method: "POST",
        body: JSON.stringify({ question: q, model: model || undefined }),
        signal: controller.signal,
      });
      setResult(data);
    } catch (err) {
      if (controller.signal.aborted) return;
      setResult(null);
      setError(
        err instanceof ApiRequestError ? err.body : { error: "ERROR", message: String(err) },
      );
    } finally {
      setPending(null);
      abortRef.current = null;
    }
  }

  function cancel() {
    abortRef.current?.abort();
  }

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void ask(question);
          }}
          className="flex flex-col gap-4"
        >
          <textarea
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                void ask(question);
              }
            }}
            rows={2}
            placeholder="Ask a question about orders, revenue, campaigns, stock or shipping…"
            className="w-full resize-y rounded-lg border border-slate-700 bg-slate-950 px-4 py-3 text-xl text-white placeholder:text-slate-500 focus:border-sky-500 focus:outline-none"
            disabled={!!pending}
          />
          <div className="flex flex-wrap items-center gap-3">
            <label className="flex items-center gap-2 text-sm text-slate-400">
              Model
              {models.status === "ready" ? (
                <select
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                  className="rounded-md border border-slate-700 bg-slate-950 px-3 py-2 font-mono text-sm text-slate-100"
                  disabled={!!pending}
                >
                  {models.data.models.map((m) => (
                    <option key={m} value={m}>
                      {m}
                      {m === models.data.default ? " (default)" : ""}
                    </option>
                  ))}
                </select>
              ) : models.status === "loading" ? (
                <span className="flex items-center gap-2 text-slate-500">
                  <Spinner className="h-3 w-3" /> loading…
                </span>
              ) : (
                <span title={models.message}>
                  <Badge tone="warn">analyzer unreachable</Badge>
                </span>
              )}
              {models.status === "ready" && (
                <span className="text-sm text-slate-400">via {models.data.provider}</span>
              )}
            </label>
            <div className="ml-auto flex gap-2">
              {pending && (
                <Button variant="ghost" onClick={cancel}>
                  Cancel
                </Button>
              )}
              <Button type="submit" variant="primary" disabled={!!pending || !question.trim()} className="px-6 text-base">
                {pending ? (
                  <>
                    <Spinner /> Thinking…
                  </>
                ) : (
                  "Ask"
                )}
              </Button>
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            {EXAMPLES.map((ex) => (
              <button
                key={ex}
                type="button"
                disabled={!!pending}
                onClick={() => void ask(ex)}
                className="rounded-full border border-slate-700 bg-slate-800/60 px-3 py-1.5 text-sm text-slate-200 hover:border-sky-500/60 hover:bg-slate-800 disabled:opacity-50"
              >
                {ex}
              </button>
            ))}
          </div>
        </form>
      </Card>

      {pending && (
        <Card className="flex items-center gap-4">
          <Spinner className="h-6 w-6 text-sky-400" />
          <div>
            <p className="text-lg text-slate-100">“{pending.question}”</p>
            <p className="text-sm text-slate-400">
              Writing SQL with {model || "the default model"} and running it on StarRocks ·{" "}
              <span className="font-mono tabular-nums">{(elapsed / 1000).toFixed(1)}s</span>
              {elapsed > 15_000 && " · a local model can take up to a minute or two"}
            </p>
          </div>
        </Card>
      )}

      {!pending && error && (
        <Card className="flex flex-col gap-4">
          <ErrorBox title={ERROR_TITLES[error.error] ?? error.error}>
            <p className="whitespace-pre-wrap">{error.message}</p>
            <p className="mt-1 font-mono text-sm text-rose-300">{error.error}</p>
          </ErrorBox>
          {error.sql && (
            <div>
              <h3 className="mb-2 text-sm font-semibold text-slate-300">The SQL that failed</h3>
              <SqlBlock sql={error.sql} tone="error" />
            </div>
          )}
        </Card>
      )}

      {!pending && result && (
        <Card className="flex flex-col gap-5">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <h2 className="text-xl font-semibold text-white">{result.question}</h2>
            {result.trace_id && (
              <a
                href={traceUrl(grafanaUrl, result.trace_id)}
                target="_blank"
                rel="noreferrer"
                className="rounded-lg bg-violet-500/15 px-3 py-1.5 text-sm font-semibold text-violet-200 ring-1 ring-violet-500/50 hover:bg-violet-500/25"
              >
                View trace ↗
              </a>
            )}
          </div>
          <div className="flex flex-wrap gap-2">
            <Badge tone="info">
              {result.row_count.toLocaleString()} row{result.row_count === 1 ? "" : "s"}
            </Badge>
            {result.truncated && <Badge tone="warn">truncated</Badge>}
            <Badge tone={result.attempts > 1 ? "warn" : "neutral"}>
              {result.attempts} attempt{result.attempts === 1 ? "" : "s"}
            </Badge>
            <Badge>{(result.duration_ms / 1000).toFixed(1)}s</Badge>
            <Badge>{result.model}</Badge>
          </div>
          <SqlBlock sql={result.sql} />
          <ResultChart result={result} />
          <ResultTable result={result} />
        </Card>
      )}

      <SchemaBrowser />
    </div>
  );
}
