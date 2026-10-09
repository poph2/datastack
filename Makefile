COMPOSE ?= docker compose

# Every kubectl/helm command targets the kind cluster explicitly, so a make
# target can never touch whatever context happens to be current.
CLUSTER      ?= datastack
KUBE_CONTEXT := kind-$(CLUSTER)
KUBECTL      := kubectl --context $(KUBE_CONTEXT)
HELM         := helm --kube-context $(KUBE_CONTEXT)

CNPG_CHART_VERSION ?= 0.29.1

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

.PHONY: up down reset test test-unit lint kind-up kind-down infra analytics deploy-all load load-stop status

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
	$(KUBECTL) rollout restart deployment/k6

load-stop:
	$(KUBECTL) delete deployment k6 --ignore-not-found

status:
	$(KUBECTL) get pods,databases.postgresql.cnpg.io
