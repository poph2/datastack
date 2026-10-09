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

.PHONY: up down reset test test-unit lint kind-up kind-down infra

## Local Postgres (docker compose) for `go run` development

up:
	$(COMPOSE) up -d --wait

down:
	$(COMPOSE) down

reset:
	$(COMPOSE) down --volumes --remove-orphans
	$(COMPOSE) up -d --wait

# Runs a service against local Postgres, e.g. `make run-customer`.
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

# Shared infrastructure: the CloudNativePG operator and the Postgres cluster.
infra:
	helm repo add cnpg https://cloudnative-pg.github.io/charts --force-update
	$(HELM) upgrade --install cnpg cnpg/cloudnative-pg --version $(CNPG_CHART_VERSION) \
		--namespace cnpg-system --create-namespace --wait
	$(KUBECTL) apply -f deploy/infra/postgres.yaml
	$(KUBECTL) wait cluster/datastack-pg --for=condition=Ready --timeout=5m

## Services, e.g. `make deploy-customer`

image-%:
	docker build --build-arg SERVICE=$* -t datastack/$*:$(IMAGE_TAG) .
	kind load docker-image datastack/$*:$(IMAGE_TAG) --name $(CLUSTER)

deploy-%: image-%
	$(HELM) upgrade --install $*-service deploy/charts/go-service \
		-f deploy/values/$*.yaml --set image.tag=$(IMAGE_TAG) --wait --timeout 3m

# Forwards a deployed service to localhost on its own port, e.g. `make forward-customer`.
forward-%:
	$(KUBECTL) port-forward svc/$*-service $(call port_for,$*):$(call port_for,$*)

# Service name -> database / port.
database_for = $(word 2,$(subst :, ,$(filter $(1):%,$(SERVICE_DATABASES))))
port_for     = $(word 2,$(subst :, ,$(filter $(1):%,$(SERVICE_PORTS))))

SERVICE_DATABASES := customer:customers inventory:inventory
SERVICE_PORTS     := customer:8081 inventory:8083
