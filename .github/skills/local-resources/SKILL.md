---
name: local-resources
description: "Use when adding or changing anything that runs on DataStack's kind cluster or in docker-compose: Helm values, deploy/values/*.yaml, the go-service chart, deploy/infra or deploy/observability manifests, StarRocks, Postgres, Prometheus, Grafana, Tempo, the OTel Collector, k6, or a new Go service. Ensures every workload declares CPU and memory requests and limits that fit a laptop."
argument-hint: "Workload, chart values, or manifest being resource-budgeted"
---

# Local Resources (DataStack)

DataStack runs on a single kind node. A small laptop (a 16 GB Intel Mac with ~8 GB for Docker) can't run the whole stack: it starves the node of CPU, probes fail and pods restart. The full stack runs on a bigger demo machine, but it still has to fit comfortably, and a single workload without limits can make the demo machine unusable mid-talk. Every workload therefore declares explicit requests and limits. Ollama runs outside the cluster and needs its own headroom, ideally a GPU.

## Core Rule

Every container on the kind cluster has `resources.requests` and `resources.limits` for both `cpu` and `memory`:

- **Go services** get them from the go-service chart's `resources` default (`deploy/charts/go-service/values.yaml`). Override in `deploy/values/<service>.yaml` only with a measured reason.
- **Raw manifests** (`deploy/infra/*.yaml`, `deploy/k6/deployment.yaml`) set them inline.
- **Third-party Helm charts** (CNPG, Collector, Tempo, Prometheus, Grafana) set them in the values file, and disable chart components we don't use (for example Prometheus's alertmanager, pushgateway, node-exporter and kube-state-metrics).
- `docker-compose.yml` (local Postgres only) uses `mem_limit`, `memswap_limit` equal to `mem_limit`, and `cpus`.

## Current Budget

| Workload | Requests | Limits | Where |
|---|---|---|---|
| Each Go service (×6 business services) | 50m / 64Mi | 500m / 256Mi | go-service chart default |
| CDC sync | 25m / 32Mi | 250m / 128Mi | `deploy/values/cdc-sync.yaml` |
| Analyzer | 25m / 32Mi | 250m / 128Mi | `deploy/values/analyzer.yaml` (waits on the LLM; result sets are capped) |
| Web UI (Next.js) | 25m / 96Mi | 500m / 256Mi | `deploy/web/deployment.yaml` |
| Postgres (CNPG, all six databases) | 100m / 256Mi | 1 CPU / 1Gi | `deploy/infra/postgres.yaml` |
| StarRocks (FE + BE, one pod) | 500m / 3Gi | 2 CPU / 8Gi | `deploy/infra/starrocks.yaml` (4Gi still OOMKilled 35 times in seven days, working set 4,038 MiB) |
| k6 | 100m / 64Mi | 1 CPU / 256Mi | `deploy/k6/deployment.yaml` |
| OTel Collector | 25m / 64Mi | 500m / 192Mi | `deploy/observability/otel-collector.yaml` |
| Tempo | 50m / 256Mi | 500m / 4Gi | `deploy/observability/tempo.yaml` (1536Mi still OOMKilled 11 times in seven days; it bursts, it does not creep) |
| Prometheus (+ config reloader 5m / 16Mi) | 50m / 256Mi | 500m / 768Mi | `deploy/observability/prometheus.yaml` |
| Grafana | 50m / 128Mi | 500m / 384Mi | `deploy/observability/grafana.yaml` |

The whole stack measured at about 5.2 GiB of working-set memory on a 7.65 GiB kind node, with k6 at 5 journeys per second. Above that rate Tempo is the component that grows: at 10-15 journeys a second it was OOMKilled five times in three hours on the 640Mi limit it used to have.

StarRocks is the dominant cost. If the machine struggles, it's the first thing to stop (`kubectl --context kind-datastack scale statefulset/starrocks --replicas=0`). The services keep running, and CDC retries until StarRocks is back.

## Procedure

1. Classify the workload: Go service, database, analytics database, observability component, or load generator.
2. Start from the table above for similar workloads. Keep requests low, since they only reserve scheduling space on a single node, and set limits to what the workload genuinely needs under k6 load.
3. Apply the change and check that nothing is OOMKilled or throttled into failing probes: `kubectl --context kind-datastack get pods`, and the RESTARTS column. A restart with reason `OOMKilled` means the memory limit is too low. A liveness-probe kill under load usually means CPU starvation (see the probe timeouts in the go-service chart).
4. Update the budget table above when a default changes.

## Review Checklist

- Every new container has CPU and memory requests and limits.
- Unused components of third-party charts are disabled.
- The total of all memory limits still fits comfortably in the Docker Desktop allocation, with StarRocks running.
- The final answer reports the budget chosen and any trade-offs.
