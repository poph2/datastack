import type { AskResponse } from "@/lib/types";

const NUMERIC_TYPE = /int|decimal|numeric|double|float|real|largeint/i;
const NUMBER_STRING = /^-?\d+(\.\d+)?$/;

/**
 * Whether a result column holds numbers: by its StarRocks type when the
 * analyzer reports one, otherwise by its values (DECIMAL arrives as strings).
 */
export function isNumericColumn(result: AskResponse, index: number): boolean {
  const type = result.columns[index]?.type ?? "";
  if (NUMERIC_TYPE.test(type)) return true;
  if (type && !/unknown/i.test(type)) return false;
  const values = result.rows.map((r) => r[index]).filter((v) => v !== null);
  return (
    values.length > 0 &&
    values.every((v) => typeof v === "number" || (typeof v === "string" && NUMBER_STRING.test(v)))
  );
}

export function toNumber(value: unknown): number | null {
  if (typeof value === "number") return value;
  if (typeof value === "string" && NUMBER_STRING.test(value)) return Number(value);
  return null;
}

/**
 * For a two-column result with one category column and one numeric column,
 * the indexes of each; null when the result isn't shaped for a bar chart.
 */
export function barChartShape(
  result: AskResponse,
): { category: number; value: number } | null {
  if (result.columns.length !== 2 || result.rows.length < 1) return null;
  const numeric = [isNumericColumn(result, 0), isNumericColumn(result, 1)];
  if (numeric[0] === numeric[1]) return null;
  return numeric[1] ? { category: 0, value: 1 } : { category: 1, value: 0 };
}
