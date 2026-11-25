# keycrm-mcp-go
MCP-сервер для keyCRM: Claude працює із замовленнями, покупцями, товарами, залишками та оплатами. Прибуткові накладні перераховують середньозважену собівартість, воронки вмикаються за потреби.

[English version](README.en.md)

Сервер підключає ваш акаунт [keyCRM](https://keycrm.app) до будь-якого MCP-клієнта: Claude Desktop, Claude Code, Cursor тощо. Він працює лише через офіційний публічний API keyCRM (`https://openapi.keycrm.app/v1`) і сам не звертається до жодної мовної моделі: повертає компактні дані, а міркує клієнт. Один статичний бінарник без бази даних і без стану.

Що можна попросити в Claude:

- «Покажи нові замовлення за сьогодні й суму по кожному».
- «Знайди покупця за номером 067 123 45 67 і його останні замовлення».
- «Скільки лишилося SKU-001 на складах?»
- «Оприбуткуй накладну: 1 шт SKU-001 по 59 грн, склад 1». Спершу буде попередній перегляд, запис — лише після вашого підтвердження.

## Встановлення

### Homebrew (macOS, Linux)

```bash
brew install --cask grengojbo/tap/keycrm-mcp
```

### Docker

```bash
docker pull ghcr.io/grengojbo/keycrm-mcp-go:latest
```

Образ на базі `distroless/static:nonroot`: без shell, від імені непривілейованого користувача, для `linux/amd64` і `linux/arm64`.

### go install

Потрібен Go 1.27 або новіший.

```bash
go install github.com/grengojbo/keycrm-mcp-go/cmd/keycrm-mcp@latest
```

### Готовий бінарник

Архіви для linux, darwin і windows (amd64, arm64) лежать на сторінці [Releases](https://github.com/grengojbo/keycrm-mcp-go/releases). Перевірте контрольну суму з `checksums.txt`, розпакуйте й покладіть `keycrm-mcp` у теку з `PATH`.

Перевірка:

```bash
keycrm-mcp --version
```

## Ключ API

Ключ створюється в keyCRM: **Налаштування → Загальні → API**. Він дає повний доступ до CRM, тому:

- не вставляйте його в чат з моделлю й не комітьте в git;
- для перших спроб увімкніть режим лише читання (`KEYCRM_READ_ONLY=true`).

У прикладах нижче `<ваш-ключ-keycrm>` і `<довгий-випадковий-токен>` — заглушки.

## Підключення

### Claude Desktop (stdio)

Файл налаштувань: **Settings → Developer → Edit Config** (`claude_desktop_config.json`).

```json
{
  "mcpServers": {
    "keycrm": {
      "command": "keycrm-mcp",
      "env": {
        "KEYCRM_API_KEY": "<ваш-ключ-keycrm>",
        "KEYCRM_READ_ONLY": "true"
      }
    }
  }
}
```

Якщо Claude Desktop не бачить команду, вкажіть повний шлях: `which keycrm-mcp`.

Через Docker:

```json
{
  "mcpServers": {
    "keycrm": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "-e", "KEYCRM_API_KEY", "ghcr.io/grengojbo/keycrm-mcp-go:latest"],
      "env": { "KEYCRM_API_KEY": "<ваш-ключ-keycrm>" }
    }
  }
}
```

### Claude Code (stdio)

```bash
claude mcp add keycrm -e KEYCRM_API_KEY=<ваш-ключ-keycrm> -- keycrm-mcp
```

Лише читання і тільки потрібні групи:

```bash
claude mcp add keycrm -e KEYCRM_API_KEY=<ваш-ключ-keycrm> -e KEYCRM_READ_ONLY=true -e KEYCRM_TOOLSETS=orders,buyers,stock -- keycrm-mcp
```

### Cursor та інші клієнти (stdio)

Той самий блок `mcpServers`, що й для Claude Desktop, у `~/.cursor/mcp.json` або в налаштуваннях вашого клієнта.

### HTTP з Bearer-токеном

Для команди або віддаленого сервера є транспорт Streamable HTTP на шляху `/mcp`. Без `KEYCRM_MCP_HTTP_TOKEN` сервер у цьому режимі не стартує.

```bash
export KEYCRM_API_KEY=<ваш-ключ-keycrm>
export KEYCRM_MCP_HTTP_TOKEN=<довгий-випадковий-токен>   # напр. openssl rand -hex 32
keycrm-mcp --http 127.0.0.1:8080
```

Через Docker (порт відкрито лише на localhost):

```bash
docker run --rm -p 127.0.0.1:8080:8080 -e KEYCRM_API_KEY -e KEYCRM_MCP_HTTP_TOKEN ghcr.io/grengojbo/keycrm-mcp-go:latest --http :8080
```

Claude Code:

```bash
claude mcp add --transport http keycrm http://127.0.0.1:8080/mcp --header "Authorization: Bearer <довгий-випадковий-токен>"
```

Claude Desktop у файлі налаштувань запускає лише локальні процеси, тому до HTTP-сервера він підключається через міст [`mcp-remote`](https://www.npmjs.com/package/mcp-remote):

```json
{
  "mcpServers": {
    "keycrm": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "http://127.0.0.1:8080/mcp", "--header", "Authorization: Bearer ${KEYCRM_MCP_HTTP_TOKEN}"],
      "env": { "KEYCRM_MCP_HTTP_TOKEN": "<довгий-випадковий-токен>" }
    }
  }
}
```

Сам сервер TLS не термінує. Якщо він доступний не лише з localhost, ставте його за зворотним проксі з HTTPS.

## Налаштування

Змінні оточення (приклад — [`.env.example`](.env.example)):

| Змінна | За замовчуванням | Опис |
|---|---|---|
| `KEYCRM_API_KEY` | — | Ключ API keyCRM, обов'язковий. |
| `KEYCRM_API_URL` | `https://openapi.keycrm.app/v1` | Адреса API. Змінювати лише для тестів з фейковим сервером. |
| `KEYCRM_TOOLSETS` | `all` | Групи інструментів через кому, напр. `orders,buyers,catalog,stock,receipts`. Невідома назва зупиняє старт. |
| `KEYCRM_READ_ONLY` | `false` | `true` — реєструються лише інструменти читання. |
| `KEYCRM_RATE_LIMIT` | `20` | Запитів до API на хвилину. Ліміт keyCRM — 20 на ключ, більше ставити немає сенсу. |
| `KEYCRM_LOG_LEVEL` | `info` | Рівень логів у stderr: `debug`, `info`, `warn`, `error`. |
| `KEYCRM_UPLOAD_DIR` | порожньо | Тека, з якої `upload_file` може брати локальні файли. Порожньо — локальні файли вимкнено. |
| `KEYCRM_ALLOW_PRIVATE_URLS` | `false` | Лише для локальних тестів: дозволити завантаження з приватних адрес. |
| `KEYCRM_MCP_HTTP_TOKEN` | — | Bearer-токен для HTTP-транспорту. Обов'язковий з `--http`. |
| `KEYCRM_TEST_API_KEY` | — | Лише для інтеграційних тестів (`-tags integration`, тільки читання). |

Прапорці командного рядка перекривають змінні оточення:

| Прапорець | Опис |
|---|---|
| `--http <адреса>` | Streamable HTTP замість stdio, напр. `127.0.0.1:8080`. |
| `--read-only` | Те саме, що `KEYCRM_READ_ONLY=true`. |
| `--toolsets <список>` | Те саме, що `KEYCRM_TOOLSETS`. |
| `--version` | Показати версію й вийти. |

## Групи інструментів

Чим менше інструментів, тим легше моделі вибрати потрібний і тим менше контексту йде на їхні описи. Вмикайте лише потрібні групи через `KEYCRM_TOOLSETS`.

| Група | Інструменти |
|---|---|
| `reference` | `list_order_statuses`, `list_order_sources`, `list_order_tags`, `list_payment_methods`, `list_expense_types`, `list_delivery_services`, `list_product_statuses`, `list_users`, `list_custom_fields` |
| `orders` | `list_orders`, `get_order`, `create_order`, `update_order`, `import_orders`, `add_order_payment`, `update_order_payment`, `add_order_expense`, `update_order_expense`, `add_order_tag`, `remove_order_tag`, `attach_order_file`, `detach_order_file` |
| `buyers` | `list_buyers`, `find_buyer`, `get_buyer`, `create_buyer`, `update_buyer`, `import_buyers` |
| `companies` | `list_companies`, `get_company`, `create_company`, `update_company` |
| `calls` | `list_calls` |
| `catalog` | `list_products`, `get_product`, `create_product`, `update_product`, `import_products`, `create_offers`, `list_offers`, `update_offers`, `list_categories`, `create_category` |
| `stock` | `get_stock`, `set_stock` |
| `receipts` | `receive_stock`, `revert_receipt` |
| `payments` | `list_external_transactions`, `attach_external_transaction` |
| `files` | `upload_file`, `list_files`, `list_entity_files` |
| `tasks` | `list_tasks`, `get_task`, `create_task`, `update_task`, `import_tasks`, `attach_task_file`, `detach_task_file` |
| `pipelines` | `list_pipelines`, `list_pipeline_statuses`, `list_pipeline_cards`, `get_pipeline_card`, `create_pipeline_card`, `update_pipeline_card`, `add_card_payment`, `update_card_payment`, `attach_card_file`, `detach_card_file` |

- Списки повертають компактну проєкцію й `next_page`; повний об'єкт — через `get_*`.
- `find_buyer` нормалізує телефон: `067 123 45 67`, `+38 (067) 123-45-67` і `380671234567` шукаються як `+380671234567`.
- Кожен інструмент має анотації MCP: `readOnlyHint` для читання, `idempotentHint` для оновлень, `destructiveHint` для від'єднання й видалення.

### Режим лише читання

З `KEYCRM_READ_ONLY=true` або `--read-only` сервер реєструє тільки інструменти читання. Інструментів запису модель просто не бачить, тож змінити дані не може.

### Підтвердження `confirm`

Небезпечні операції виконуються лише з `confirm: true`. Модель має спершу показати вам, що саме зміниться, і передати `confirm` тільки після вашої згоди. Параметра «пропустити підтвердження» немає.

| Інструмент | Коли потрібен `confirm: true` |
|---|---|
| `import_orders`, `import_buyers`, `import_products`, `import_tasks` | завжди |
| `create_order` | завжди |
| `set_stock` | завжди |
| `receive_stock`, `revert_receipt` | при `dry_run: false` |
| `update_offers` | понад 20 позицій за раз |

## Прибуткові накладні й собівартість

`receive_stock` додає кількість на склад і перераховує закупівельну ціну (`purchased_price`) варіанту за середньозваженою:

```
нова собівартість = (Q · C + q · ціна) / (Q + q)
```

де `Q` — загальний залишок, `C` — поточна собівартість, `q` — кількість у накладній. Округлення — до 2 знаків, half-up. Кількості не округлюються: 0.75 кг філаменту лишається 0.75. Якщо залишок був нульовим або від'ємним, собівартість стає ціною з накладної.

**Приклад.** На складі 2 шт по 50 грн, надійшла 1 шт по 59 грн:

```
(2 · 50 + 1 · 59) / (2 + 1) = 159 / 3 = 53
```

Що бачить модель. Спершу попередній перегляд (`dry_run` за замовчуванням `true`), нічого не записано:

```json
{
  "warehouse_id": 1,
  "items": [{ "sku": "SKU-001", "quantity": 1, "unit_cost": 59 }],
  "supplier": "Постачальник",
  "doc_number": "123"
}
```

Відповідь (скорочено):

```json
{
  "status": "preview",
  "lines": [{
    "sku": "SKU-001", "quantity": 1, "unit_cost": 59,
    "qty_before": 2, "warehouse_qty_before": 2, "cost_before": 50,
    "warehouse_qty_after": 3, "cost_after": 53
  }]
}
```

Після вашого «так» модель повторює виклик з `"dry_run": false, "confirm": true`. Сервер перечитує залишки, записує кількість, потім собівартість і звіряє результат.

`revert_receipt` з тією самою накладною відкочує її: 3 шт по 53, відкат 1 шт по 59 → 2 шт по 50.

## Обмеження

- **API keyCRM: 20 запитів на хвилину на ключ**, до 50 записів на сторінку. Сервер має один спільний лімітер: запити стають у чергу й рівномірно розподіляються в межах хвилини, а не падають. На 429 сервер чекає `Retry-After` і повторює запит до 3 разів; GET після 5xx повторюється один раз. Тому великі вибірки займають хвилини, а дві копії сервера з одним ключем ділитимуть ліміт між собою.
- Час в API — UTC. Дати у відповідях приходять у форматі ISO 8601 (`2026-11-18T16:19:58.000000Z`), а не як у прикладах специфікації.
- Для системних статусів, які ви не перейменовували, `name` приходить службовим аліасом англійською (`new`, `manufacturing`). Назв груп статусів API не віддає, лише `group_id`.
- **Прибуткові накладні (`receive_stock`, `revert_receipt`).** keyCRM не має публічного API для документа надходження, тож накладна проводиться двома записами: `PUT /offers/stocks` встановлює нову кількість на складі, `PUT /offers` — нову закупівельну ціну. Перевірено на реальному акаунті: `PUT /offers/stocks` не змінює `purchased_price`, тому порядок записів безпечний. В історії «Рух товарів» keyCRM така накладна з'являється як «Оновлення через API» з кількістю, але без суми, отримувача й менеджера, а не як «Оприбуткування». Постачальник, номер і дата документа в keyCRM не зберігаються — вони лише повертаються в результаті інструмента.
- Якщо між двома записами стався збій, результат має статус `partial` і перелік цін для `update_offers`; накладну повторювати не можна.
- `revert_receipt` рахує собівартість зворотною формулою з даних самої накладної; якщо після надходження були інші рухи, результат наближений.
- `purchased_price` у keyCRM одна на варіант, а не на склад.
- SKU з комою не підтримується: кома — роздільник фільтра keyCRM, тож такий SKU відхиляється з помилкою.

## Безпека

Ключ API дає повний доступ до CRM, а модель може читати недовірений текст: повідомлення клієнтів, описи товарів. Тому сервер безпечний за замовчуванням:

- **Лише читання** одним прапорцем, **`confirm`** для масових і незворотних операцій, накладні — спершу dry-run.
- **Логи** йдуть у stderr і не містять телефонів, email, імен, адрес, приміток, постачальників, ключів і токенів: параметри інструментів маскуються.
- **`upload_file`** приймає лише `http(s)`, перевіряє IP у момент з'єднання (жодних private, loopback, link-local, CGNAT, multicast; DNS rebinding не проходить), перевіряє кожен редирект і обмежує розмір 50 МБ. Ключ API на сторонні адреси не передається. Локальні файли — лише з `KEYCRM_UPLOAD_DIR`.
- **HTTP-транспорт** не стартує без `KEYCRM_MCP_HTTP_TOKEN`; токен порівнюється за сталий час.
- **Без стану:** сервер нічого не зберігає на диску.
- Тримайте ключ у змінних оточення або в менеджері секретів, а не у файлах у git. `.env` у репозиторії ігнорується.

Про вразливість повідомляйте приватно, як описано в [SECURITY.md](SECURITY.md).

## Розробка

```bash
make check      # gofmt, golangci-lint, go vet, тести
make build      # bin/keycrm-mcp
make generate   # перегенерувати клієнт з pkg/keycrm/api.yaml
```

Перевірка релізу локально (потрібні GoReleaser і Docker з buildx):

```bash
goreleaser check
goreleaser release --snapshot --clean
```

## Участь і ліцензія

Код під ліцензією [AGPL-3.0](LICENSE). Внески приймаються після підписання CLA через CLA Assistant; навіщо він і як це працює — у [CONTRIBUTING.md](CONTRIBUTING.md). Журнал змін — [CHANGELOG.md](CHANGELOG.md).

keyCRM — торгова марка її власника. Цей проєкт незалежний і не пов'язаний з keyCRM.
