# webinar_mtx

Приложение для управления вебинарами со стримингом через [MediaMTX](https://mediamtx.org):
уникальные комнаты, доступ только для зарегистрированных по email участников,
админка с импортом списков и webhook-интеграциями для авторегистрации после оплаты,
плеер на WebRTC с задержкой меньше секунды.

Написано на Go (один статический бинарник, шаблоны и статика встроены) — весь стек
потребляет ~150–200 МБ RAM в простое.

```
Ведущий (OBS) --RTMP--> MediaMTX --WebRTC/WHEP--> Браузер участника
                            ↑  external auth         ↑  сессия участника
        Приложение (Go + PostgreSQL) ──── админка, комнаты, вебхуки ────
```

## Состав стека

| Компонент | Назначение |
|---|---|
| `app` | Приложение (Go, ~30 МБ RAM): админка, комнаты, вебхуки, WHEP-прокси |
| `db` | PostgreSQL 16 |
| `mediamtx` | Стриминг-сервер: приём RTMP, раздача WebRTC |

## Быстрый старт

```bash
cp .env.example .env
# отредактируйте .env: ADMIN_EMAIL, ADMIN_PASSWORD, APP_SECRET (openssl rand -hex 32),
# MEDIA_HOST — домен или публичный IP сервера
docker compose up -d
```

Админка: `http://localhost:8080/admin` (данные из `ADMIN_EMAIL`/`ADMIN_PASSWORD`).

Порядок первого запуска:

1. **Создайте вебинар** — на странице вебинара появятся:
   - RTMP-сервер и **ключ потока** для OBS/кодировщика;
   - **ссылка комнаты** `/w/{slug}` для рассылки участникам.
2. **Импортируйте участников** (CSV) или добавьте вручную — только эти email смогут войти.
3. Установите статус **«В эфире»** и начните трансляцию в OBS — плеер участников
   подхватит эфир автоматически (переподключается сам, пока эфира нет).
4. В админке виден живой счётчик зрителей и статус эфира.

## Доступ участников

- **`ACCESS_MODE=direct`** (по умолчанию): участник вводит email на странице комнаты;
  если email есть в списке — сразу открывается плеер.
- **`ACCESS_MODE=code`**: на введённый email дополнительно приходит одноразовый код
  (нужны настроенные `SMTP_*`). Гарантирует, что вошёл владелец почты.

Заблокированный администратором участник теряет доступ мгновенно (активные сессии сбрасываются).

Как защищён стрим:

- путь потока в MediaMTX — `w/{случайный ключ}`, подобрать его невозможно;
- каждая публикация и каждое чтение проходят через **external authentication**
  MediaMTX → приложение сверяет ключ вебинара / токен сессии участника;
- сигналинг WHEP идёт через приложение (проверка сессии), а видео летит напрямую
  браузер ↔ MediaMTX — приложение не тратит ресурсы на трафик.

## Вебхуки (авторегистрация после оплаты)

Админка → «Вебхуки» → создать. Для приёма платежей настраиваются:

- **URL** — `https://ваш-домен/hooks/{id}` + секрет в заголовке `X-Webhook-Secret`
  (или параметр `?secret=`);
- **пути к полям** в JSON-уведомлении: где искать email клиента и id товара/тарифа
  (dot-нотация, например `object.metadata.email`);
- **маппинг** `id_товара = slug_вебинара` — какому вебинару соответствует товар;
- необязательный **вебинар по умолчанию**, если товар в маппинге не найден.

Есть пресеты полей для ЮKassa, CloudPayments, Stripe, Prodamus и Т-Банка и кнопка
«тестовый запрос». Повторные уведомления идемпотентны. Поддерживаются JSON и
`form-urlencoded`.

Пример (ЮKassa-подобный payload):

```bash
curl -X POST https://домен/hooks/wh_xxx \
  -H "X-Webhook-Secret: whsec_..." \
  -H "Content-Type: application/json" \
  -d '{"object":{"metadata":{"email":"client@example.com","product":"webinar-pro"}}}'
```

## Переменные окружения

Все переменные со значениями и комментариями — в [`.env.example`](.env.example).

Ключевые: `APP_URL` (публичный адрес, используется в ссылках и письмах),
`MEDIA_HOST` (публичный адрес для ICE-кандидатов WebRTC — без него браузеры за NAT
не подключатся), `RTMP_URL` (адрес RTMP, показываемый ведущему), `APP_SECRET`.

## Порты

| Порт | Назначение |
|---|---|
| 8080/tcp | Приложение (админка + комнаты + WHEP-прокси) |
| 1935/tcp | RTMP: приём трансляции (OBS) |
| 8889/tcp | WHEP-сигналинг MediaMTX |
| 8189/tcp+udp | ICE: медиа-трафик WebRTC |

UDP 8189 даёт минимальную задержку; TCP 8189 — запасной путь для сетей с
блокировкой UDP. Для лучшей задержки на Linux можно запускать mediamtx с
`network_mode: host`.

## Сборка и публикация контейнера

- **Dockerfile** — multi-stage сборка, итоговый образ на distroless/static ~20 МБ.
- `.github/workflows/release.yml` — на каждый push в `main` и теги `v*`: тесты →
  сборка multi-arch (amd64+arm64) → публикация в GHCR
  `ghcr.io/vdao/webinar_mtx` (теги `latest`, `vX.Y.Z`, sha). Аутентификация через
  встроенный `GITHUB_TOKEN` — секреты настраивать не нужно.

## Автодеплой

`.github/workflows/deploy.yml` деплоит по SSH при публикации тега `v*` (или вручную):
`docker compose pull && docker compose up -d` на сервере.

Настройка (Settings → Secrets and variables → Actions):

| Где | Имя | Значение |
|---|---|---|
| Variables | `DEPLOY_HOST` | адрес сервера (без переменной деплой просто пропускается) |
| Secrets | `DEPLOY_USER` | SSH-пользователь (по умолчанию `root`) |
| Secrets | `DEPLOY_KEY` | приватный SSH-ключ |
| Secrets | `DEPLOY_PATH` | каталог с `docker-compose.yml` (по умолчанию `/opt/webinar_mtx`) |

На сервере в каталоге должны лежать `docker-compose.yml`, `.env`
и `mediamtx/mediamtx.yml` из этого репозитория.

## Производство: HTTPS

Для продакшена поставьте reverse-proxy с TLS (Caddy/Traefik/nginx) перед приложением:
`https://вебинар.домен` → `app:8080`. WHEP-плеер работает и по HTTP, но HTTPS
обязателен, если включите `ACCESS_MODE=code` (некоторые браузерные ограничения) —
и просто правильный тон для страниц с входом.

## Разработка

```bash
go test ./...        # юнит-тесты
go vet ./...
docker compose up    # локальный стек: приложение + postgres + mediamtx
```

Тестовый поток без OBS:

```bash
docker run --rm --network webinar_mtx_default --entrypoint ffmpeg \
  bluenviron/mediamtx:1.21.3-ffmpeg \
  -re -f lavfi -i testsrc2=size=1280x720:rate=30 -f lavfi -i sine=frequency=440 \
  -c:v libx264 -preset veryfast -tune zerolatency -c:a aac -f flv \
  rtmp://mediamtx:1935/w/КЛЮЧ_ПОТОКА
```

Структура кода:

```
cmd/server          точка входа
internal/config     переменные окружения
internal/db         подключение и миграции (embedded SQL)
internal/store      все SQL-запросы
internal/model      сущности
internal/web        HTTP-хендлеры: админка, комнаты, WHEP-прокси, вебхуки, external auth
internal/mtx        клиент REST API MediaMTX (счётчик зрителей)
internal/mail       SMTP-отправка одноразовых кодов
internal/csvutil    парсер CSV-импорта
internal/auth       сессия админа, cookie участников, rate limiter
web → internal/web/{templates,static}  встроенные шаблоны и статика
```
