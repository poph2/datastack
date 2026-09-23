const DEFAULT_BACKEND_URL = "http://localhost:8080";

/** Base URL for the Go backend API, reachable from the browser. */
export function backendUrl(): string {
  return process.env.NEXT_PUBLIC_BACKEND_URL ?? DEFAULT_BACKEND_URL;
}

const DEFAULT_GRAFANA_URL = "http://localhost:3001";

/** Base URL for Grafana, reachable from the browser (used for dashboard embeds). */
export function grafanaUrl(): string {
  return process.env.NEXT_PUBLIC_GRAFANA_URL ?? DEFAULT_GRAFANA_URL;
}
