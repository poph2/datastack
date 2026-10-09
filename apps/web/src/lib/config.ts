// Server-side configuration. Every value comes from the environment, with
// defaults that work inside the kind cluster (in-cluster Service DNS names).
// Read at request time, so one image works everywhere.

export const SERVICES = [
  "order",
  "customer",
  "marketing",
  "inventory",
  "payment",
  "shipment",
] as const;

export type ServiceName = (typeof SERVICES)[number];

const SERVICE_DEFAULTS: Record<ServiceName, string> = {
  order: "http://order-service:8080",
  customer: "http://customer-service:8081",
  marketing: "http://marketing-service:8082",
  inventory: "http://inventory-service:8083",
  payment: "http://payment-service:8084",
  shipment: "http://shipment-service:8085",
};

export function isServiceName(name: string): name is ServiceName {
  return (SERVICES as readonly string[]).includes(name);
}

function trimSlash(url: string): string {
  return url.replace(/\/+$/, "");
}

/** Base URL of one of the six operational services, e.g. ORDER_URL. */
export function serviceUrl(name: ServiceName): string {
  const env = process.env[`${name.toUpperCase()}_URL`];
  return trimSlash(env || SERVICE_DEFAULTS[name]);
}

/** Base URL of the text-to-SQL analyzer service. */
export function analyzerUrl(): string {
  return trimSlash(process.env.ANALYZER_URL || "http://analyzer-service:8086");
}

/** Grafana as the browser sees it (a port-forward on the presenter's laptop). */
export function grafanaUrl(): string {
  return trimSlash(process.env.GRAFANA_URL || "http://localhost:3000");
}

/** Namespace of the k6 Deployment. */
export function k8sNamespace(): string {
  return process.env.K8S_NAMESPACE || "default";
}

/** Name of the k6 Deployment the load page drives. */
export function k6Deployment(): string {
  return process.env.K6_DEPLOYMENT || "k6";
}
