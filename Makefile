COMPOSE ?= docker compose

# Every kubectl/helm command targets the kind cluster explicitly, so a make
# target can never touch whatever context happens to be current.
CLUSTER      ?= datastack
KUBE_CONTEXT := kind-$(CLUSTER)
KUBECTL      := kubectl --context $(KUBE_CONTEXT)
HELM         := helm --kube-context $(KUBE_CONTEXT)

CNPG_CHART_VERSION ?= 0.29.1
OTEL_COLLECTOR_CHART_VERSION ?= 0.175.1
TEMPO_CHART_VERSION          ?= 1.24.4
PROMETHEUS_CHART_VERSION     ?= 29.36.1
GRAFANA_CHART_VERSION        ?= 10.5.15

# A fresh tag per make invocation, so a redeploy always rolls the pods.
IMAGE_TAG := dev-$(shell date +%Y%m%d%H%M%S)

LOCAL_DATABASE_HOST ?= postgres://demo:demo@localhost:5433

# service:database:port for every microservice. The order is the deploy
# order: leaf services first, then the Order Service, which calls them all.
SERVICE_TABLE := customer:customers:8081 marketing:marketing:8082 inventory:inventory:8083 \
                 payment:payments:8084 shipment:shipments:8085 order:orders:8080
SERVICES      := $(foreach s,$(SERVICE_TABLE),$(word 1,$(subst :, ,$(s))))
service_field  = $(word $(2),$(subst :, ,$(filter $(1):%,$(SERVICE_TABLE))))
database_for   = $(call service_field,$(1),2)
port_for       = $(or $(call service_field,$(1),3),$(if $(filter cdc-sync,$(1)),8090))

# Journeys per second for `make load`.
RATE ?= 5

.PHONY: catalog catalog-migrate catalog-check up down reset test test-unit lint kind-up kind-down infra analytics deploy-all load load-stop status \
        observability dashboards dashboards-configmap grafana

## Local Postgres (docker compose) for `go run` development

up:
	$(COMPOSE) up -d --wait

down:
	$(COMPOSE) down

reset:
	$(COMPOSE) down --volumes --remove-orphans
	$(COMPOSE) up -d --wait

# Runs a microservice against local Postgres, e.g. `make run-customer`.
run-%:
	DATABASE_URL=$(LOCAL_DATABASE_HOST)/$(call database_for,$*)?sslmode=disable LOG_FORMAT=text \
		go run ./services/$* migrate
	DATABASE_URL=$(LOCAL_DATABASE_HOST)/$(call database_for,$*)?sslmode=disable LOG_FORMAT=text \
		go run ./services/$* serve

## Data catalog

CATALOG_DATABASES := $(foreach s,$(SERVICES),$(call database_for,$(s)))
CATALOG_ARGS       = --postgres-url "$(LOCAL_DATABASE_HOST)/?sslmode=disable" \
                     --databases $(subst $(eval) ,$(comma),$(strip $(CATALOG_DATABASES)))
comma := ,

