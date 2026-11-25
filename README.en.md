# keycrm-mcp-go
MCP server for keyCRM: Claude works with orders, buyers, products, stock and payments. Incoming goods receipts recalculate the weighted-average cost; pipelines can be enabled when needed.

[Українська версія](README.md)

The server connects your [keyCRM](https://keycrm.app) account to any MCP client: Claude Desktop, Claude Code, Cursor and others. It talks only to the official public keyCRM API (`https://openapi.keycrm.app/v1`) and never calls a language model itself: it returns compact data, and the client does the reasoning. One static binary, no database, no state.

Things you can ask Claude:

- "Show today's new orders with the total of each."
- "Find the buyer with phone 067 123 45 67 and their latest orders."
- "How many SKU-001 are left in each warehouse?"
- "Receive 1 pc of SKU-001 at 59 UAH into warehouse 1." You get a preview first; nothing is written until you confirm.

Tool descriptions are in Ukrainian; tool names, parameters and error messages are in English. Models handle both without trouble.

## Installation

### Homebrew (macOS, Linux)

```bash
brew install --cask grengojbo/tap/keycrm-mcp
```

### Docker

```bash
docker pull ghcr.io/grengojbo/keycrm-mcp-go:latest
```

The image is based on `distroless/static:nonroot`: no shell, runs as an unprivileged user, built for `linux/amd64` and `linux/arm64`.

### go install

Requires Go 1.27 or newer.

```bash
go install github.com/grengojbo/keycrm-mcp-go/cmd/keycrm-mcp@latest
```

### Prebuilt binary

Archives for linux, darwin and windows (amd64, arm64) are on the [Releases](https://github.com/grengojbo/keycrm-mcp-go/releases) page. Verify the checksum from `checksums.txt`, unpack, and put `keycrm-mcp` on your `PATH`.

Check:

```bash
keycrm-mcp --version
```

## API key

Create the key in keyCRM: **Settings → General → API**. It grants full access to your CRM, so:

- never paste it into a chat with a model and never commit it to git;
- start with read-only mode (`KEYCRM_READ_ONLY=true`).

In the examples below `<your-keycrm-key>` and `<long-random-token>` are placeholders.

## Connecting

### Claude Desktop (stdio)

Config file: **Settings → Developer → Edit Config** (`claude_desktop_config.json`).

```json
{
  "mcpServers": {
    "keycrm": {
      "command": "keycrm-mcp",
      "env": {
        "KEYCRM_API_KEY": "<your-keycrm-key>",
        "KEYCRM_READ_ONLY": "true"
      }
    }
  }
}
```

If Claude Desktop cannot find the command, use the full path from `which keycrm-mcp`.

With Docker:

```json
{
  "mcpServers": {
    "keycrm": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "-e", "KEYCRM_API_KEY", "ghcr.io/grengojbo/keycrm-mcp-go:latest"],
      "env": { "KEYCRM_API_KEY": "<your-keycrm-key>" }
    }
  }
}
```

### Claude Code (stdio)

```bash
claude mcp add keycrm -e KEYCRM_API_KEY=<your-keycrm-key> -- keycrm-mcp
```

Read-only, selected toolsets only:

```bash
claude mcp add keycrm -e KEYCRM_API_KEY=<your-keycrm-key> -e KEYCRM_READ_ONLY=true -e KEYCRM_TOOLSETS=orders,buyers,stock -- keycrm-mcp
```

### Cursor and other clients (stdio)

Use the same `mcpServers` block as for Claude Desktop in `~/.cursor/mcp.json` or in your client's settings.

### HTTP with a Bearer token

For a team or a remote host there is a Streamable HTTP transport at `/mcp`. Without `KEYCRM_MCP_HTTP_TOKEN` the server refuses to start in this mode.

```bash
export KEYCRM_API_KEY=<your-keycrm-key>
export KEYCRM_MCP_HTTP_TOKEN=<long-random-token>   # e.g. openssl rand -hex 32
keycrm-mcp --http 127.0.0.1:8080
```

With Docker (port published on localhost only):

```bash
docker run --rm -p 127.0.0.1:8080:8080 -e KEYCRM_API_KEY -e KEYCRM_MCP_HTTP_TOKEN ghcr.io/grengojbo/keycrm-mcp-go:latest --http :8080
```

Claude Code:

```bash
claude mcp add --transport http keycrm http://127.0.0.1:8080/mcp --header "Authorization: Bearer <long-random-token>"
```

Claude Desktop's config file only launches local processes, so it reaches the HTTP server through the [`mcp-remote`](https://www.npmjs.com/package/mcp-remote) bridge:

```json
{
  "mcpServers": {
    "keycrm": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "http://127.0.0.1:8080/mcp", "--header", "Authorization: Bearer ${KEYCRM_MCP_HTTP_TOKEN}"],
      "env": { "KEYCRM_MCP_HTTP_TOKEN": "<long-random-token>" }
    }
  }
}
```

The server does not terminate TLS. If it is reachable beyond localhost, put it behind a reverse proxy with HTTPS.

## Configuration

Environment variables (see [`.env.example`](.env.example)):

| Variable | Default | Description |
|---|---|---|
| `KEYCRM_API_KEY` | — | keyCRM API key, required. |
| `KEYCRM_API_URL` | `https://openapi.keycrm.app/v1` | API address. Change it only for tests against a fake server. |
| `KEYCRM_TOOLSETS` | `all` | Comma-separated toolsets, e.g. `orders,buyers,catalog,stock,receipts`. An unknown name stops startup. |
| `KEYCRM_READ_ONLY` | `false` | `true` registers read tools only. |
| `KEYCRM_RATE_LIMIT` | `20` | API requests per minute. keyCRM's limit is 20 per key, so a higher value makes no sense. |
| `KEYCRM_LOG_LEVEL` | `info` | Log level on stderr: `debug`, `info`, `warn`, `error`. |
| `KEYCRM_UPLOAD_DIR` | empty | Directory `upload_file` may read local files from. Empty disables local files. |
| `KEYCRM_ALLOW_PRIVATE_URLS` | `false` | Local testing only: allow downloads from private addresses. |
| `KEYCRM_MCP_HTTP_TOKEN` | — | Bearer token for the HTTP transport. Required with `--http`. |
| `KEYCRM_TEST_API_KEY` | — | Integration tests only (`-tags integration`, read-only). |

Command-line flags override environment variables:

| Flag | Description |
|---|---|
| `--http <addr>` | Streamable HTTP instead of stdio, e.g. `127.0.0.1:8080`. |
| `--read-only` | Same as `KEYCRM_READ_ONLY=true`. |
| `--toolsets <list>` | Same as `KEYCRM_TOOLSETS`. |
| `--version` | Print the version and exit. |

## Toolsets

Fewer tools make it easier for the model to pick the right one and leave more context for your data. Enable only the toolsets you need with `KEYCRM_TOOLSETS`.

| Toolset | Tools |
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

- List tools return a compact projection plus `next_page`; the full object comes from `get_*`.
- `find_buyer` normalises phone numbers: `067 123 45 67`, `+38 (067) 123-45-67` and `380671234567` are all searched as `+380671234567`.
- Every tool carries MCP annotations: `readOnlyHint` for reads, `idempotentHint` for updates, `destructiveHint` for detaching and removing.

### Read-only mode

With `KEYCRM_READ_ONLY=true` or `--read-only` the server registers read tools only. The model never sees write tools, so it cannot change data.

### `confirm`

Risky operations run only with `confirm: true`. The model must first show you what will change and pass `confirm` only after you agree. There is no "skip confirmation" parameter.

| Tool | When `confirm: true` is required |
|---|---|
| `import_orders`, `import_buyers`, `import_products`, `import_tasks` | always |
| `create_order` | always |
| `set_stock` | always |
| `receive_stock`, `revert_receipt` | with `dry_run: false` |
| `update_offers` | more than 20 items at once |

## Incoming goods and cost

`receive_stock` adds quantity to a warehouse and recalculates the variant's purchase price (`purchased_price`) as a weighted average:

```
new cost = (Q · C + q · price) / (Q + q)
```

where `Q` is the total stock, `C` the current cost and `q` the received quantity. Cost is rounded half-up to 2 decimals. Quantities are not rounded: 0.75 kg of filament stays 0.75. If stock was zero or negative, the new cost is the receipt price.

**Example.** 2 pcs in stock at 50 UAH, 1 pc arrives at 59 UAH:

```
(2 · 50 + 1 · 59) / (2 + 1) = 159 / 3 = 53
```

What the model does. First a preview (`dry_run` defaults to `true`), nothing is written:

```json
{
  "warehouse_id": 1,
  "items": [{ "sku": "SKU-001", "quantity": 1, "unit_cost": 59 }],
  "supplier": "Supplier",
  "doc_number": "123"
}
```

Response (shortened):

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

After your "yes" the model repeats the call with `"dry_run": false, "confirm": true`. The server re-reads stock, writes the quantity, then the cost, and verifies the result.

`revert_receipt` with the same receipt rolls it back: 3 pcs at 53, revert 1 pc at 59 → 2 pcs at 50.

## Limitations

- **keyCRM API: 20 requests per minute per key**, up to 50 records per page. The server has one shared limiter: requests queue up and are spread evenly over the minute instead of failing. On 429 the server waits for `Retry-After` and retries up to 3 times; a GET after 5xx is retried once. Large listings therefore take minutes, and two server instances with the same key share the limit.
- API times are UTC. Dates in responses come as ISO 8601 (`2026-11-18T16:19:58.000000Z`), not as in the specification examples.
- For system statuses you have not renamed, `name` is an English service alias (`new`, `manufacturing`). The API does not return status group names, only `group_id`.
- **Incoming goods (`receive_stock`, `revert_receipt`).** keyCRM has no public API for a goods receipt document, so a receipt is applied with two writes: `PUT /offers/stocks` sets the new warehouse quantity and `PUT /offers` sets the new purchase price. Verified on a real account: `PUT /offers/stocks` does not change `purchased_price`, so this order is safe. In keyCRM's stock movement history such a receipt appears as "Update via API" with the quantity but without amount, recipient or manager, not as a goods receipt. Supplier, document number and date are not stored in keyCRM; they are only echoed in the tool result.
- If a failure happens between the two writes, the result has status `partial` and lists the prices for `update_offers`; do not repeat the receipt.
- `revert_receipt` computes cost with the reverse formula from the receipt itself; if there were other movements after the receipt, the result is approximate.
- keyCRM keeps one `purchased_price` per variant, not per warehouse.
- SKUs containing a comma are not supported: the comma is keyCRM's filter separator, so such a SKU is rejected with an error.

## Security

The API key grants full access to the CRM, and the model may read untrusted text such as customer messages or product descriptions. So the server is safe by default:

- **Read-only** with one flag, **`confirm`** for bulk and irreversible operations, receipts start with a dry run.
- **Logs** go to stderr and never contain phones, emails, names, addresses, notes, suppliers, keys or tokens: tool parameters are masked.
- **`upload_file`** accepts only `http(s)`, checks the IP at connect time (no private, loopback, link-local, CGNAT or multicast; DNS rebinding does not get through), re-checks every redirect and caps size at 50 MB. The API key is never sent to third-party hosts. Local files only from `KEYCRM_UPLOAD_DIR`.
- **HTTP transport** refuses to start without `KEYCRM_MCP_HTTP_TOKEN`; the token is compared in constant time.
- **Stateless:** the server stores nothing on disk.
- Keep the key in environment variables or a secrets manager, not in files under git. `.env` is git-ignored in this repository.

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## Development

```bash
make check      # gofmt, golangci-lint, go vet, tests
make build      # bin/keycrm-mcp
make generate   # regenerate the client from pkg/keycrm/api.yaml
```

Check the release locally (requires GoReleaser and Docker with buildx):

```bash
goreleaser check
goreleaser release --snapshot --clean
```

## Contributing and license

Licensed under [AGPL-3.0](LICENSE). Contributions are accepted after signing the CLA via CLA Assistant; why and how is explained in [CONTRIBUTING.md](CONTRIBUTING.md) (in Ukrainian). Changelog: [CHANGELOG.md](CHANGELOG.md).

keyCRM is a trademark of its owner. This project is independent and not affiliated with keyCRM.
