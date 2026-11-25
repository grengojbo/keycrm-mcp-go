# Журнал змін

Формат — [Keep a Changelog](https://keepachangelog.com/uk/1.1.0/), версіонування — [SemVer](https://semver.org/lang/uk/).

## [Unreleased]

## [0.1.0] - 2026-11-19

Перший публічний реліз.

### Додано

- MCP-сервер `keycrm-mcp` поверх офіційного публічного API keyCRM: 70 інструментів у 12 групах (`reference`, `orders`, `buyers`, `companies`, `calls`, `catalog`, `stock`, `receipts`, `payments`, `files`, `tasks`, `pipelines`). Групи вмикаються через `KEYCRM_TOOLSETS`.
- Транспорти stdio (за замовчуванням) і Streamable HTTP (`--http`) з обов'язковим Bearer-токеном `KEYCRM_MCP_HTTP_TOKEN`.
- Прибуткові накладні `receive_stock` і `revert_receipt`: середньозважена собівартість (half-up до 2 знаків, дробові кількості), попередній перегляд за замовчуванням, звірка після запису, статус `partial` при збої між записами.
- `find_buyer` нормалізує телефон у будь-якому поширеному форматі до `+380…`.
- Режим лише читання (`KEYCRM_READ_ONLY`, `--read-only`) і обов'язковий `confirm: true` для імпорту, `create_order`, `set_stock`, запису накладних і масового `update_offers`.
- Go-клієнт `pkg/keycrm`, згенерований з вендореної специфікації keyCRM: спільний лімітер 20 запитів на хвилину, повтори після 429 і 5xx (лише GET), типізовані помилки з полями 422, посторінкове читання.
- Безпечний `upload_file`: перевірка IP у момент з'єднання, перевірка редиректів, ліміт 50 МБ, локальні файли лише з `KEYCRM_UPLOAD_DIR`.
- Маскування персональних даних у логах.
- README українською й англійською.
- Реліз через GoReleaser: бінарники для linux, darwin і windows (amd64, arm64), Homebrew tap `grengojbo/tap`, образ `ghcr.io/grengojbo/keycrm-mcp-go` на базі distroless, маніфест `server.json`, який `release.yml` публікує в MCP Registry через GitHub OIDC.
- CI (тести, лінтери, збірка під linux/darwin/windows) і перевірки безпеки (CodeQL, govulncheck, dependency-review, zizmor).
- Файли для спільноти: CONTRIBUTING, CLA, SECURITY, CODE_OF_CONDUCT, шаблони issue і PR.

[Unreleased]: https://github.com/grengojbo/keycrm-mcp-go/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/grengojbo/keycrm-mcp-go/releases/tag/v0.1.0
