import type { AskResponse, Cell } from "@/lib/types";
import { isNumericColumn } from "./columns";

function render(cell: Cell): React.ReactNode {
  if (cell === null) return <span className="text-slate-500 italic">NULL</span>;
  if (typeof cell === "boolean") return cell ? "true" : "false";
  if (typeof cell === "number") return cell.toLocaleString("en-US", { maximumFractionDigits: 4 });
  return cell;
}

export function ResultTable({ result }: { result: AskResponse }) {
  const numeric = result.columns.map((_, i) => isNumericColumn(result, i));

  if (result.rows.length === 0) {
    return (
      <p className="rounded-lg border border-slate-800 bg-slate-950 p-6 text-center text-slate-400">
        The query ran and returned no rows.
      </p>
    );
  }

  return (
    <div className="max-h-[32rem] overflow-auto rounded-lg border border-slate-800">
      <table className="w-full border-collapse text-[15px]">
        <thead className="sticky top-0 bg-slate-800 text-left">
          <tr>
            {result.columns.map((col, i) => (
              <th
                key={col.name + i}
                className={`px-4 py-2.5 font-semibold whitespace-nowrap text-slate-100 ${
                  numeric[i] ? "text-right" : ""
                }`}
              >
                {col.name}
                <span className="ml-2 text-xs font-normal text-slate-400">{col.type}</span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {result.rows.map((row, r) => (
            <tr key={r} className="border-t border-slate-800 odd:bg-slate-900/40 hover:bg-slate-800/50">
              {row.map((cell, c) => (
                <td
                  key={c}
                  className={`px-4 py-2 whitespace-nowrap ${
                    numeric[c] ? "text-right font-mono tabular-nums" : ""
                  }`}
                >
                  {render(cell)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
