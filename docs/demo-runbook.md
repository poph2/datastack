# Demo Runbook

How to bring DataStack up on the demo machine, check it is healthy, and run the talk's demo. Architecture and conventions are in [microservices/README.md](microservices/README.md).

## 1. Machine

The whole stack runs on one kind node. The resource limits add up to about 9 GB of memory, StarRocks alone is 3 GB, and the working set under load is about 5–6 GB. Plan for:

- **Docker:** at least 12 GB of memory and 6 CPUs for the Docker VM (or for the host, on Linux).
- **Ollama:** outside the cluster, on this machine or another one, ideally with a GPU. On a CPU, `qwen2.5-coder` (7B) needs minutes to read the ~4k-token schema prompt the first time and writes about 3 tokens a second. With a GPU, a question takes a few seconds.
- **Tools:** Docker, kind, kubectl, helm, Go 1.26, make.

Every make target pins `--context kind-datastack`, so it never touches another cluster.

## 2. Ollama

```sh
ollama pull qwen2.5-coder        # the default model
ollama pull qwen3.5:9b           # optional: a second model to compare in the UI
```

The pods must be able to reach Ollama:

| Where Ollama runs | `OLLAMA_URL` |
|---|---|
| Same machine, Docker Desktop (Mac or Windows) | Default `http://host.docker.internal:11434`. Nothing to pass |
| Same machine, Linux | Run Ollama with `OLLAMA_HOST=0.0.0.0`, then use the kind network's gateway: `docker network inspect kind -f '{{(index .IPAM.Config 0).Gateway}}'`, for example `http://172.18.0.1:11434` |
| Another machine | `http://<that-host>:11434`, with Ollama listening on `0.0.0.0` and the port reachable |

Check from inside the cluster once it is up (step 3):

```sh
kubectl --context kind-datastack run ollama-probe --rm -i --restart=Never --image=curlimages/curl:8.10.1 \
  -- curl -s -m 5 <OLLAMA_URL>/api/version
```

## 3. Bring-up (about 15 minutes the first time)

```sh
make kind-up                                  # kind cluster "datastack"
make infra                                    # CloudNativePG + Postgres
make analytics                                # StarRocks (takes 1–2 minutes to become ready)
make observability                            # OTel Collector, Tempo, Prometheus, Grafana + dashboards
make deploy-all OLLAMA_URL=<url>              # 6 services, CDC sync, analyzer, web UI (omit OLLAMA_URL on Docker Desktop)
make load RATE=5                              # k6 traffic
```

Then, in two terminals:

```sh
make grafana                                  # http://localhost:3000
make forward-web                              # http://localhost:3001
```

Let it run under load for **at least 15 minutes** before the talk, so the dashboards have history and the analyzer has data worth asking about.

## 4. Pre-flight checklist

| Check | How | Expected |
|---|---|---|
| Pods | `make status` | Everything `Running` and ready, with restarts not climbing |
| CDC | `make forward-cdc-sync`, then `curl localhost:8090/status` | Every table syncing with a lag of a few seconds, `comments_in_sync: true` |
| Dashboards | Grafana → DataStack folder | Executive shows orders and revenue. The Tracing service map shows Order with edges to five services |
| Analyzer | Web UI → Ask, with an example question | An answer within seconds (GPU) or tens of seconds (CPU). The first question after the model loads is the slow one; the analyzer warms the model at start-up |
| Analyzer accuracy | `make eval` | Record the score in [analyzer-service.md](microservices/analyzer-service.md). Rerun after changing models |
| Chaos | Web UI → Chaos | Six cards, all "healthy defaults" |
| Load | Web UI → Load | Running at the rate you set |

## 5. The demo

Each act shows one layer, then breaks something and shows the system noticing it.

### Act 1: the business is running
1. **Web UI → Load:** k6 is simulating customers. Raise the rate to 10–20.
2. **Grafana → Executive:** orders, revenue and success rate, from StarRocks, seconds behind Postgres.
3. **Grafana → Tracing:** the service map, then open one checkout trace. It is one trace across six services.

### Act 2: ask the data
1. **Web UI → Ask:** use the example chips, or ask:
   - "What is the total revenue for each customer loyalty tier?" (a bar chart appears)
   - "What is the ROAS of each paid campaign?"
   - "What is the average time to ship, in seconds, for each loyalty tier?"
2. Show the generated SQL. The joins come from the `[ref: …]` markers in the column comments, which the services' GORM models wrote. Nobody hand-wrote a semantic layer: see [data-catalog.md](microservices/data-catalog.md).
3. **View trace:** the question's own trace, showing schema prompt → LLM → guard → EXPLAIN → execute.

### Act 3: break things
Run one scenario at a time from **Web UI → Chaos**, and reset it before the next. For each one, the table lists what to change, where its effect shows, and what to ask the analyzer.

| Scenario | Knob | Where it shows | Ask the analyzer |
|---|---|---|---|
| Payment processor degrades | payment `decline_rate` 0.6 | Executive: success rate falls. Services: declines. Tracing: failed checkouts | "How many payments were declined for each decline reason?" |
| Slow dependency | inventory `latency_ms` 1500 | Tracing: the reserve span grows inside every checkout trace. Services: p95 | n/a (a latency story) |
| Warehouse backlog | shipment `ship_capacity` 0, then `packing_delay_factor` 10 | Fulfillment: backlog grows and time-to-ship climbs | "What is the average time to ship, in seconds, for each loyalty tier?" |
| Stock-out | inventory `restock_paused` on, then Drain stock 20 | Inventory: out-of-stock failures and lost revenue | "Which products are out of stock right now?" |

Close with **Reset all chaos**. The dashboards recover within a minute, and the analytics tables keep the incident as history.

## 6. Reset and teardown

- **Chaos:** "Reset all chaos" in the web UI. A pod restart also resets its knobs.
- **Load:** `make load-stop` scales k6 to zero; `make load RATE=…` starts it again.
- **Everything:** `make kind-down` deletes the cluster and all its data.

## 7. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Ask fails with `LLM_UNAVAILABLE` immediately | The analyzer can't reach Ollama | Run the probe in §2. Redeploy with the right `make deploy-analyzer OLLAMA_URL=…` |
| Ask fails with `LLM_UNAVAILABLE` after minutes | The model is running on a CPU, or a model switch evicted the cached prompt | Use a GPU machine. Stick to the default model during the talk. Lower `ASK_TIMEOUT` if you'd rather fail fast |
| Ask fails with `QUERY_FAILED` or wrong numbers | The model wrote bad SQL | Try the other model. If the same mistake keeps coming back, improve the column comment, not the prompt (analyzer-service.md, "Analytics Impact") |
| Pods restarting, probes failing | The node is short of CPU | Lower the k6 rate. Check `make status`. As a last resort, scale StarRocks to zero (§ Current Budget in `.github/skills/local-resources/SKILL.md`) |
| Dashboards empty | CDC isn't syncing or StarRocks isn't ready | `curl localhost:8090/status` (after `make forward-cdc-sync`), `kubectl --context kind-datastack logs deploy/cdc-sync-service` |
| No traces | The collector is down | `kubectl --context kind-datastack get pods -l app.kubernetes.io/name=opentelemetry-collector` |
| Load page says to run `make load` | The k6 Deployment doesn't exist yet | `make load RATE=5` once |
