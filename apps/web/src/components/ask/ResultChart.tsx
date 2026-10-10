"use client";

import type { AskResponse } from "@/lib/types";
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { barChartShape, toNumber } from "./columns";

const MAX_BARS = 30;
const BAR = "#38bdf8"; // sky-400: one hue, single series
const INK = "#cbd5e1"; // slate-300
const MUTED = "#64748b"; // slate-500
const GRID = "#1e293b"; // slate-800

const fmt = (n: number) => n.toLocaleString("en-US", { maximumFractionDigits: 2 });

/** A bar chart for results with one category column and one numeric column. */
export function ResultChart({ result }: { result: AskResponse }) {
  const shape = barChartShape(result);
  if (!shape) return null;

  const data = result.rows.slice(0, MAX_BARS).flatMap((row) => {
    const value = toNumber(row[shape.value]);
    return value === null ? [] : [{ label: String(row[shape.category] ?? "NULL"), value }];
  });
  if (data.length === 0) return null;

  const valueName = result.columns[shape.value].name;
  const categoryName = result.columns[shape.category].name;
  const longLabels = data.some((d) => d.label.length > 10) || data.length > 8;

  return (
    <figure className="rounded-lg border border-slate-800 bg-slate-950 p-4">
      <figcaption className="mb-2 text-base text-slate-300">
        <span className="font-semibold text-slate-200">{valueName}</span> by {categoryName}
        {result.rows.length > MAX_BARS && ` (first ${MAX_BARS} rows)`}
      </figcaption>
      <div className="h-80">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} margin={{ top: 8, right: 8, bottom: longLabels ? 48 : 8, left: 8 }}>
            <CartesianGrid vertical={false} stroke={GRID} />
            <XAxis
              dataKey="label"
              tick={{ fill: INK, fontSize: 15 }}
              tickLine={false}
              axisLine={{ stroke: MUTED }}
              interval={0}
              angle={longLabels ? -35 : 0}
              textAnchor={longLabels ? "end" : "middle"}
            />
            <YAxis
              tick={{ fill: INK, fontSize: 15 }}
              tickLine={false}
              axisLine={false}
              tickFormatter={fmt}
              width={72}
            />
            <Tooltip
              cursor={{ fill: "rgba(148,163,184,0.12)" }}
              contentStyle={{
                background: "#0f172a",
                border: "1px solid #334155",
                borderRadius: 8,
                color: "#e2e8f0",
              }}
              labelStyle={{ color: "#f8fafc", fontWeight: 600 }}
              itemStyle={{ color: "#e2e8f0" }}
              formatter={(v) => [fmt(Number(v)), valueName]}
            />
            <Bar dataKey="value" fill={BAR} radius={[4, 4, 0, 0]} maxBarSize={56} />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </figure>
  );
}
