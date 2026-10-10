# DataStack control center (apps/web)

The web UI for the DataStack demo talk. One page per thing the presenter does on stage:

| Page | What it does | Talks to |
|---|---|---|
| **Ask the data** (`/`) | Question box, model picker, example questions. Shows the generated SQL (highlighted, copyable), the result table, a bar chart when the result is one category column plus one number column, row count / truncated / attempts / duration, and a **View trace** link into Grafana Explore (Tempo). Includes a collapsible browser for the schema the model sees. | analyzer `GET /models`, `GET /schema`, `POST /ask` |
| **Chaos** (`/chaos`) | One card per service with its knobs (sliders, number inputs, toggles), Apply (partial PUT), Reset, a "chaos active" badge when any knob differs from its default, and **Drain stock** on inventory. Has a global **Reset all chaos** button. | each service's `/admin/chaos` |
| **Load** (`/load`) | Shows the k6 Deployment's `RATE` and replicas. Sets the rate (1 to 100 journeys per second) and starts (replicas 1) or stops (replicas 0) the load. | Kubernetes API, `deployments/k6` |
| **The stack** (`/stack`) | A landscape of every tool the demo is built from, grouped by architecture layer, with a live green dot on each one that runs as a pod. A **Show libraries** toggle expands it from the 18 headline tools to all 30. | Kubernetes API, `pods` (list) |
| Sidebar | Links to every provisioned Grafana dashboard and to Tempo Explore. | Grafana (browser-side links) |

Next.js 16 (App Router), React 19, Tailwind 4, recharts. Nothing else.

The tool catalogue behind `/stack` is a plain list in `src/lib/stack.ts`: add an entry there (and a logo in `public/logos/`, see its README) and the page picks it up. A tool with a `workload` gets a live dot; one with no `logo` renders as a monogram tile.

## How it's wired

The browser only ever talks to this app. Every call to the cluster goes through a Next.js route handler under `src/app/api/`, which calls the in-cluster Service and relays the status and JSON body, so a single port-forward (to `svc/web`) is enough. Network failures and timeouts come back in the services' own `{"error", "message"}` shape (`UPSTREAM_UNAVAILABLE` 502, `UPSTREAM_TIMEOUT` 504).

| Route | Upstream |
|---|---|
| `GET /api/models`, `GET /api/schema`, `POST /api/ask` | `ANALYZER_URL` (`/ask` waits up to 280 s, because a local LLM is slow) |
| `GET/PUT/DELETE /api/chaos/{service}` | `{SERVICE}_URL/admin/chaos` |
| `POST /api/chaos/inventory/drain-stock` | `INVENTORY_URL/admin/chaos/drain-stock` |
| `GET/PUT /api/load` (`{"rate"?, "running"?}`) | Kubernetes API: GET and strategic-merge PATCH of `deployments/k6` |
| `GET /api/stack` | Kubernetes API: list pods, reduced to `{ready, restarts}` per watched workload. Answers 200 with an empty map when Kubernetes is unreachable, so the page stays a usable showcase |
| `GET /api/health` | none (probe) |

The load and stack pages use the pod's ServiceAccount token (`/var/run/secrets/kubernetes.io/serviceaccount`). The `web` ServiceAccount may only `get` and `patch` the Deployment named `k6`, and `list` pods in its own namespace (see `deploy/web/rbac.yaml`). Changing the rate patches the `RATE` env var, which rolls the k6 pod. If the Deployment doesn't exist, the page asks you to run `make load` once.

The Grafana dashboard list lives in `src/lib/dashboards.ts`, copied from the `uid` and `title` fields of `deploy/observability/dashboards/*.json` (the image's build context is `apps/web`, so it can't read them). Add an entry there when you add a dashboard.

## Configuration

All environment variables are read at request time, so one image works anywhere.

| Variable | Default | Meaning |
|---|---|---|
| `ANALYZER_URL` | `http://analyzer-service:8086` | text-to-SQL analyzer |
| `ORDER_URL` | `http://order-service:8080` | |
| `CUSTOMER_URL` | `http://customer-service:8081` | |
| `MARKETING_URL` | `http://marketing-service:8082` | |
| `INVENTORY_URL` | `http://inventory-service:8083` | |
| `PAYMENT_URL` | `http://payment-service:8084` | |
| `SHIPMENT_URL` | `http://shipment-service:8085` | |
| `GRAFANA_URL` | `http://localhost:3000` | Grafana **as the browser sees it** (the `make grafana` port-forward) |
| `K8S_NAMESPACE` | `default` | namespace of the k6 Deployment |
| `K6_DEPLOYMENT` | `k6` | name of the k6 Deployment |
| `K8S_API_URL` | unset | local development only: a `kubectl proxy` URL to use instead of the in-cluster API |
| `PORT` | `3000` (`3001` in the image) | listen port |

## Running in the kind cluster

```sh
docker build -t datastack/web:dev1 apps/web
kind load docker-image datastack/web:dev1 --name datastack
kubectl --context kind-datastack apply -f deploy/web/
kubectl --context kind-datastack set image deploy/web web=datastack/web:dev1
kubectl --context kind-datastack port-forward svc/web 3001:3001   # http://localhost:3001
make grafana                                                     # http://localhost:3000, for the links
```

The pod requests 25m CPU / 96Mi and is limited to 500m / 256Mi.

## Local development

```sh
cd apps/web
npm install
# Port-forward what you need, then point the app at localhost:
kubectl --context kind-datastack port-forward svc/analyzer-service 8086:8086 &
kubectl --context kind-datastack port-forward svc/payment-service 8084:8084 &   # and so on
kubectl --context kind-datastack proxy --port 8001 &                            # for the load page
ANALYZER_URL=http://localhost:8086 PAYMENT_URL=http://localhost:8084 \
K8S_API_URL=http://127.0.0.1:8001 PORT=3001 npm run dev
```

Anything you don't forward shows up as "unreachable" on its card. Without `K8S_API_URL`, the load page explains that Kubernetes isn't reachable instead of failing.

Before committing, run `npm run lint` and `npm run build`.
