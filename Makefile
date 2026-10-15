SHELL := /bin/bash

COMPOSE      ?= docker compose
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
