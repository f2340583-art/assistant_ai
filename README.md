# Fahriddin AI — личный ассистент (Фаза 1)

Telegram-бот на Go: ежедневная сводка в 08:00 (Asia/Tashkent), свой
таск-трекер, напоминания из Google Calendar. Сводку составляет Claude
(Anthropic API) на основе задач и событий календаря.

Бот общается **на узбекском (латиница)**. Никаких команд запоминать не нужно —
пиши обычным текстом или голосовым сообщением, бот сам понимает, что нужно
сделать (Claude разбирает намерение). Внизу чата есть кнопки-ярлыки для
самых частых действий.

Фаза 2 (позже): Google Sheets, Billz, Instagram-аналитика.

## 1. Получить credentials

### Telegram bot token
1. Открой в Telegram [@BotFather](https://t.me/BotFather)
2. `/newbot`, следуй инструкциям
3. Получишь токен вида `123456789:AAExampleToken` — это `TELEGRAM_BOT_TOKEN`

### Твой Telegram user ID
1. Открой [@userinfobot](https://t.me/userinfobot), напиши ему что угодно
2. Он пришлёт твой числовой ID — это `TELEGRAM_OWNER_ID`
   (бот будет отвечать только этому ID, это защита от посторонних)

### Google service account (для Calendar)
1. Зайди в [Google Cloud Console](https://console.cloud.google.com/), создай проект
2. В "APIs & Services" → "Library" включи **Google Calendar API**
3. "APIs & Services" → "Credentials" → "Create Credentials" → "Service account"
4. Создай account, затем открой его → вкладка "Keys" → "Add Key" → "Create new key" → JSON — скачается файл
5. Открой скачанный JSON, скопируй **всё содержимое одной строкой** — это `GOOGLE_SERVICE_ACCOUNT_JSON`
6. В файле JSON найди поле `client_email` (что-то вроде `agent@project.iam.gserviceaccount.com`)
7. Зайди в [Google Calendar](https://calendar.google.com/) → настройки твоего календаря →
   "Share with specific people" → добавь этот email с правом "See all event details"

### Speech-to-Text (для голосовых сообщений)
В том же Google Cloud проекте, где создавал service account для Calendar:
1. "APIs & Services" → "Library" → включи **Cloud Speech-to-Text API**
2. Убедись, что на проекте включён billing (Speech-to-Text — платный API,
   но есть небольшой бесплатный лимит в месяц) — без этого голосовые
   сообщения не будут распознаваться, но всё остальное будет работать
3. Отдельный ключ не нужен — используется тот же `GOOGLE_SERVICE_ACCOUNT_JSON`

Если не настроить этот шаг — бот продолжит работать нормально, просто на
голосовые сообщения будет отвечать, что пока не понимает голос, и попросит
написать текстом.

### Billz (бизнес-показатели на дашборде, опционально)
1. Зайди в свой аккаунт Billz → **Ключи интеграции**
2. Открой нужный ключ (или создай новый) → раздел **Роль**
3. Включи права в разделах **"Продажи"** и **"Отчёты"** (галочки внутри
   каждого раздела, не только общий тумблер) → сохрани/примени
4. Скопируй сам `secret_token` этого ключа — это `BILLZ_SECRET_TOKEN`

Бот использует Billz **только на чтение** — вызывает исключительно
отчётные эндпоинты (`/v1/general-report` и т.п.), ничего не создаёт и не
меняет в самом Billz. Без этого ключа дашборд просто не показывает
бизнес-блок (выручка, магазины) — остальное работает как обычно.

### Anthropic API key
1. Зайди на [console.anthropic.com](https://console.anthropic.com/)
2. Создай API key — это `ANTHROPIC_API_KEY`
   (это отдельный ключ от Claude Code, используется рантаймом агента)

### Postgres
Если деплоишь на Railway — просто добавь Postgres addon в проекте, Railway
сам даст `DATABASE_URL`. Для локального запуска можно поднять Postgres в Docker:
```
docker run -d --name fahriddin-db -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:16
```
и использовать `postgres://postgres:postgres@localhost:5432/postgres`.

## 2. Настроить .env

Скопируй `.env.example` в `.env` и заполни все значения, полученные выше.

## 3. Локальный запуск

```
go run ./cmd/agent
```

Открой бота в Telegram, нажми **Start**, дальше просто пиши обычным текстом
или голосом:
- "Ertaga soat 15:00 da mijoz bilan uchrashuvni qo'sh" — добавит задачу с дедлайном
- "Vazifalarimni ko'rsat" — покажет открытые задачи (или кнопка 📋)
- "Bugungi xulosani ber" — сводка на сейчас (или кнопка 📅)
- "5-vazifani bajardim" — отметит задачу #5 выполненной

Команды печатать не нужно — бот понимает свободный текст и голосовые
сообщения.

## 4. Деплой на Railway

1. Запушь этот репозиторий на GitHub
2. В Railway создай новый проект → "Deploy from GitHub repo"
3. Добавь Postgres addon в этом же проекте (Railway сам пропишет `DATABASE_URL`)
4. В настройках сервиса добавь остальные переменные окружения из `.env.example`
5. Railway соберёт и задеплоит по `Dockerfile` автоматически

## 5. Telegram Mini App (дашборд + таски внутри Telegram)

Бот отдаёт полноценный веб-интерфейс (дашборд со сводкой + список задач)
через кнопку меню Telegram — тот же Go-сервис отдаёт и статику, и API,
отдельный деплой не нужен. Но Telegram требует **публичный HTTPS URL** —
`localhost` не подходит.

Два варианта получить такой URL:

**Вариант А — задеплоить на Railway (проще, постоянный URL)**
1. После деплоя (шаг 4 выше) открой сервис в Railway → Settings → Networking →
   "Generate Domain" — получишь URL вида `https://your-app.up.railway.app`
2. Пропиши `WEBAPP_URL=https://your-app.up.railway.app` в переменных окружения
3. Перезапусти сервис — при старте бот сам зарегистрирует кнопку меню

**Вариант Б — туннель для локальной проверки**
```
cloudflared tunnel --url http://localhost:8080
```
Полученный `https://*.trycloudflare.com` URL пропиши как `WEBAPP_URL` в `.env`
и перезапусти `go run ./cmd/agent` локально.

Если `WEBAPP_URL` не задан — бот просто не показывает кнопку меню и работает
как раньше, в чат-режиме (ничего не ломается).

После настройки: в Telegram рядом с полем ввода появится кнопка меню — она
открывает дашборд и список задач. Задачи, добавленные через Mini App, сразу
видны и в обычном чате (общая база данных).

## Структура проекта

```
cmd/agent/          — точка входа
internal/config/    — загрузка конфигурации
internal/db/        — подключение к Postgres, миграции
internal/telegram/  — бот, кнопки, свободный текст/голос → NLU
internal/scheduler/ — cron-джобы (сводка, напоминания)
internal/tasks/      — таск-трекер
internal/calendar/   — Google Calendar клиент
internal/ai/          — клиент Claude API (сводка, классификация намерений, чат)
internal/stt/          — Google Speech-to-Text клиент (голосовые сообщения)
internal/summary/     — сборка сводки из задач+календаря → Claude
internal/webapp/       — Telegram Mini App: статика (внутри web/) + JSON API
internal/billz/         — read-only клиент Billz (выручка/отчёты по магазинам)
migrations/           — SQL-миграции
```
