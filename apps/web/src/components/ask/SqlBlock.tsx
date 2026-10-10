"use client";

import { useState } from "react";

// A small SQL highlighter: enough for keywords, strings, numbers, comments
// and functions, without pulling in a highlighting library.

const KEYWORDS = new Set(
  `select from where group by order having limit offset join left right inner outer full cross on as and or not in is null like between case when then else end distinct union all with asc desc over partition interval exists true false cast if coalesce`
    .split(/\s+/),
);

const TOKEN =
  /(--[^\n]*)|('(?:[^'\\]|\\.|'')*')|(`[^`]*`)|(\b\d+(?:\.\d+)?\b)|([A-Za-z_][A-Za-z0-9_]*)(?=\s*\()|([A-Za-z_][A-Za-z0-9_]*)/g;

function highlight(sql: string): React.ReactNode[] {
  const out: React.ReactNode[] = [];
  let last = 0;
  for (const m of sql.matchAll(TOKEN)) {
    const i = m.index ?? 0;
    if (i > last) out.push(sql.slice(last, i));
    const [text, comment, str, quoted, num, fn, word] = m;
    let cls = "";
    if (comment) cls = "text-slate-500 italic";
    else if (str) cls = "text-emerald-300";
    else if (quoted) cls = "text-orange-200";
    else if (num) cls = "text-amber-300";
    else if (fn) cls = KEYWORDS.has(fn.toLowerCase()) ? "text-sky-300 font-semibold" : "text-violet-300";
    else if (word && KEYWORDS.has(word.toLowerCase())) cls = "text-sky-300 font-semibold";
    out.push(
      cls ? (
        <span key={i} className={cls}>
          {text}
        </span>
      ) : (
        text
      ),
    );
    last = i + text.length;
  }
  if (last < sql.length) out.push(sql.slice(last));
  return out;
}

export function SqlBlock({ sql, tone = "normal" }: { sql: string; tone?: "normal" | "error" }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(sql);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard needs a secure context; localhost qualifies, anything else may not.
    }
  }

  return (
    <div
      className={`relative rounded-lg border bg-slate-950 ${
        tone === "error" ? "border-rose-500/40" : "border-slate-800"
      }`}
    >
      <button
        type="button"
        onClick={copy}
        className="absolute top-2 right-2 rounded-md bg-slate-800 px-2.5 py-1 text-sm font-semibold text-slate-200 ring-1 ring-slate-700 hover:bg-slate-700"
      >
        {copied ? "Copied" : "Copy"}
      </button>
      <pre className="overflow-x-auto p-4 pr-20 font-mono text-[15px] leading-relaxed whitespace-pre-wrap text-slate-100">
        <code>{highlight(sql)}</code>
      </pre>
    </div>
  );
}
