// Small presentational building blocks shared by the pages.

type ButtonVariant = "primary" | "secondary" | "danger" | "ghost";

const BUTTON_STYLES: Record<ButtonVariant, string> = {
  primary: "bg-sky-500 text-slate-950 hover:bg-sky-400",
  secondary: "bg-slate-800 text-slate-100 ring-1 ring-slate-700 hover:bg-slate-700",
  danger: "bg-rose-500 text-white hover:bg-rose-400",
  ghost: "text-slate-300 hover:bg-slate-800 hover:text-white",
};

export function Button({
  variant = "secondary",
  className = "",
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant }) {
  return (
    <button
      type="button"
      {...props}
      className={`inline-flex items-center justify-center gap-2 rounded-lg px-4 py-2 text-sm font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${BUTTON_STYLES[variant]} ${className}`}
    />
  );
}

export function Card({
  className = "",
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <section
      className={`rounded-xl border border-slate-800 bg-slate-900/60 p-5 shadow-sm ${className}`}
    >
      {children}
    </section>
  );
}

type Tone = "neutral" | "good" | "warn" | "bad" | "info";

const BADGE_STYLES: Record<Tone, string> = {
  neutral: "bg-slate-800 text-slate-200 ring-slate-600",
  good: "bg-emerald-500/15 text-emerald-300 ring-emerald-500/40",
  warn: "bg-amber-500/15 text-amber-300 ring-amber-500/40",
  bad: "bg-rose-500/15 text-rose-300 ring-rose-500/40",
  info: "bg-sky-500/15 text-sky-300 ring-sky-500/40",
};

export function Badge({ tone = "neutral", children }: { tone?: Tone; children: React.ReactNode }) {
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-sm font-semibold ring-1 ${BADGE_STYLES[tone]}`}
    >
      {children}
    </span>
  );
}

export function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: string;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
}) {
  return (
    <header className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-3xl font-bold tracking-tight text-white">{title}</h1>
        {subtitle && <p className="mt-1 text-base text-slate-400">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </header>
  );
}

export function ErrorBox({ title, children }: { title: string; children?: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-rose-500/40 bg-rose-500/10 p-4 text-rose-100">
      <p className="font-semibold text-rose-300">{title}</p>
      {children && <div className="mt-1 text-sm">{children}</div>}
    </div>
  );
}

export function Spinner({ className = "h-4 w-4" }: { className?: string }) {
  return (
    <span
      aria-hidden
      className={`inline-block animate-spin rounded-full border-2 border-current border-t-transparent ${className}`}
    />
  );
}
