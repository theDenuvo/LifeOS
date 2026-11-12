# LifeOS — MCP

LifeOS exposes a tools-only MCP server over Streamable HTTP at `LIFEOS_MCP_PATH`
(default `/mcp`). It reuses the same tool registry as the in-app assistant, so
REST, the assistant and MCP clients see identical capabilities.

## Включение

```env
LIFEOS_MCP_ENABLED=true
LIFEOS_MCP_PATH=/mcp
LIFEOS_MCP_ALLOW_WRITES=false
```

По умолчанию сервер **read-only**: доступны `list_tasks`, `get_today_summary`,
`list_inbox`. Мутации (`create_task`, `update_task`, `complete_task`,
`cancel_task`, `restore_task`, `reschedule_task`, `capture_inbox`,
`convert_inbox`, `dismiss_inbox`) включаются только при
`LIFEOS_MCP_ALLOW_WRITES=true`.

MCP-клиент не имеет интерфейса подтверждения, поэтому включение записи — явное
решение оператора. Встроенный ассистент всегда использует подтверждаемые
`pending_action`.

## Протокол

- `POST /mcp`, JSON-RPC 2.0
- Методы: `initialize`, `ping`, `tools/list`, `tools/call`
- Notifications (без `id`) принимаются с `202 Accepted`
- `GET /mcp` → `405` (server-initiated notifications не поддерживаются)

Пример:

```bash
curl -s http://127.0.0.1:8787/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

## Подключение клиента

Большинство MCP-клиентов поддерживают Streamable HTTP напрямую:

```json
{
  "mcpServers": {
    "lifeos": {
      "type": "http",
      "url": "https://<tailscale-host>/mcp"
    }
  }
}
```

Если клиент умеет только stdio, используйте HTTP-мост (например, `mcp-remote`).
Доступ по умолчанию локальный; публикуйте endpoint только через Tailscale Serve
или другой доверенный прокси.
