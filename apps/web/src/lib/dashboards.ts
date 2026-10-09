// The provisioned Grafana dashboards, from the "uid" and "title" fields of
// deploy/observability/dashboards/*.json. Kept in code because the Docker
// build context is apps/web; update this list when a dashboard is added.

export type Dashboard = { uid: string; title: string; blurb: string };

export const DASHBOARDS: Dashboard[] = [
  {
    uid: "datastack-executive",
    title: "Executive heartbeat",
    blurb: "Orders, revenue and checkout health",
  },
  {
    uid: "datastack-services",
    title: "Services (RED)",
    blurb: "Request rate, errors and latency per service",
  },
  {
    uid: "datastack-tracing",
    title: "Tracing & service map",
    blurb: "Service graph, span metrics, failed checkouts",
  },
  {
    uid: "datastack-inventory",
    title: "Inventory & lost revenue",
    blurb: "Stock depletion and out-of-stock losses",
  },
  {
    uid: "datastack-fulfillment",
    title: "Fulfillment SLA",
    blurb: "Time to ship, carriers, VIP SLA",
  },
  {
    uid: "datastack-marketing",
    title: "Marketing & attribution",
    blurb: "Campaign performance and ROAS",
  },
  {
    uid: "datastack-pipeline",
    title: "CDC pipeline",
    blurb: "Postgres to StarRocks sync health",
  },
];
