// Grafana deep links. The base URL is browser-facing (GRAFANA_URL), because
// the presenter opens these in a new tab.

export function dashboardUrl(base: string, uid: string): string {
  return `${base}/d/${encodeURIComponent(uid)}?orgId=1&refresh=10s`;
}

/** Explore with the Tempo datasource (uid "tempo") opened on one trace. */
export function traceUrl(base: string, traceId: string): string {
  const panes = {
    trace: {
      datasource: "tempo",
      queries: [
        {
          refId: "A",
          datasource: { type: "tempo", uid: "tempo" },
          queryType: "traceql",
          query: traceId,
        },
      ],
      range: { from: "now-1h", to: "now" },
    },
  };
  return `${base}/explore?schemaVersion=1&orgId=1&panes=${encodeURIComponent(JSON.stringify(panes))}`;
}

/** Explore on Tempo, for browsing recent traces. */
export function exploreUrl(base: string): string {
  const panes = {
    tempo: {
      datasource: "tempo",
      queries: [
        {
          refId: "A",
          datasource: { type: "tempo", uid: "tempo" },
          queryType: "traceqlSearch",
          filters: [],
        },
      ],
      range: { from: "now-15m", to: "now" },
    },
  };
  return `${base}/explore?schemaVersion=1&orgId=1&panes=${encodeURIComponent(JSON.stringify(panes))}`;
}
