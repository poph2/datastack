export function StatusBadge({ ok, label }: { ok: boolean | null; label: string }) {
  const color =
    ok === null
      ? "bg-zinc-200 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400"
      : ok
        ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-400"
        : "bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-400";
  const text = ok === null ? "Checking…" : ok ? "Healthy" : "Unreachable";

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ${color}`}
    >
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {label}: {text}
    </span>
  );
}
