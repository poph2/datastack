COMPOSE ?= docker compose

.PHONY: up down reup reset

up:
	$(COMPOSE) up -d

down:
	$(COMPOSE) down

reup:
	$(COMPOSE) down --volumes --remove-orphans
	$(COMPOSE) up -d

reset: reup