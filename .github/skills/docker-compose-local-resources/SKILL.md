---
name: docker-compose-local-resources
description: "Use when writing, editing, reviewing, or validating Docker Compose for DataStack, docker-compose.yml, compose.yaml, services, containers, local Docker, memory limits, CPU limits, resource budgets, Prometheus, Grafana, Postgres, StarRocks, Ollama, Go services, or Next.js services. Always ensure every Compose service has appropriate local-machine CPU and memory allowances."
argument-hint: "Docker Compose file or service change to resource-budget"
---

# Docker Compose Local Resources

Use this skill whenever creating, changing, or reviewing Docker Compose configuration for DataStack.

DataStack runs locally during development and demos. Docker Compose must therefore protect the developer machine by giving every running service explicit CPU and memory allowances.

## Core Rule

Every service in `docker-compose.yml` or any Compose override file must define local resource limits.

For local Docker Compose, prefer fields that are applied by regular `docker compose up`:

```yaml
mem_limit: 512m
memswap_limit: 512m
cpus: 0.50
```

Do not rely only on `deploy.resources` for local development. `deploy.resources` is useful documentation and may matter in Swarm-like contexts, but DataStack's first runtime target is a local machine using Docker Compose.

## Procedure

1. Identify every service that can run under Compose.
2. Classify each service by role: database, analytics database, LLM runtime, BI tool, metrics, exporter, broker, Go service, Next.js UI, or utility job.
3. Assign `mem_limit`, `memswap_limit`, and `cpus` to every service.
4. Keep limits realistic for a laptop demo. Do not over-allocate the whole machine by default.
5. Use profiles for heavy or optional services when the full stack may exceed a normal laptop budget.
6. After editing Compose, run `docker compose config`.
7. If service images or referenced config files changed, run the narrowest practical startup check, such as `docker compose up -d <service>` followed by `docker compose ps` or logs for that service.
8. If a service cannot be given a confident limit yet, choose a conservative starting budget and leave a clear TODO in the Compose-adjacent documentation, not in the YAML unless necessary.

## Default Budget Guidance

Use these as starting points, then adjust when real measurements prove they are too small or too large.

| Service type             |           Memory |              CPU |
| ------------------------ | ---------------: | ---------------: |
| Small Go service         | `128m` to `256m` | `0.25` to `0.50` |
| Go ingestion worker      | `256m` to `512m` | `0.50` to `1.00` |
| Next.js dev server       |   `512m` to `1g` | `0.50` to `1.00` |
| Postgres source database |   `512m` to `1g` | `0.50` to `1.00` |
| Postgres exporter        |  `64m` to `128m` | `0.10` to `0.25` |
| StarRocks                |     `2g` to `4g` | `1.50` to `3.00` |
| Ollama                   |     `4g` to `8g` | `2.00` to `4.00` |
| Prometheus               |   `512m` to `1g` | `0.50` to `1.00` |
| Grafana                  |   `512m` to `1g` | `0.50` to `1.00` |

## DataStack Recommended First Pass

For the first full local demo stack, use a conservative laptop-oriented budget:

| Service                 | `mem_limit` | `cpus` |
| ----------------------- | ----------: | -----: |
| `orders-postgres`       |      `768m` | `0.75` |
| `crm-postgres`          |      `768m` | `0.75` |
| `inventory-postgres`    |      `768m` | `0.75` |
| `starrocks`             |        `3g` | `2.00` |
| `ollama`                |        `6g` | `3.00` |
| each Postgres exporter  |      `128m` | `0.20` |
| `prometheus`            |        `1g` | `1.00` |
| `grafana`               |      `768m` | `0.75` |
| `scenario-orchestrator` |      `256m` | `0.50` |
| each simulator service  |      `256m` | `0.50` |
| `ingestor`              |      `512m` | `1.00` |
| `backend`               |      `512m` | `1.00` |
| `web`                   |        `1g` | `1.00` |

Set `memswap_limit` equal to `mem_limit` unless there is a specific reason to permit swap. This keeps runaway services from making the demo machine unusable.

## Compose Pattern

Use this shape for each service:

```yaml
services:
  example-service:
    image: example:latest
    mem_limit: 256m
    memswap_limit: 256m
    cpus: 0.50
```

If resource values repeat heavily, YAML anchors are allowed, but keep the Compose file easy to read during the demo.

## Review Checklist

Before finishing a Docker Compose task, verify:

- Every service has `mem_limit`.
- Every service has `memswap_limit`.
- Every service has `cpus`.
- Heavy services such as Ollama, ClickHouse, Prometheus, Grafana, and Next.js have realistic local-machine budgets.
- Optional heavy services are candidates for Compose profiles if the total budget is too high.
- `docker compose config` succeeds.
- The final answer reports the resource-budgeting choice and any known tradeoffs.

## When To Ask The User

Ask a clarifying question before assigning final budgets if:

- The target machine memory is unknown and the stack is expected to run all services at once.
- The user wants production-like load testing rather than a local demo.
- Ollama model size changes materially.
- The user wants to run multiple analytical databases at the same time.

If the user does not answer, use the DataStack recommended first pass and mention that the budgets can be tuned after observing Prometheus/Grafana metrics.
