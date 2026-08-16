# Zephyr

Zephyr — локальный read-only ревьюер изменений кода на Go. Он фиксирует один
неизменяемый Git-снапшот, выбирает подходящие роли, параллельно запускает их через
[Aether](https://github.com/signaturekey/aether), проверяет доказательства в
`evidence gate` и собирает единый Markdown-отчёт.

```text
snapshot -> routing -> parallel reviewers -> evidence gate -> report
```

Zephyr не исправляет код, не меняет исходный репозиторий и не пишет во внешние
системы. Его задача — дать независимое ревью конкретного набора изменений до
коммита, push или pull request.

<a id="navigation"></a>
## Навигация

- [Что решает Zephyr](#what-zephyr-solves)
- [Чем он отличается от обычного AI-review](#how-it-is-different)
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

<a id="what-zephyr-solves"></a>
## Что решает Zephyr

Zephyr поддерживает три практических сценария:

| Сценарий | Что проверяется |
| --- | --- |
| Локальная работа | Staged, unstaged, удалённые, переименованные и non-ignored untracked изменения относительно `HEAD` |
| Один коммит | Изменения коммита относительно первого родителя; root commit сравнивается с пустым Git tree |
| Ветка | Изменения от `merge-base(base, branch)` до выбранной ветки |

Для каждого запуска Zephyr:

1. создаёт отдельный временный клон;
2. фиксирует точный diff и состояние файлов;
3. выбирает все релевантные роли;
4. запускает роли изолированно и параллельно;
5. детерминированно отбрасывает кандидатов без достаточной опоры на diff;
6. передаёт оставшиеся замечания в отдельный `evidence gate`;
7. печатает один отчёт со всеми подтверждёнными замечаниями P0–P3 и ограничениями покрытия.

Zephyr сейчас ревьюит изменения кода. Отдельных plan-only и alignment-режимов в
текущей реализации нет. Спецификацию или бизнес-требования можно приложить как
замороженный контекст к code review.

<a id="how-it-is-different"></a>
## Чем он отличается от обычного AI-review

Zephyr разделяет детерминированную механику и модельное суждение:

| Компонент | Ответственность |
| --- | --- |
| Zephyr CLI | Git-снапшот, routing policy, схемы, precheck, дедупликация и отчёт |
| Aether | Go-клиент к Codex App Server, lifecycle соединения и изолированных threads |
| Semantic router | Выбор только необязательных ролей из закрытого списка |
| Reviewer roles | Поиск проблем в своей узкой области |
| Evidence gate | Проверка уже найденных кандидатов; новые замечания он создавать не может |

Внутри Zephyr нет shell-dispatcher, приватного `CODEX_HOME`, устанавливаемых
agent definitions, compatibility probe, постоянной state machine запуска,
собственного MCP-клиента, базы данных, сервера или web UI. Codex App Server
подключён как обычная Go-зависимость через Aether.

Ключевые свойства:

- один запуск всегда привязан к одному замороженному снапшоту;
- обязательные роли нельзя потерять из-за ответа semantic router;
- повреждённый или неполный routing приводит к консервативному fallback;
- роли не видят вывод друг друга;
- сбой одной роли снижает покрытие, но не стирает результаты остальных;
- неподтверждённые кандидаты не попадают в отчёт как findings.

<a id="quick-start"></a>
## Быстрый старт

### Требования

- Go 1.24 или новее для сборки из исходников;
- системный `git` в `PATH`;
- Codex с поддержкой App Server и действующей пользовательской авторизацией.

### Установка

Установить CLI вместе с пользовательским Codex skill:

```bash
make install
```

Skill устанавливается в `$HOME/.agents/skills/zephyr`. Только CLI можно установить
через `go install ./cmd/zephyr`.

Версия бинарника определяется из текущего checkout через
`git describe --tags --always`: на release tag это имя тега, между тегами — tag с
числом коммитов и SHA, а до первого тега — SHA. Dirty-состояние выводится отдельно.

Или собрать бинарник локально:

```bash
make build
./bin/zephyr version
```

Aether уже указан в `go.mod`; отдельно устанавливать SDK не нужно.

### Первое ревью

Текущие локальные изменения:

```bash
zephyr review
```

Явно указанный репозиторий:

```bash
zephyr review --worktree --repo /path/to/repository
```

Через Codex skill достаточно явно попросить:

```text
Прогони Zephyr по текущим локальным изменениям.
```

Markdown всегда печатается в stdout. При необходимости его можно одновременно
сохранить вместе с JSON:

```bash
zephyr review --output review.md --json-output review.json
```

<a id="maintenance"></a>
## Обновление и удаление

Переустановить CLI и skill из текущего checkout:

```bash
make update
```

`update` — alias на `install`: обе команды всегда устанавливают CLI и skill из одной
версии исходников и сами не изменяют Git checkout. Чтобы сначала получить изменения
из remote:

```bash
git pull --rebase
make update
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
- не требует clean working tree и не различает staged/unstaged как отдельные режимы;
- никогда не изменяет исходный checkout.

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
zephyr review --exclude-role security-auditor
zephyr review --max-parallel 4
zephyr review --config /path/to/config.yaml
```

`--max-parallel` ограничивает только конкурентность. Он не обрезает число выбранных
ролей и не является лимитом покрытия.

Отчёт привязан к SHA, зафиксированным в снапшоте. Финальной проверки drift исходного
worktree после ревью сейчас нет.

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
4. `security-auditor` защищён по умолчанию, но пользователь может явно исключить его.
5. Semantic router обязан принять решение по каждой оставшейся включённой роли.
6. При ошибке, неполном ответе или нарушении схемы все нерешённые роли включаются
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
  --context requirements.json
```

Контекст может содержать требования, спецификацию, Jira issue, Confluence page,
Bitbucket PR metadata или другие подтверждённые источники. Один флаг `--context`
можно указать несколько раз.

CLI Zephyr не содержит Jira-, Confluence-, Bitbucket-, document-provider- или
MCP-клиентов. При явном запуске Zephyr тонкий
[Codex skill](.agents/skills/zephyr/SKILL.md) распознаёт явно указанные в запросе Jira
issues, Confluence pages, Bitbucket pull requests и документы. Если доступна однозначно
read-only MCP-операция, skill читает только указанные объекты, сохраняет каждый в
приватный временный Markdown-файл, передаёт его через отдельный `--context` и удаляет
после запуска даже при ошибке.

Skill не обходит деревья страниц и связанные документы по умолчанию, не читает
комментарии или историю без явного запроса и не использует неоднозначные read/write
MCP tools. Недоступный, устаревший или усечённый источник показывается как ограничение
покрытия. Текст внешнего контекста считается недоверенным evidence, а не инструкцией
для orchestration.

Zephyr и skill не пишут комментарии, статусы или изменения во внешние системы.

<a id="configuration"></a>
## Конфигурация

Встроенный [`configs/default.yaml`](configs/default.yaml) остаётся полным out-of-box
конфигом. Для обычного запуска создавать проектный конфиг не нужно.

Zephyr всегда начинает со встроенного `configs/default.yaml` и накладывает на него
один project overlay:

1. файл из `--config`, если флаг указан;
2. иначе `.zephyr/config.yaml` из замороженного снапшота, если он существует;
3. иначе остаются только встроенные defaults.

Текущая pipeline использует из конфигурации concurrency, enablement ролей,
routing rules, model/effort для semantic router, reviewers и evidence gate, а также
path policies детерминированного precheck. Некоторые совместимые поля встроенного
конфига сохранены для формата конфигурации, но не должны считаться отдельными
runtime-стадиями.

<a id="safety"></a>
## Гарантии read-only и изоляция

### Исходный репозиторий

- Zephyr не выполняет в пользовательском checkout `add`, `commit`, `checkout`,
  `switch`, `reset`, `clean`, `stash`, `merge`, `rebase`, `push` или создание веток;
- все изменения воспроизводятся только внутри нового временного клона;
- submodules не инициализируются;
- снапшот удаляется только после проверки, что это созданный Zephyr temp root;
- `--keep-temp` — единственный штатный способ оставить его после запуска.

### Agent runtime

- на один review создаётся один Aether client;
- semantic router, каждый reviewer и evidence gate получают отдельный fresh thread;
- threads помечены как ephemeral;
- approval policy — `never`, sandbox — read-only;
- рабочая директория агента нейтральная и временная;
- ответы ограничены JSON Schema.

Read-only sandbox защищает от записи, но не является границей конфиденциальности:
reviewer может читать разрешённые supporting files из замороженного снапшота.
Non-ignored untracked файлы входят в worktree-снапшот автоматически. Поэтому секреты
должны быть gitignored или не находиться в передаваемом scope.

<a id="limitations"></a>
## Ограничения

- Zephyr сейчас выполняет implementation review; самостоятельного ревью плана без
  diff и отдельного alignment mode нет.
- Прямого режима «дай PR URL и сам получи provider metadata» нет. Для удалённого
  репозитория используются `--commit` или `--branch ... --base ...`.
- CLI сам не читает MCP и не получает бизнес-контекст: его нужно передать через
  `--context` или явно запустить Codex skill.
- Zephyr не запускает build, lint, unit tests или CI вместо reviewer roles.
- Для реального ревью нужны рабочие Codex App Server и пользовательская авторизация.
- Модельные ответы не обязаны быть byte-identical между запусками; Git scope,
  routing invariants, схемы и агрегация при этом фиксированы кодом.
- Финальной проверки изменения исходного worktree после создания снапшота нет;
  отчёт относится к SHA и содержимому уже созданного снапшота.
- Non-ignored untracked файлы включаются в worktree review без отдельного prompt.
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
| Semantic router завершился ошибкой | Zephyr включит нерешённые роли через fallback и отразит degradation в отчёте |
| Один reviewer завершился ошибкой | Результаты остальных сохранятся, а роль появится в `coverage_limits` |
| Evidence gate завершился ошибкой | Запуск вернёт exit code `1`; кандидаты не станут подтверждёнными findings |
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
