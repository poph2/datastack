"use client";

import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

const COLORS = [
  "#2563eb",
  "#16a34a",
  "#d97706",
  "#dc2626",
  "#7c3aed",
  "#0891b2",
];

export type ChartPoint = { time: string } & Record<string, number | string>;

export function DatabaseLineChart({
  title,
  data,
  seriesKeys,
  valueFormatter,
}: {
  title: string;
  data: ChartPoint[];
  seriesKeys: string[];
  valueFormatter?: (value: number) => string;
}) {
  return (
    <div className="rounded-lg border border-zinc-200 bg-white p-5 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
      <h3 className="mb-4 text-sm font-medium text-zinc-700 dark:text-zinc-300">
        {title}
      </h3>
      <div className="h-64 w-full">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart
            data={data}
            margin={{ top: 5, right: 10, left: 0, bottom: 0 }}
          >
            <CartesianGrid strokeDasharray="3 3" stroke="#e4e4e7" />
            <XAxis dataKey="time" tick={{ fontSize: 11 }} minTickGap={30} />
            <YAxis
              tick={{ fontSize: 11 }}
              tickFormatter={valueFormatter}
              width={56}
            />
            <Tooltip
              formatter={(value: number | string) =>
                typeof value === "number"
                  ? (valueFormatter?.(value) ?? value)
                  : value
              }
            />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            {seriesKeys.map((key, i) => (
              <Line
                key={key}
                type="monotone"
                dataKey={key}
                stroke={COLORS[i % COLORS.length]}
                dot={false}
                strokeWidth={2}
                isAnimationActive={false}
              />
            ))}
          </LineChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}
