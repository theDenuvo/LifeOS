# LifeOS — DEPLOYMENT.md

## Обзор

LifeOS — один Go-бинарник со встроенным статическим фронтендом. Разработка на Windows, деплой на Debian 13 (Intel Atom D525).

---

## 1. Разработка на Windows

### Требования
- Go 1.27.3 (`/c/Users/cheli/go/go/bin/go` в PATH)
- Node.js 20+ (`/z/Node.js/node`)
- npm 10+
- Git
- SQLite (встроен в Go через `modernc.org/sqlite`)

### Переменные окружения (`.env`)
```env
LIFEOS_LISTEN_ADDR=127.0.0.1:8787
LIFEOS_DATA_DIR=./data
LIFEOS_TIMEZONE=Europe/Moscow
LIFEOS_LLM_PROVIDER=openrouter
LIFEOS_LLM_BASE_URL=https://openrouter.ai/api/v1
LIFEOS_LLM_API_KEY=sk-...
LIFEOS_LLM_MODEL=openai/gpt-4o-mini
LIFEOS_MCP_ENABLED=false
LIFEOS_MCP_PATH=/mcp
```

### Запуск
```bash
# Backend
go run ./cmd/lifeos

# Frontend (dev, отдельный терминал)
cd web && npm run dev
# Vite на http://localhost:5173, проксирует API на 127.0.0.1:8787
```

### Структура данных (dev)
```
lifeos-repo/
├── data/
│   └── lifeos.db
├── backups/
└── .env
```

---

## 2. Сборка под Windows

```bash
go build -trimpath -ldflags="-s -w" -o lifeos.exe ./cmd/lifeos
```

Результат: `lifeos.exe` (~15-20 MB)

Запуск: `./lifeos.exe` (читает `.env` в CWD)

---

## 3. Сборка под Linux AMD64 v1 (Atom D525)

### Ключевые флаги
```bash
GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0 \
go build -trimpath -ldflags="-s -w" -o lifeos ./cmd/lifeos
```

| Флаг | Назначение |
|------|------------|
| `GOOS=linux` | Целевая ОС |
| `GOARCH=amd64` | Архитектура |
| `GOAMD64=v1` | Базовый x86-64 (без SSE4.1, SSE4.2, AVX) — критично для Atom D525 |
| `CGO_ENABLED=0` | Pure Go, без C-зависимостей |
| `-trimpath` | Убирает пути к исходникам из бинарника |
| `-ldflags="-s -w"` | Strip symbols + DWARF (уменьшает ~30%) |

### Проверка совместимости
```bash
# Проверить отсутствие запрещённых инструкций
go tool objdump -s "TEXT" ./lifeos | grep -E "SSE4|AVX" && echo "FOUND BAD INSTRUCTIONS" || echo "OK"

# Проверить зависимости
ldd ./lifeos  # должно показать "not a dynamic executable" (статический)
```

### Зависимости — проверка AMD64 v1
Все Go-зависимости должны собираться с `GOAMD64=v1`. Особенно:
- `modernc.org/sqlite` — pure Go, OK
- `github.com/openai/openai-go/v3` — pure Go, OK
- `github.com/modelcontextprotocol/go-sdk` — pure Go, OK
- `github.com/pressly/goose/v3` — pure Go, OK
- `github.com/sqlc-dev/sqlc` — только для генерации, не в рантайме

---

## 4. Встраивание Frontend в Go

### SvelteKit Static Adapter
```javascript
// web/svelte.config.js
import adapter from '@sveltejs/adapter-static';
export default {
  kit: {
    adapter: adapter({ pages: 'build', assets: 'build' }),
    paths: { base: '' },
    prerender: { entries: ['*'] }
  }
};
```

### Go embed
```go
// internal/web/dist/embed.go
package dist

//go:embed all:build
var FS embed.FS
```

### HTTP serving
```go
// internal/transport/http/static.go
func staticHandler() http.Handler {
    fs := http.FS(webdist.FS)
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        path := r.URL.Path
        if path == "/" || path == "" { path = "/index.html" }
        f, err := fs.Open(strings.TrimPrefix(path, "/"))
        if err != nil {
            // SPA fallback
            f, _ = fs.Open("index.html")
        }
        defer f.Close()
        stat, _ := f.Stat()
        http.ServeContent(w, r, path, stat.ModTime(), f.(io.ReadSeeker))
    })
}
```

