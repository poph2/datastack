const DEFAULT_BACKEND_URL = "http://localhost:8080";

/** Base URL for the Go backend API, reachable from the browser. */
export function backendUrl(): string {
  return process.env.NEXT_PUBLIC_BACKEND_URL ?? DEFAULT_BACKEND_URL;
}

const DEFAULT_PROMETHEUS_URL = "http://localhost:9090";

/** Base URL for Prometheus, only ever called server-side (API routes). */
export function prometheusUrl(): string {
  return process.env.PROMETHEUS_URL ?? DEFAULT_PROMETHEUS_URL;
}
