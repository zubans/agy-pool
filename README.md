# agy-pool

agy-pool — пул аккаунтов Antigravity CLI и шлюз к моделям. Самостоятельный сервис: принимает запросы «промпт → ответ модели» и выполняет их моделями разных провайдеров — Antigravity CLI (agy) по подписке Google, Anthropic, OpenAI, OpenAI-совместимые и локальные. agy работает одним активным аккаунтом: когда он упирается в лимит, получает отказ по входу или региону, сервис отключает его, помечает время возврата и сразу продолжает следующим.

## Требования

- Docker с Compose v2 — для запуска;
- Go (версия из `go.mod`) — для тестов и разработки; тестам тоже нужен Docker.

## Запуск

```sh
cp .env.example .env   # make up сделает это сам, если .env нет
make up
```

`make up` создаёт внешнюю сеть `agy-pool` (если её нет), собирает образы и поднимает контейнеры, дожидаясь их готовности:

| Контейнер | Что |
|---|---|
| `app` | сервер `agy-pool` и Antigravity CLI (`agy`) |
| `postgres` | PostgreSQL 16 — всё состояние сервиса |
| `nats` | NATS с JetStream, данные — в томе `nats_data` |
| `sandboxd` | исполнитель песочниц навыков; пока заглушка с `/healthz` |

Тома: `agy_accounts` → `/data/accounts` (домашние папки аккаунтов agy), `agy_skills` → `/data/skills` (каталог навыков), `agy_work` → `/data/work` (рабочие папки запросов), `pg_data`, `nats_data`.

Миграции БД применяются при старте `app`; вручную — `make migrate`.

## Локальный запуск

Есть два варианта:

- `make up` — как на сервере: порты наружу не публикуются, к `app` можно обратиться только из внешней сети `agy-pool`;
- `make up-dev` — то же самое плюс `compose.dev.yaml`: порты `app`, `postgres` и `nats` публикуются на `127.0.0.1` хоста, журнал `app` пишется на уровне `debug`.

```sh
make up-dev
curl -s localhost:8080/healthz                          # 200 {"status":"ok",...}
curl -s 'localhost:8222/healthz?js-enabled-only=true'   # NATS с JetStream
psql postgres://agy:agy@localhost:5432/agy -c 'select 1'
make agy-version                                        # версия agy в app
make down-dev
```

После `make up` проверить сервис можно из сети `agy-pool`:

```sh
docker run --rm --network agy-pool curlimages/curl -s http://agy-pool:8080/healthz
make down
```

Порты слушают только на `127.0.0.1`, поэтому из сети сервис недоступен. Если на хосте порт уже занят (например, 5432 у локального PostgreSQL или 8080), задайте другой номер в `.env`: `AGY_DEV_HTTP_PORT`, `AGY_DEV_PG_PORT`, `AGY_DEV_NATS_PORT` или `AGY_DEV_NATS_MON_PORT`. `make config` проверяет, что оба варианта compose валидны.

## Подключение клиентов

Порт наружу не публикуется. Клиент в Docker подключает свой контейнер к внешней сети `agy-pool` и обращается к `http://agy-pool:8080`:

```yaml
services:
  worker:
    networks: [default, agy-pool]

networks:
  agy-pool:
    external: true
```

## Служебные адреса

- `GET /healthz` — `200 {"status":"ok","checks":{"postgres":"ok","nats":"ok"}}`, если PostgreSQL и NATS (с JetStream) отвечают; иначе `503` и причина в `checks`.
- `GET /metrics` — метрики Prometheus: `agy_pool_http_requests_total`, `agy_pool_http_request_duration_seconds`, `agy_pool_dependency_up`, `agy_pool_build_info`, метрики Go и процесса.

## Цели make

| Цель | Что |
|---|---|
| `make up` | собрать и поднять все контейнеры |
| `make down` | остановить контейнеры (тома сохраняются) |
| `make up-dev`, `make down-dev` | то же с портами на `127.0.0.1` (`compose.dev.yaml`) |
| `make config` | проверить `compose.yaml` и `compose.dev.yaml` |
| `make build` | собрать образы |
| `make test` | все тесты; testcontainers поднимают PostgreSQL и NATS |
| `make test-short` | тесты без Docker |
| `make migrate` | применить миграции БД |
| `make migrate-status` | состояние миграций |
| `make sqlc` | сгенерировать Go-код запросов из `internal/db/queries` |
| `make logs` | журнал `app` (JSON) |
| `make ps` | состояние контейнеров |
| `make agy-version` | версия agy в контейнере `app` |
| `make lint`, `make fmt` | `go vet` и `gofmt` |

## Настройки

Переменные окружения — в `.env`; все с умолчаниями и пояснениями перечислены в `.env.example`. Основные (по ТЗ, раздел «Настройки»): `AGY_TIMEOUT_MAX`, `AGY_CONCURRENCY`, `AGY_QUOTA_COOLDOWN`, `AGY_AUTH_COOLDOWN`, `AGY_REGION_COOLDOWN`, `AGY_PREFER_PRIMARY`, `AGY_LOG_DAYS`, `AGY_ATTACH_MAX_FILES`, `AGY_ATTACH_MAX_BYTES`, `AGY_SECRETS_KEY`, `AGY_CURRENCY`, `AGY_TIMEZONE`, `AGY_ENDPOINT_COOLDOWN`, `AGY_JOBS_TTL`, `AGY_JOB_MAX_ATTEMPTS`, `NATS_URL`, `AGY_FILES_TTL`, `AGY_SANDBOX_CPU`, `AGY_SANDBOX_MEMORY`, `AGY_SANDBOX_TIMEOUT`. Служебные: `AGY_HTTP_ADDR`, `DATABASE_URL`, `AGY_DATA_DIR`, `AGY_LOG_LEVEL`, `AGY_SANDBOXD_URL`. Только для `make up-dev`: `AGY_DEV_HTTP_PORT`, `AGY_DEV_PG_PORT`, `AGY_DEV_NATS_PORT`, `AGY_DEV_NATS_MON_PORT` (порты на хосте).

Длительности задаются целым числом секунд. Неверное значение останавливает запуск с сообщением, какую переменную исправить.

Версия Antigravity CLI закреплена в `Dockerfile` (`AGY_VERSION` и контрольные суммы архивов для amd64 и arm64); для обновления поменяйте версию и суммы.

## Структура

```text
cmd/agy-pool/        сервер: serve, migrate, healthcheck, version
cmd/sandboxd/        исполнитель песочниц (заглушка)
internal/config/     настройки из окружения
internal/db/         пул pgx, миграции goose (migrations/), запросы sqlc (queries/ → sqlc/)
internal/httpapi/    маршруты chi, /healthz, /metrics
internal/natsx/      подключение к NATS и JetStream
internal/logging/    slog в JSON
internal/testutil/   PostgreSQL и NATS для тестов (testcontainers-go)
```
