# Zephyr response format

Present one self-contained Russian Markdown response in chat. Use only Zephyr's
Markdown stdout. Preserve validated claims and coverage limitations; never invent
missing evidence or expose rejected raw candidates as findings.

Use this structure and omit only sections with no items:

1. `## TL;DR` — state the outcome and exact accepted counts by severity. Use `Нужны
   исправления` when at least one P0 or P1 exists, `Есть замечания` for P2/P3 only, and
   `Подтверждённых проблем не найдено` when there are no accepted findings. If the run
   is incomplete or materially coverage-limited, say so in the same paragraph. Never
   claim the code is fully correct or unconditionally ready to merge.
2. `## Критические и серьёзные замечания` — render every accepted P0 and P1
   separately. Include severity and title, location, concrete problem, reachable
   execution or data path, violated invariant or requirement, observable impact,
   focused recommendation, source roles, and evidence-gate confirmation. Preserve the
   validated claim; do not embellish or invent missing evidence.
3. `## Остальные замечания` — render every accepted P2 and P3 as its own compact
   bullet. Each bullet contains severity, location, title or problem, and concrete
   impact. Never hide, collapse into a count, or omit P3 findings.
4. `## Требуется решение человека` — render every `needs-human` item as a question
   with its reason and location when available. Do not present it as a confirmed
   defect.
5. `## Применённые роли` — list every selected reviewer role. Separate roles that
   produced a validated result from roles that failed or timed out, with a safe concise
   reason for each failure. Briefly name the successfully applied role scope from the
   report; do not imply that a selected but failed role checked the change. State
   whether the evidence gate completed.
6. `## Покрытие и ограничения` — state the reviewed frozen Git scope and every
   material coverage limit, including unavailable required context and failed roles.
   Distinguish sources that were not required from unavailable sources.

Keep P0/P1 detailed and P2/P3 compact regardless of the total finding count. Every
accepted finding must remain visible in the chat response.

Example chat response (illustrative values only; never copy its facts, paths, roles,
counts, or limitations into a real run):

```markdown
## TL;DR

**Нужны исправления:** подтверждены 1 P1, 1 P2 и 1 P3. Ревью завершено с ограничениями: `golang-expert` завершился по таймауту, Jira недоступна.

## Критические и серьёзные замечания

### P1 · Возможен обход проверки авторизации

- **Место:** `internal/auth/handler.go:42`
- **Проблема:** после ошибки проверки токена обработчик продолжает выполнение.
- **Сценарий:** клиент передаёт некорректный токен → `ParseToken` возвращает ошибку → обработчик выполняет защищённую операцию.
- **Нарушенный инвариант:** защищённые операции доступны только аутентифицированным пользователям.
- **Последствие:** неавторизованный пользователь может изменить чужие данные.
- **Что изменить:** завершать запрос при ошибке проверки токена и возвращать `401 Unauthorized`.
- **Подтверждение:** `code-reviewer`, `security-auditor`; evidence gate принял замечание.

## Остальные замечания

- **P2 · `internal/client/client.go:81` · Не проверен timeout внешнего запроса** — зависший upstream может удерживать обработчик до общего завершения запроса.
- **P3 · `internal/service/service.go:54` · Избыточная одноразовая обёртка** — дополнительный уровень не меняет поведение и усложняет навигацию по коду.

## Требуется решение человека

- Разрешены ли вызовы без токена для legacy-клиента? В доступных требованиях это исключение не описано.

## Применённые роли

Успешно:

- `code-reviewer` — функциональная корректность и обработка ошибок.
- `security-auditor` — аутентификация, авторизация и разделение данных.
- `qa-expert` — тесты изменённого поведения и негативных сценариев.
- `code-simplifier` — локальная сложность изменённого кода.

Не завершилась:

- `golang-expert` — timeout; Go-специфичное покрытие неполное.

Evidence gate завершён.

## Покрытие и ограничения

- Проверены staged, unstaged и non-ignored untracked изменения из frozen snapshot.
- Jira недоступна, поэтому соответствие требованиям из Jira не подтверждено.
- Confluence и Bitbucket не требовались для этой области проверки.
```

Do not mention or link internal report artifacts unless the user explicitly asks for
them. Do not persist chain-of-thought.
