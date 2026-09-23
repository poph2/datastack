import { prometheusUrl } from "@/lib/config";

export type PromSample = {
  metric: Record<string, string>;
  value: [number, string];
};

/** Runs an instant PromQL query server-side and returns the raw result vector. */
export async function promQuery(expr: string): Promise<PromSample[]> {
  const url = `${prometheusUrl()}/api/v1/query?query=${encodeURIComponent(expr)}`;
  const res = await fetch(url, { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`prometheus query failed: ${res.status}`);
  }
  const json = await res.json();
  if (json.status !== "success") {
    throw new Error(`prometheus query error: ${JSON.stringify(json)}`);
  }
  return json.data.result as PromSample[];
}

export function promValue(sample: PromSample): number {
  return Number.parseFloat(sample.value[1]);
}
