# Fahriddin AI — личный ассистент (Фаза 1)

Telegram-бот на Go: ежедневная сводка в 08:00 (Asia/Tashkent), свой
таск-трекер, напоминания из Google Calendar. Сводку составляет Claude
(Anthropic API) на основе задач и событий календаря.

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

Проверь в Telegram: `/start`, `/addtask купить муку`, `/tasks`, `/done 1`,
`/summary` (ручной запуск сводки без ожидания расписания).

## 4. Деплой на Railway

1. Запушь этот репозиторий на GitHub
2. В Railway создай новый проект → "Deploy from GitHub repo"
3. Добавь Postgres addon в этом же проекте (Railway сам пропишет `DATABASE_URL`)
4. В настройках сервиса добавь остальные переменные окружения из `.env.example`
5. Railway соберёт и задеплоит по `Dockerfile` автоматически

## Структура проекта

```
cmd/agent/          — точка входа
internal/config/    — загрузка конфигурации
internal/db/        — подключение к Postgres, миграции
internal/telegram/  — бот и команды
internal/scheduler/ — cron-джобы (сводка, напоминания)
internal/tasks/      — таск-трекер
internal/calendar/   — Google Calendar клиент
internal/ai/          — клиент Claude API
internal/summary/     — сборка сводки из задач+календаря → Claude
migrations/           — SQL-миграции
```