### Сборка с встраиванием
```bash
# 1. Собрать frontend (SPA, fallback index.html)
cd web && npm ci && npm run build

# 2. Скопировать сборку в embed-директорию
rm -rf internal/web/dist/build && cp -r web/build internal/web/dist/build

# 3. Собрать Go с встроенным frontend
GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0 \
go build -trimpath -ldflags="-s -w" -o lifeos ./cmd/lifeos
```

### Проверка
```bash
./lifeos &
curl -I http://127.0.0.1:8787/        # 200 OK, text/html
curl -I http://127.0.0.1:8787/healthz # 200 OK, application/json
```

---

## 5. Конфигурация через переменные окружения

### Полный список

| Переменная | Обязательна | Default | Описание |
|------------|-------------|---------|----------|
| `LIFEOS_LISTEN_ADDR` | Нет | `127.0.0.1:8787` | Адрес HTTP сервера |
| `LIFEOS_DATA_DIR` | Нет | `./data` | Директория для SQLite и бэкапов |
| `LIFEOS_TIMEZONE` | Нет | `Europe/Moscow` | IANA TZ по умолчанию |
| `LIFEOS_LLM_PROVIDER` | Нет | `openrouter` | `openrouter` \| `ollama` |
| `LIFEOS_LLM_BASE_URL` | Нет* | `https://openrouter.ai/api/v1` | Base URL для LLM |
| `LIFEOS_LLM_API_KEY` | Нет* | — | API ключ (обязателен для OpenRouter) |
| `LIFEOS_LLM_MODEL` | Нет | `openai/gpt-4o-mini` | Модель по умолчанию |
| `LIFEOS_MCP_ENABLED` | Нет | `false` | Включить MCP endpoint |
| `LIFEOS_MCP_PATH` | Нет | `/mcp` | Путь для MCP Streamable HTTP |
| `LIFEOS_MCP_ALLOW_WRITES` | Нет | `false` | Разрешить мутирующие MCP-инструменты |

* — обязательны если соответствующий провайдер включен

### Приоритет конфигурации
1. Environment variables (systemd EnvironmentFile, shell)
2. `.env` файл в CWD (dev)
3. Defaults в коде

### Безопасность
- `.env` в `.gitignore`
- Секреты никогда не логируются (`slog` с фильтром)
- Production: только systemd EnvironmentFile (600, root:lifeos)

---

## 6. Размещение данных на сервере

### Структура
```
/opt/lifeos/
├── lifeos              # Бинарик (root:root, 755)
├── lifeos.service      # systemd unit (root:root, 644)
├── .env                # Секреты (root:lifeos, 640)
├── data/
│   └── lifeos.db       # SQLite (lifeos:lifeos, 660)
├── backups/
│   └── *.db.gz         # Бэкапы (lifeos:lifeos, 640)
└── logs/               # journald (systemd)
```

### Пользователь и права
```bash
useradd -r -s /bin/false -d /opt/lifeos lifeos
mkdir -p /opt/lifeos/data /opt/lifeos/backups
chown -R lifeos:lifeos /opt/lifeos/data /opt/lifeos/backups
chmod 750 /opt/lifeos/data /opt/lifeos/backups
chmod 640 /opt/lifeos/.env
chown root:lifeos /opt/lifeos/.env
```

---

## 7. Резервное копирование SQLite

### Онлайн бэкап (без остановки)
```bash
# Полный бэкап
sqlite3 /opt/lifeos/data/lifeos.db ".backup /opt/lifeos/backups/lifeos-$(date +%F).db"

# С сжатием
sqlite3 /opt/lifeos/data/lifeos.db ".backup" | gzip > /opt/lifeos/backups/lifeos-$(date +%F).db.gz
```

### Автоматизация (systemd timer)
```ini
# /etc/systemd/system/lifeos-backup.service
[Unit]
Description=LifeOS SQLite Backup
[Service]
Type=oneshot
User=lifeos
ExecStart=/bin/bash -c 'sqlite3 /opt/lifeos/data/lifeos.db ".backup" | gzip > /opt/lifeos/backups/lifeos-$(date +%%F-%%H%%M).db.gz'

# /etc/systemd/system/lifeos-backup.timer
[Unit]
Description=Daily LifeOS Backup
[Timer]
OnCalendar=daily
Persistent=true
[Install]
WantedBy=timers.target
```

### Восстановление
```bash
systemctl stop lifeos
gunzip -c /opt/lifeos/backups/lifeos-2026-11-10.db.gz > /opt/lifeos/data/lifeos.db
chown lifeos:lifeos /opt/lifeos/data/lifeos.db
systemctl start lifeos
```

