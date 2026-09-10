# Zephyr response format

Present one self-contained Russian Markdown response in chat. Use only Zephyr's
Markdown stdout. Preserve validated claims and coverage limitations; never invent
missing evidence or expose rejected raw candidates as findings.

Use this structure and omit only sections with no items:

1. `## Zephyr` — followed immediately by one plain paragraph starting with a bold
   outcome (`**Нужны исправления:**`, `**Есть замечания:**` or
   `**Подтверждённых проблем не найдено.**`). State exact accepted counts by severity,
   evidence-gate status, and the frozen reviewed scope. Use `Нужны исправления` when
   at least one P0 or P1 exists, `Есть замечания` for P2/P3 only. If the run is
   incomplete or materially coverage-limited, say so in this paragraph. Never claim
   the code is fully correct or unconditionally ready to merge.
2. `### Критические и серьёзные замечания` — render every accepted P0 and P1
   separately as `#### P<severity> · <title>`. Under each heading use exactly the
   compact labelled bullets that evidence supports, in this order: `**Место:**`,
   `**Проблема:**`, and `**Последствие:**`. Put paths and code identifiers in inline
   code. Do not add scenario, invariant, recommendation, source roles, or evidence
   confirmation fields unless they are necessary to preserve a validated claim.
3. `### Остальные замечания` — render every accepted P2 and P3 as one compact bullet:
   `**P<severity> · `<location>` · <title>** — <concrete impact or explanation>.`
   Never hide, collapse into a count, or omit P3 findings.
4. `### Требуется решение человека` — render every `needs-human` item as a question
   with its reason and location when available. Do not present it as a confirmed
   defect.
5. `### Применённые роли` — begin with `Успешно завершены:` and list every successful
   reviewer role inline in backticks, followed by a period. On a separate paragraph,
   state `Evidence gate завершён.` or its actual status. List failed or timed-out
   roles separately with a safe concise reason; do not imply that such a role checked
   the change.
6. `### Покрытие и ограничения` — use bullets for the frozen Git scope, frozen context,
   and every material coverage limit, including unavailable required context and
   failed roles. Distinguish sources that were not required from unavailable sources.

Keep P0/P1 detailed and P2/P3 compact regardless of the total finding count. Every
accepted finding must remain visible in the chat response.

Example chat response (illustrative values only; never copy its facts, paths, roles,
counts, or limitations into a real run):

```markdown
## Zephyr

**Нужны исправления:** подтверждены 1 P1, 1 P2 и 1 P3. Evidence gate завершён. Проверен frozen snapshot текущих изменений. Ревью завершено с ограничениями: `golang-expert` завершился по таймауту, Jira недоступна.

### Остальные замечания

- **Место:** `internal/auth/handler.go:42`
- **Проблема:** после ошибки проверки токена обработчик продолжает выполнение.
- **Последствие:** неавторизованный пользователь может изменить чужие данные.

## Остальные замечания

- **P2 · `internal/client/client.go:81` · Не проверен timeout внешнего запроса** — зависший upstream может удерживать обработчик до общего завершения запроса.
- **P3 · `internal/service/service.go:54` · Избыточная одноразовая обёртка** — дополнительный уровень не меняет поведение и усложняет навигацию по коду.

### Требуется решение человека

- Разрешены ли вызовы без токена для legacy-клиента? В доступных требованиях это исключение не описано.

### Применённые роли

Успешно завершены: `code-reviewer`, `security-auditor`, `qa-expert`, `code-simplifier`.

Не завершилась: `golang-expert` — timeout; Go-специфичное покрытие неполное.

Evidence gate завершён.

### Покрытие и ограничения

- Проверены staged, unstaged и non-ignored untracked изменения из frozen snapshot.
- Заморожен контекст: Jira недоступна, поэтому соответствие требованиям из Jira не подтверждено.
- Confluence и Bitbucket не требовались для этой области проверки.
```

Do not mention or link internal report artifacts unless the user explicitly asks for
them. Do not persist chain-of-thought.
