# Zephyr

Zephyr — локальный read-only ревьюер изменений кода на Go. Он фиксирует один
неизменяемый Git-снапшот, выбирает подходящие роли, параллельно запускает их через
[Aether](https://github.com/signaturekey/aether), проверяет доказательства в
`evidence gate` и собирает единый Markdown-отчёт.

<a id="navigation"></a>
## Навигация

- [Архитектура](#architecture)
- [Быстрый старт](#quick-start)
- [Обновление и удаление](#maintenance)
- [Как проходит ревью](#review-flow)
- [Источники изменений и Git scope](#sources)
- [Роли и routing](#roles)
- [Результат ревью](#result)
- [Внешний контекст и MCP](#external-context)
- [Конфигурация](#configuration)
- [Гарантии read-only и изоляция](#safety)
- [Ограничения](#limitations)
- [Диагностика](#troubleshooting)
- [Разработка](#development)

<a id="architecture"></a>
## Архитектура

Zephyr разделяет детерминированную механику и модельное суждение:

| Компонент | Ответственность |
| --- | --- |
| Zephyr CLI | Git-снапшот, routing policy, схемы, precheck, дедупликация и отчёт |
| Aether | Go-клиент к Codex App Server, lifecycle соединения и изолированных threads |
| Semantic router | Выбор только необязательных ролей из закрытого списка |
| Reviewer roles | Поиск проблем в своей узкой области |
| Evidence gate | Вердикт по кандидатам, прошедшим deterministic precheck |

<a id="quick-start"></a>
## Быстрый старт

### Требования

- Go 1.24 или новее для сборки из исходников;
- системный `git` в `PATH`;
- Codex с поддержкой App Server и действующей пользовательской авторизацией.

### Установка

Установить CLI вместе с пользовательским Codex skill:

```bash
git clone https://github.com/signaturekey/zephyr.git
cd zephyr
make install
```

Чтобы установить конкретный release tag, переключать checkout или клонировать
репозиторий заново не нужно:

```bash
make install VER=v0.1.0
```

В этом режиме Go скачивает указанный tag в module cache, а CLI и skill устанавливаются
из одной версии. `VER` должен быть semantic version вида `vX.Y.Z`; ветки, commit SHA и
`latest` не принимаются.

Skill устанавливается в `$HOME/.agents/skills/zephyr`. Только CLI можно установить
через `go install ./cmd/zephyr`.

При установке из checkout версия бинарника определяется через
`git describe --tags --always`: на release tag это имя тега, между тегами — tag с
числом коммитов и SHA, а до первого тега — SHA. Dirty-состояние выводится отдельно.
При `VER=vX.Y.Z` версией бинарника становится указанный tag.

Или собрать бинарник локально:

```bash
make build
./bin/zephyr version
```

### Первое ревью

Текущие локальные изменения:

```bash
zephyr review
```

Через Codex skill достаточно явно попросить:

```text
Прогони Zephyr по текущим локальным изменениям.
```

<a id="maintenance"></a>
## Обновление и удаление

Переустановить CLI и skill из текущего checkout:

```bash
make update
```

`update` — alias на `install`: обе команды всегда устанавливают CLI и skill из одной
версии исходников и сами не изменяют Git checkout. Можно также обновиться до
конкретного release tag:

```bash
make update VER=v0.1.0
```

Чтобы сначала получить изменения из remote для установки из checkout:

```bash
git pull --rebase
make update
```

Показать последний локальный release tag:

```bash
make tag
```

Создать и отправить новый release tag:

```bash
make tag VER=v0.1.3
```

Удалить CLI и установленный пользовательский skill:

```bash
make uninstall
```

`uninstall` удаляет `zephyr` из `GOBIN` (или `GOPATH/bin`) и skill из
`$HOME/.agents/skills/zephyr`. Исходники, конфиги и локальный `bin/zephyr`, созданный
через `make build`, команда не меняет. После `make install`, `make update` или
`make uninstall` перезапустите Codex, чтобы применить изменения; каждая из этих
команд также напомнит об этом.

<a id="review-flow"></a>
## Как проходит ревью

| Этап | Что происходит |
| --- | --- |
| 1. Snapshot | Zephyr создаёт новый приватный временный клон и фиксирует выбранный Git scope |
| 2. Routing | Детерминированные сигналы защищают обязательные роли, semantic router решает судьбу остальных |
| 3. Review | Каждая выбранная роль получает свой primary diff и запускается в отдельном ephemeral Aether thread |
| 4. Precheck | Zephyr проверяет схему, роль, severity, location и принадлежность замечания разрешённому scope |
| 5. Evidence gate | Отдельный thread принимает решение по точному набору прошедших precheck кандидатов |
| 6. Aggregation | Zephyr проверяет целостность verdicts, дедуплицирует findings и сохраняет provenance ролей |
| 7. Report | Формируются JSON-модель и русский Markdown-отчёт; временный снапшот удаляется |

Evidence gate запускается один раз и только при наличии кандидатов. Он может
принять, отклонить, понизить severity, отметить дубликат или запросить решение
человека. Повысить severity или придумать новое замечание он не может.

Если reviewer завершился с ошибкой, Zephyr продолжает работу и добавляет явное
ограничение покрытия. Если evidence gate не завершился корректно, весь запуск
завершается ошибкой: непроверенные кандидаты не выдаются за подтверждённые.

<a id="sources"></a>
## Источники изменений и Git scope

За один запуск выбирается ровно один источник.

### Worktree

```bash
zephyr review --worktree --repo /path/to/repository
```

Это режим по умолчанию. Zephyr:

- разрешает локальный `HEAD`;
- клонирует его без submodules;
- применяет combined binary diff относительно `HEAD`;
- копирует non-ignored untracked обычные файлы и безопасные symlinks;
- не требует clean working tree и не различает staged/unstaged как отдельные режимы.

### Commit

```bash
zephyr review --commit 0123456789abcdef --repo /path/to/repository
zephyr review --commit 0123456789abcdef --repo https://github.com/org/repository.git
```

Zephyr клонирует локальный или удалённый репозиторий, разрешает выбранный коммит,
делает detached checkout и ревьюит diff относительно первого родителя.

### Branch

```bash
zephyr review --branch feature --base main --repo /path/to/repository
zephyr review --branch feature --base main --repo https://github.com/org/repository.git
```

Zephyr разрешает base и head, вычисляет merge base, делает detached checkout head и
ревьюит диапазон `merge-base..head`.

### Дополнительные параметры scope

```bash
zephyr review --include-role qa-expert
zephyr review --exclude-role code-simplifier
zephyr review --max-parallel 4
zephyr review --config /path/to/config.yaml
```

`--max-parallel` ограничивает только конкурентность. Он не обрезает число выбранных
ролей и не является лимитом покрытия.

<a id="roles"></a>
## Роли и routing

| Роль | Область ревью |
| --- | --- |
| `code-reviewer` | Функциональная корректность, control/data flow, ошибки и достижимые edge cases |
| `architect-reviewer` | Границы модулей, зависимости, совместимость и системные failure modes |
| `golang-expert` | Go semantics, context, errors, concurrency, lifecycle ресурсов |
| `python-expert` | Python runtime, asyncio, exceptions, typing drift и lifecycle ресурсов |
| `typescript-expert` | Type soundness, narrowing, async ordering и runtime-schema drift |
| `react-expert` | Hooks, effects, lifecycle, reconciliation и React ecosystem |
| `frontend-expert` | Browser UI, DOM/events, accessibility, navigation и пользовательские состояния |
| `skill-authoring-expert` | `SKILL.md`, triggers, tool contracts, references, scripts и eval coverage |
| `reliability-expert` | Timeouts, retries, idempotency, backpressure и graceful degradation |
| `messaging-expert` | Producers, consumers, ordering, delivery, retry/DLQ и offsets |
| `infrastructure-expert` | Docker, Kubernetes, Helm, CI/CD, rollout и runtime wiring |
| `storage-expert` | Cache, search, object storage, consistency, TTL и invalidation |
| `security-auditor` | AuthN/AuthZ, injection, secrets, PII и privilege boundaries |
| `sql-expert` | SQL, транзакции, locking, индексы и online migrations |
| `contract-reviewer` | OpenAPI, Proto, schemas, events, DTO и mixed-version compatibility |
| `qa-expert` | Конкретные непокрытые ветки, failure modes и неэффективные assertions |
| `code-simplifier` | Локальная сложность, лишние абстракции и divergence risk в изменённом коде |

### Как выбираются роли

1. `code-reviewer` обязателен для каждого текущего code review.
2. Пути и сильные сигналы в diff защищают профильных экспертов.
3. Роли из `--include-role` становятся защищёнными.
4. Semantic router обязан принять решение по каждой оставшейся включённой роли.
5. При ошибке, неполном ответе или нарушении схемы все нерешённые роли включаются
   консервативным fallback.

Каждая роль запускается не более одного раза. Она получает сфокусированный primary
diff, полный индекс изменённых путей и доступ на чтение к общему замороженному
снапшоту для проверки зависимого кода. Роль не видит исходный путь пользовательского
checkout и результаты других ролей.

<a id="result"></a>
## Результат ревью

### Статус запуска

| Статус | Значение |
| --- | --- |
| `complete` | Все выбранные стадии завершились без потери покрытия |
| `complete-with-limits` | Отчёт валиден, но часть ролей или входных данных была недоступна |

Отсутствие findings не означает автоматически, что проблем нет: сначала нужно
проверить `coverage_limits` и список реально завершившихся ролей.

### Severity

| Уровень | Значение |
| --- | --- |
| P0 | Подтверждённая критическая уязвимость, необратимая потеря данных или гарантированный main-path отказ |
| P1 | Вероятный функциональный дефект, contract break, race/leak или серьёзный production-риск |
| P2 | Конкретный test gap, failure mode, maintenance- или performance-риск |
| P3 | Низкорисковое локальное упрощение или точный вопрос, требующий решения человека |

P0 и P1 не проходят детерминированный precheck без полного evidence bundle.
Markdown показывает все подтверждённые findings P0–P3, вопросы человеку и ограничения
покрытия.

### Выходы

| Выход | Поведение |
| --- | --- |
| stdout | Markdown-отчёт печатается всегда |
| `--output FILE` | Дополнительная копия Markdown с mode `0600` |
| `--json-output FILE` | Машиночитаемый JSON-отчёт версии 2 с mode `0600` |
| `--keep-temp` | Не удалять снапшот и вывести его путь в stderr для диагностики |

JSON содержит scope, routing, выбранные роли, статус evidence gate, findings,
`needs_human`, `coverage_limits` и отклонённые кандидаты.

### Exit codes

| Код | Значение |
| --- | --- |
| `0` | Evidence-gated ревью завершено независимо от числа findings |
| `1` | Операционная ошибка или незавершённый evidence gate |
| `2` | Некорректные CLI-параметры или routing input |

Findings — данные отчёта, а не ошибка процесса.

<a id="external-context"></a>
## Внешний контекст и MCP

CLI принимает заранее замороженные Markdown- или JSON-файлы:

```bash
zephyr review \
  --context issue.md \
  --context requirements.json \
  --coverage-limit "Jira MCP failed: connection timed out"
```

Контекст может содержать требования, спецификацию, Jira issue, Confluence page,
Bitbucket PR metadata или другие подтверждённые источники. Один флаг `--context`
можно указать несколько раз.

Известные проблемы получения контекста передаются повторяемым флагом
`--coverage-limit`. Они попадают в canonical Markdown/JSON report и переводят результат
в `complete-with-limits`, не прерывая само ревью.

CLI Zephyr не содержит Jira-, Confluence-, Bitbucket-, document-provider- или
MCP-клиентов. При каждом явном запуске Zephyr тонкий
[Codex skill](.agents/skills/zephyr/SKILL.md) собирает явно указанные источники и
пытается определить Jira issue и Bitbucket PR по выбранной ветке. Через однозначно
read-only MCP-операции skill читает acceptance criteria и связанные Jira-, Confluence-
и Bitbucket-объекты, которые нужны для понимания требований. Каждый объект сохраняется
в приватный временный Markdown-файл, передаётся через отдельный `--context` и удаляется
после запуска даже при ошибке.

У обхода нет фиксированной глубины или лимита числа объектов. Skill дедуплицирует
источники и останавливается, когда собраны требования и contract-relevant зависимости;
он не обходит побочные ссылки и целые деревья только потому, что они достижимы. Skill
не читает комментарии или историю без необходимости либо явного запроса и не использует
неоднозначные read/write MCP tools. Недоступный, неоднозначный, устаревший или усечённый
источник показывается как ограничение покрытия. Текст внешнего контекста считается
недоверенным evidence, а не инструкцией для orchestration.

<a id="configuration"></a>
## Конфигурация

Zephyr всегда начинает со встроенного [`configs/default.yaml`](configs/default.yaml)
и накладывает на него не более одного project overlay:

1. файл из `--config`, если флаг указан;
2. иначе `.zephyr/config.yaml` из замороженного снапшота, если он существует;
3. иначе остаются только встроенные defaults.

Конфигурация управляет concurrency, enablement ролей, routing rules, model/effort для
semantic router, reviewers и evidence gate, а также path policies детерминированного
precheck.

<a id="safety"></a>
## Гарантии read-only и изоляция

### Исходный репозиторий

- Zephyr не выполняет в пользовательском checkout `add`, `commit`, `checkout`,
  `switch`, `reset`, `clean`, `stash`, `merge`, `rebase`, `push` или создание веток;
- все изменения воспроизводятся только внутри нового временного клона;
- снапшот удаляется только после проверки, что это созданный Zephyr temp root.

### Agent runtime

- на один review создаётся один Aether client;
- semantic router, каждый reviewer и evidence gate получают отдельный fresh thread;
- threads помечены как ephemeral;
- approval policy — `never`, sandbox — read-only;
- рабочая директория агента нейтральная и временная;
- ответы ограничены JSON Schema.

Read-only sandbox защищает от записи, но не является границей конфиденциальности:
reviewer может читать разрешённые supporting files из замороженного снапшота. Секреты
должны быть gitignored или находиться вне передаваемого scope.

<a id="limitations"></a>
## Ограничения

- Zephyr сейчас выполняет implementation review; самостоятельного ревью плана без
  diff и отдельного alignment mode нет.
- Прямого режима «дай PR URL и сам получи provider metadata» нет. Для удалённого
  репозитория используются `--commit` или `--branch ... --base ...`.
- Zephyr не запускает build, lint, unit tests или CI вместо reviewer roles.
- Модельные ответы не обязаны быть byte-identical между запусками; Git scope,
  routing invariants, схемы и агрегация при этом фиксированы кодом.
- Финальной проверки изменения исходного worktree после создания снапшота нет;
  отчёт относится к SHA и содержимому уже созданного снапшота.
- Read-only Git-команды могут учитывать пользовательскую Git-конфигурацию; текущая
  реализация не выполняет отдельный preflight внешних clean/process filters.

<a id="troubleshooting"></a>
## Диагностика

| Симптом | Что проверить |
| --- | --- |
| `zephyr: command not found` | Выполнить `make install` и проверить, что `$(go env GOPATH)/bin` находится в `PATH` |
| Ошибка подключения или авторизации Codex | Проверить локальный Codex, App Server и действующую пользовательскую сессию |
| Нет изменений для ревью | Проверить выбранный source: worktree, commit либо пара `--branch` + `--base` |
| Branch/ref не разрешается | Проверить имя ref и доступность remote; для URL также проверить сетевой доступ и credentials Git |
| Semantic router завершился ошибкой | Проверить routing и `coverage_limits` в отчёте |
| Один reviewer завершился ошибкой | Проверить ошибку роли и `coverage_limits` в отчёте |
| Evidence gate завершился ошибкой | Проверить stderr, доступность runtime и корректность model output |
| Нужен снапшот для разбора | Повторить запуск с `--keep-temp`; путь будет выведен в stderr |

Для проверки доступных аргументов:

```bash
zephyr review --help
zephyr version
```

<a id="development"></a>
## Разработка

Основные команды:

```bash
make fmt
make fmt-check
make test
make race
make vet
make check
```

Реальный smoke test с установленным Codex runtime:

```bash
go run ./cmd/zephyr review --worktree --repo /path/to/repository
```

Обычный test suite не требует Codex authentication: orchestration проверяется через
fake runtime boundary, а snapshot-сценарии — на временных настоящих Git-репозиториях.
`fixtures/` хранит детерминированные test/golden inputs, а `evals/` — отдельные
сценарии оценки качества, не заменяющие автоматические correctness tests.

Правила проекта и актуальные продуктовые границы описаны в [`AGENTS.md`](AGENTS.md).