# Migrates every service into the local Postgres, so their schema comments are
# in place. Missing databases are created, so an older compose volume works too.
catalog-migrate: up
	@for db in $(CATALOG_DATABASES); do \
		$(COMPOSE) exec -T postgres psql -U demo -d datastack -tAc "SELECT 1 FROM pg_database WHERE datname = '$$db'" | grep -q 1 \
			|| $(COMPOSE) exec -T postgres psql -U demo -d datastack -qc "CREATE DATABASE $$db"; \
	done
	@for entry in $(SERVICE_TABLE); do \
		svc=$${entry%%:*}; rest=$${entry#*:}; db=$${rest%%:*}; \
		DATABASE_URL=$(LOCAL_DATABASE_HOST)/$$db?sslmode=disable LOG_LEVEL=warn go run ./services/$$svc migrate || exit 1; \
	done

# Validates every table/column comment and [ref: ...] join marker, then
# regenerates docs/microservices/data-catalog.md.
catalog: catalog-migrate
	go run ./tools/catalog $(CATALOG_ARGS)

# Fails if the committed catalog doesn't match the models.
catalog-check: catalog-migrate
	go run ./tools/catalog $(CATALOG_ARGS) --check

## Quality

# Full suite. Integration tests start Postgres containers (needs Docker).
test:
	go test ./...

test-unit:
	go test -short ./...

lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:" && gofmt -l . && exit 1)
	go vet ./...
	@for values in deploy/values/*.yaml; do helm lint --quiet deploy/charts/go-service -f $$values || exit 1; done

## kind cluster

kind-up:
	kind create cluster --config deploy/kind/cluster.yaml

kind-down:
	kind delete cluster --name $(CLUSTER)

# Operational infrastructure: the CloudNativePG operator and the Postgres cluster.
infra:
	helm repo add cnpg https://cloudnative-pg.github.io/charts --force-update
	$(HELM) upgrade --install cnpg cnpg/cloudnative-pg --version $(CNPG_CHART_VERSION) \
		--namespace cnpg-system --create-namespace --wait
	$(KUBECTL) apply -f deploy/infra/postgres.yaml
	$(KUBECTL) wait cluster/datastack-pg --for=condition=Ready --timeout=5m

# Analytical infrastructure: StarRocks (FE and BE in one pod, about 3 GB of memory).
analytics:
	$(KUBECTL) apply -f deploy/infra/starrocks.yaml
	$(KUBECTL) rollout status statefulset/starrocks --timeout=10m

## Observability: OTel Collector -> Tempo, Prometheus, Grafana (about 1.5 GB of limits)

# Installs or upgrades the whole stack. Values live in deploy/observability/.
# The dashboards ConfigMap goes first: Grafana's pod mounts it.
observability: dashboards-configmap
	helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts --force-update
	helm repo add grafana https://grafana.github.io/helm-charts --force-update
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update
	$(HELM) upgrade --install prometheus prometheus-community/prometheus --version $(PROMETHEUS_CHART_VERSION) \
		-f deploy/observability/prometheus.yaml --wait --timeout 5m
	$(HELM) upgrade --install tempo grafana/tempo --version $(TEMPO_CHART_VERSION) \
		-f deploy/observability/tempo.yaml --wait --timeout 5m
	$(HELM) upgrade --install otel-collector open-telemetry/opentelemetry-collector --version $(OTEL_COLLECTOR_CHART_VERSION) \
		-f deploy/observability/otel-collector.yaml --wait --timeout 5m
	$(HELM) upgrade --install grafana grafana/grafana --version $(GRAFANA_CHART_VERSION) \
		-f deploy/observability/grafana.yaml --wait --timeout 5m

# Builds the grafana-dashboards ConfigMap from deploy/observability/dashboards/*.json.
# Server-side apply: a client-side apply would copy the whole ConfigMap into the
# last-applied annotation, which is capped at 256 KB.
dashboards-configmap:
	$(KUBECTL) create configmap grafana-dashboards \
		$(addprefix --from-file=,$(wildcard deploy/observability/dashboards/*.json)) \
		--dry-run=client -o yaml | $(KUBECTL) apply --server-side --force-conflicts -f -

# Refreshes the dashboards and restarts Grafana so it loads them right away
# (it would otherwise notice within a minute or two, once the kubelet syncs).
dashboards: dashboards-configmap
	$(KUBECTL) rollout restart deployment/grafana
	$(KUBECTL) rollout status deployment/grafana --timeout=2m

# Grafana on http://localhost:3000 (anonymous admin, no login).
grafana:
	$(KUBECTL) port-forward svc/grafana 3000:3000

## Services, e.g. `make deploy-customer`, `make deploy-cdc-sync`

image-%:
	docker build --build-arg SERVICE=$* -t datastack/$*:$(IMAGE_TAG) .
	kind load docker-image datastack/$*:$(IMAGE_TAG) --name $(CLUSTER)

# deploy-all is listed explicitly so it isn't matched by the deploy-% pattern.
deploy-all: $(addprefix deploy-,$(SERVICES)) deploy-cdc-sync

deploy-%: image-%
	$(HELM) upgrade --install $*-service deploy/charts/go-service \
		-f deploy/values/$*.yaml --set image.tag=$(IMAGE_TAG) --wait --timeout 5m

# Forwards a deployed service to localhost on its own port, e.g. `make forward-order`.
forward-%:
	$(KUBECTL) port-forward svc/$*-service $(call port_for,$*):$(call port_for,$*)

## Load (k6 inside the cluster)

# Starts or updates continuous load, e.g. `make load RATE=20`.
load:
	$(KUBECTL) create configmap k6-script --from-file=deploy/k6/checkout-journey.js \
		--dry-run=client -o yaml | $(KUBECTL) apply -f -
	$(KUBECTL) apply -f deploy/k6/deployment.yaml
	$(KUBECTL) set env deployment/k6 RATE=$(RATE)
	$(KUBECTL) scale deployment/k6 --replicas=1
	$(KUBECTL) rollout restart deployment/k6

# Scales k6 to zero rather than deleting it, so the web UI's load page can
# start it again.
load-stop:
	$(KUBECTL) scale deployment/k6 --replicas=0

status:
	$(KUBECTL) get pods,databases.postgresql.cnpg.io