### Retention
```bash
# Хранить 30 дней
find /opt/lifeos/backups -name '*.db.gz' -mtime +30 -delete
```

---

## 8. systemd Service

```ini
# /etc/systemd/system/lifeos.service
[Unit]
Description=LifeOS Personal Operating System
Documentation=https://github.com/theDenuvo/LifeOS
After=network-online.target tailscale.service
Wants=network-online.target

[Service]
Type=simple
User=lifeos
Group=lifeos
WorkingDirectory=/opt/lifeos
ExecStart=/opt/lifeos/lifeos
EnvironmentFile=/opt/lifeos/.env
Restart=on-failure
RestartSec=5
TimeoutStartSec=30
TimeoutStopSec=10
LimitNOFILE=65536
LimitNPROC=4096

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/lifeos/data /opt/lifeos/backups
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=lifeos

[Install]
WantedBy=multi-user.target
```

### Команды управления
```bash
systemctl daemon-reload
systemctl enable --now lifeos
systemctl status lifeos
journalctl -u lifeos -f
systemctl restart lifeos
```

---

## 9. Tailscale Serve

### Вариант 1: Tailscale terminates HTTPS (рекомендуется)
```bash
# На сервере
tailscale serve --bg --https=443 --set-path / /opt/lifeos/lifeos
# Или указать порт:
tailscale serve --bg 8787 http://127.0.0.1:8787
```

### Вариант 2: LifeOS слушает на Tailscale IP
```env
LIFEOS_LISTEN_ADDR=100.x.y.z:8787  # Tailscale IP
```
```bash
tailscale serve --bg --https=443 --set-path / http://100.x.y.z:8787
```

### Проверка
```bash
# Своего устройства в tailnet
curl https://<machine-name>.<tailnet>.ts.net/healthz
curl https://<machine-name>.<tailnet>.ts.net/
```

---

## 10. Обновление версии

### На Windows (сборка)
```bash
# 1. Обновить версию в коде (git tag)
git tag v1.0.0

# 2. Собрать frontend
cd web && npm ci && npm run build

# 3. Собрать Linux бинарник
GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=0 \
go build -trimpath -ldflags="-s -w -X main.version=v1.0.0" -o lifeos ./cmd/lifeos

# 4. Проверить
./lifeos -version  # если флаг реализован
```

### На сервере (деплой)
```bash
# 1. Скопировать бинарник
scp lifeos user@atom:/tmp/lifeos.new

# 2. На сервере
ssh user@atom
sudo systemctl stop lifeos
sudo cp /opt/lifeos/lifeos /opt/lifeos/lifeos.bak
sudo cp /tmp/lifeos.new /opt/lifeos/lifeos
sudo chown root:root /opt/lifeos/lifeos
sudo chmod 755 /opt/lifeos/lifeos
sudo systemctl start lifeos

# 3. Проверить
curl http://127.0.0.1:8787/healthz
journalctl -u lifeos -f
```

---

## 11. Откат при неудачном обновлении

```bash
# На сервере
sudo systemctl stop lifeos
sudo mv /opt/lifeos/lifeos /opt/lifeos/lifeos.failed
sudo mv /opt/lifeos/lifeos.bak /opt/lifeos/lifeos
sudo systemctl start lifeos

# Проверить
curl http://127.0.0.1:8787/healthz
```

---

## 12. Мониторинг и логи

### Логи
```bash
#journald
journalctl -u lifeos -f
journalctl -u lifeos --since "1 hour ago" -o json

# Структурированные логи (slog JSON)
journalctl -u lifeos -o json | jq '.MESSAGE'
```

### Health checks
```bash
# Liveness
curl -f http://127.0.0.1:8787/healthz || exit 1

# Readiness (если добавим проверку БД)
curl -f http://127.0.0.1:8787/readyz || exit 1
```

### Метрики (базовые, через logs)
- Request latency (slog attributes)
- Error rate
- Active connections
- DB query duration

---

## 13. Checklist перед деплоем

- [ ] `GOAMD64=v1` сборка проходит
- [ ] `CGO_ENABLED=0` — бинарник статический
- [ ] Frontend встроен (`embed.FS`)
- [ ] `.env` с секретами готов
- [ ] `data/` директория создана с правами
- [ ] systemd unit установлен и включён
- [ ] Tailscale Serve настроен
- [ ] Backup timer настроен
- [ ] Health endpoint отвечает
- [ ] Frontend отдаётся корректно
- [ ] SSE endpoint работает
- [ ] MCP endpoint доступен (если включён)