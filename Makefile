SHELL := /bin/bash

COMPOSE      ?= docker compose
COMPOSE_DEV  ?= $(COMPOSE) -f compose.yaml -f compose.dev.yaml
NETWORK      ?= agy-pool
SQLC_VERSION ?= 1.31.1
GO           ?= go

.DEFAULT_GOAL := help

.PHONY: help
help: ## список целей
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

.env:
	cp .env.example .env

.PHONY: network
network: ## создать внешнюю сеть agy-pool, если её нет
	@docker network inspect $(NETWORK) >/dev/null 2>&1 || docker network create $(NETWORK)

.PHONY: up
up: .env network ## собрать и поднять все контейнеры, дождаться готовности
	$(COMPOSE) up -d --build --wait

.PHONY: down
down: ## остановить контейнеры (тома сохраняются)
	$(COMPOSE) down

.PHONY: up-dev
up-dev: .env network ## как up, но с портами app, postgres и nats на 127.0.0.1
	$(COMPOSE_DEV) up -d --build --wait

.PHONY: down-dev
down-dev: ## остановить контейнеры, поднятые up-dev (тома сохраняются)
	$(COMPOSE_DEV) down

.PHONY: config
config: .env ## проверить compose.yaml и compose.dev.yaml
	$(COMPOSE) config -q
	$(COMPOSE_DEV) config -q

.PHONY: build
build: .env ## собрать образы
	$(COMPOSE) build

.PHONY: test
test: ## тесты (нужен Docker: testcontainers поднимают PostgreSQL и NATS)
	$(GO) test -race ./...

.PHONY: test-short
test-short: ## тесты без Docker
	$(GO) test -race -short ./...

.PHONY: migrate
migrate: .env network ## применить миграции БД
	$(COMPOSE) run --rm app migrate up

.PHONY: migrate-status
migrate-status: .env network ## состояние миграций
	$(COMPOSE) run --rm app migrate status

.PHONY: sqlc
sqlc: ## сгенерировать Go-код запросов sqlc
	docker run --rm -u $$(id -u):$$(id -g) -v "$(CURDIR)":/src -w /src sqlc/sqlc:$(SQLC_VERSION) generate

.PHONY: logs
logs: ## журнал приложения
	$(COMPOSE) logs -f --tail=200 app

.PHONY: ps
ps: ## состояние контейнеров
	$(COMPOSE) ps

.PHONY: agy-version
agy-version: ## версия Antigravity CLI в контейнере app
	$(COMPOSE) exec app agy --version

.PHONY: fmt
fmt: ## форматирование кода
	gofmt -w .

.PHONY: lint
lint: ## проверки go vet и gofmt
	$(GO) vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
