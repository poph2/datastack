"use client";

import { DASHBOARDS } from "@/lib/dashboards";
import { dashboardUrl, exploreUrl } from "@/lib/grafana";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useConfig } from "./ConfigContext";

const PAGES = [
  { href: "/", label: "Ask the data", hint: "Text to SQL on StarRocks" },
  { href: "/chaos", label: "Chaos", hint: "Break the services" },
  { href: "/load", label: "Load", hint: "k6 checkout journeys" },
  { href: "/stack", label: "The stack", hint: "Every tool, live" },
];

export function Sidebar() {
  const pathname = usePathname();
  const { grafanaUrl } = useConfig();

  return (
    <aside className="border-b border-slate-800 bg-slate-950/70 lg:sticky lg:top-0 lg:h-screen lg:w-72 lg:shrink-0 lg:overflow-y-auto lg:border-r lg:border-b-0">
      <div className="flex flex-col gap-6 px-5 py-5">
        <Link href="/" className="flex items-center gap-3">
          <span className="grid h-10 w-10 place-items-center rounded-lg bg-sky-500 text-lg font-black text-slate-950">
            DS
          </span>
          <span>
            <span className="block text-lg font-bold leading-tight text-white">DataStack</span>
            <span className="block text-sm text-slate-400">Demo control center</span>
          </span>
        </Link>

        <nav className="flex flex-wrap gap-2 lg:flex-col">
          {PAGES.map((page) => {
            const active =
              page.href === "/" ? pathname === "/" : pathname.startsWith(page.href);
            return (
              <Link
                key={page.href}
                href={page.href}
                className={`rounded-lg px-3 py-2 transition-colors ${
                  active
                    ? "bg-sky-500/15 text-sky-200 ring-1 ring-sky-500/50"
                    : "text-slate-300 hover:bg-slate-800/70 hover:text-white"
                }`}
              >
                <span className="block text-base font-semibold">{page.label}</span>
                <span className="hidden text-sm text-slate-400 lg:block">{page.hint}</span>
              </Link>
            );
          })}
        </nav>

        <div>
          <h2 className="mb-2 px-3 text-sm font-semibold uppercase tracking-wider text-slate-400">
            Grafana
          </h2>
          <ul className="flex flex-wrap gap-1 lg:flex-col">
            {DASHBOARDS.map((d) => (
              <li key={d.uid}>
                <a
                  href={dashboardUrl(grafanaUrl, d.uid)}
                  target="_blank"
                  rel="noreferrer"
                  title={d.blurb}
                  className="flex items-center justify-between gap-2 rounded-md px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800/70 hover:text-white"
                >
                  {d.title}
                  <span aria-hidden className="text-slate-500">↗</span>
                </a>
              </li>
            ))}
            <li>
              <a
                href={exploreUrl(grafanaUrl)}
                target="_blank"
                rel="noreferrer"
                className="flex items-center justify-between gap-2 rounded-md px-3 py-1.5 text-sm text-slate-300 hover:bg-slate-800/70 hover:text-white"
              >
                Explore traces (Tempo)
                <span aria-hidden className="text-slate-500">↗</span>
              </a>
            </li>
          </ul>
          <p className="mt-3 px-3 text-sm text-slate-400">
            Grafana at {grafanaUrl} (<code>make grafana</code>)
          </p>
        </div>
      </div>
    </aside>
  );
}
