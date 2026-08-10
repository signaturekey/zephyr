# Zephyr

Zephyr — локальный read-only ревьювер инженерных планов и изменений кода.
Он фиксирует проверяемую версию проекта, запускает независимых профильных
ревьюеров и оставляет в итоговом отчёте только замечания с проверяемыми
доказательствами.

Zephyr работает через Codex, Claude Code или OpenCode, не вызывает LLM API
напрямую и не изменяет проверяемый репозиторий.

<a id="navigation"></a>
## Навигация

- [Что решает Zephyr](#overview)
- [Чем он отличается от обычного AI-review](#difference)
- [Быстрый старт](#quick-start)
- [Обновление и удаление](#maintenance)
- [Как проходит ревью](#workflow)
- [Режимы и Git scope](#modes)
- [Роли ревьюеров](#roles)
- [Результат](#result)
- [Внешний контекст](#external-context)
- [Конфигурация](#configuration)
- [Гарантии read-only](#safety)
- [Ограничения](#limitations)
- [Ручной CLI-протокол](#manual-cli)
- [Решение проблем](#troubleshooting)
- [Разработка](#development)

<a id="overview"></a>
## Что решает Zephyr

Обычный AI-review часто зависит от состояния конкретного диалога. Модель может
прочитать уже изменившееся рабочее дерево, потерять часть требований, смешать
факты с предположениями или выдать непроверенное замечание уверенным тоном.

Zephyr превращает ревью в воспроизводимый процесс:

- фиксирует один immutable snapshot изменений и требований;
- детерминированно выбирает релевантные reviewer-роли;
- изолирует роли друг от друга;
- проверяет формат, scope и доказательства каждого замечания;
- явно показывает непроверенные области и упавшие стадии;
- сохраняет машинный `review.json` и короткий `review.md`.

Zephyr подходит для четырёх основных сценариев:

- проверить план до начала реализации;
- проверить staged, unstaged или committed изменения;
- сверить реализацию с планом и доступными требованиями;
- проверить ветку относительно базовой ветки.

<a id="difference"></a>
## Чем он отличается от обычного AI-review

Zephyr состоит из двух частей:

| Компонент | Ответственность |
|---|---|
| Go CLI | Git snapshot, review packet, protected routing policy, schema validation, fallback, evidence precheck, дедупликация и отчёты |
| Harness-пакет | Изолированный semantic routing, запуск reviewer-моделей и доступ к разрешённому внешнему контексту |

Одна изолированная модель классифицирует только необязательные роли по смыслу frozen packet. Детерминированное Go-ядро не позволяет ей удалить обязательные, явно запрошенные или подтверждённые changed paths роли, проверяет полный JSON-ответ и при сбое включает все нерешённые роли. Финальный отчёт модель напрямую не формирует.

Каждый ревьюер получает один и тот же frozen packet, но проверяет только свою
область. Например, `golang-expert` отвечает за Go-семантику, а
`security-auditor` — за конкретные нарушения границ безопасности.

После ревьюеров запускается отдельный `evidence-gate`. Он не ищет новые ошибки
и не может повысить severity. Его задача — принять, отклонить, понизить или
объединить уже найденные замечания.

Это даёт три практических свойства:

1. Результат всегда относится к конкретному snapshot.
2. Ноль находок не скрывает неполное покрытие.
3. P0/P1 нельзя получить без полного evidence bundle.

<a id="quick-start"></a>
## Быстрый старт

### Требования

- system Git в `PATH`;
- Go 1.24+ и `make` для установки из исходников;
- Codex, Claude Code или OpenCode для запуска настоящего AI-review.

### Установка для Codex

```bash
git clone https://github.com/signaturekey/zephyr.git
cd zephyr
make install-codex
```

После установки откройте новую сессию Codex, чтобы она увидела skill и
reviewer-роли.

Для других harness используйте одну из команд:

```bash
make install-claude
make install-opencode
make install-all
```

Проверить CLI:

```bash
zephyr version
zephyr --help
```

<a id="maintenance"></a>
## Обновление и удаление

Обновить CLI и нужный harness:

```bash
make update-codex
make update-claude
make update-opencode
make update-all
```

Удалить Zephyr:

```bash
make uninstall        # CLI и все harness-пакеты
make uninstall-skill  # только skills и reviewer definitions
make uninstall-cli    # только binary
```

Installer и updater не перезаписывают отличающиеся пользовательские файлы без
проверки. После установки, обновления или удаления skill откройте новую сессию
выбранного harness.

### Первое ревью

Основной UX — запрос на естественном языке:

```text
$zephyr Проверь staged и unstaged изменения в текущем Go-репозитории.

$zephyr Проверь только staged изменения перед commit.

$zephyr Проверь REVIEW_SPEC.md без Git diff.

$zephyr Сверь текущую реализацию с REVIEW_SPEC.md и требованиями PROJ-123.

$zephyr Проверь текущую ветку относительно main.
```

По умолчанию формулировка «проверь локальные изменения» означает
`working-tree`: staged и unstaged изменения относительно `HEAD`.

После завершения harness показывает:

- краткий итог;
- подтверждённые findings;
- coverage limits и упавшие роли;
- пути к `review.md` и `review.json`.

<a id="workflow"></a>
## Как проходит ревью

| Этап | Что происходит |
|---|---|
| 1. Init | Определяются mode, Git scope и входной план |
| 2. Collect | Создаётся read-only Git snapshot |
| 3. Context | Фиксируются доступные и недоступные внешние источники |
| 4. Route | Go-ядро выбирает релевантные роли |
| 5. Review | Изолированные reviewer-процессы возвращают JSON candidates |
| 6. Precheck | Проверяются schema, scope, locations и evidence |
| 7. Evidence gate | Каждый candidate получает ровно один verdict |
| 8. Report | Создаются `review.json` и `review.md` |

Все reviewer-роли работают с одной packet identity. Они не читают live-tree,
не вызывают Git и не видят ответы друг друга.

Если одна роль падает, результаты остальных сохраняются. Если evidence-gate не
завершился, run получает статус `incomplete`, а candidates не выдаются за
подтверждённые findings.

<a id="modes"></a>
## Режимы и Git scope

Mode определяет смысл проверки, а source — набор Git-данных в snapshot.

### Режимы

| Mode | Что проверяется |
|---|---|
| `plan` | План или спецификация без обязательного Git diff |
| `implementation` | Изменения кода без обязательного плана |
| `alignment` | Соответствие реализации плану и требованиям |
| `auto` | Режим определяется по доступным входам |

`auto` разрешается так:

- только план → `plan`;
- только изменения → `implementation`;
- план и изменения → `alignment`;
- нет ни плана, ни изменений → input error.

### Источники Git-изменений

| Source | Содержимое snapshot |
|---|---|
| `working-tree` | Staged и unstaged изменения относительно `HEAD` |
| `staged` | Только index относительно `HEAD` |
| `branch` | Изменения от merge-base с `--base` до текущего working tree |
| `commit-range` | Явный диапазон `A..B` или `A...B` |
| `plan-only` | План без Git diff |

Примеры низкоуровневого `init`:

```bash
zephyr init --repo . --mode plan --source plan-only --plan REVIEW_SPEC.md
zephyr init --repo . --mode implementation --source working-tree
zephyr init --repo . --mode implementation --source staged
zephyr init --repo . --mode implementation --base main
zephyr init --repo . --mode implementation --range main..HEAD
zephyr init --repo . --mode alignment --plan REVIEW_SPEC.md
```

Generated, vendor и binary changes сохраняются в metadata, но их содержимое по
умолчанию не попадает в packet. Untracked content читается только с явным
`--include-untracked` и проходит ограничение размера, path filtering и secret
filtering.

<a id="roles"></a>
## Роли ревьюеров

Hybrid routing защищает роли по mode, user override и changed paths, а остальные классифицирует по смыслу immutable packet. Для implementation/alignment `security-auditor` также защищён от решений, управляемых содержимым недоверенного diff. При сбое semantic router применяется консервативный fallback.
`max_parallel_reviewers` ограничивает только одновременный запуск, а не общее
покрытие.

| Роль | Область проверки |
|---|---|
| `code-reviewer` | Функциональная корректность, control/data flow и error handling |
| `architect-reviewer` | Границы компонентов, зависимости, rollout и failure modes |
| `golang-expert` | Go API, context, errors, concurrency и lifetime ресурсов |
| `typescript-expert` | Type soundness, nullability, async semantics и runtime drift |
| `frontend-expert` | React lifecycle, UI-состояния, accessibility и browser behavior |
| `skill-authoring-expert` | `SKILL.md`, references, scripts, templates и evals |
| `reliability-expert` | Timeout, retry, idempotency, backpressure и degradation |
| `messaging-expert` | Delivery, ordering, acknowledgements, retry и DLQ |
| `infrastructure-expert` | Docker, Kubernetes, Helm и CI/CD |
| `storage-expert` | Cache, search index, object storage, TTL и invalidation |
| `security-auditor` | AuthN/AuthZ, injection, secrets, PII и privilege boundaries |
| `sql-expert` | SQL, transactions, locks, indexes и migration safety |
| `contract-reviewer` | OpenAPI, Proto, JSON Schema и compatibility |
| `qa-expert` | Конкретные непокрытые branches и failure cases |
| `code-simplifier` | Локальное доказуемое переусложнение; только P2/P3 |

`evidence-gate` не считается reviewer-ролью и запускается один раз после
precheck всех candidates.

<a id="result"></a>
## Результат

### Статусы

| Run status | Значение |
|---|---|
| `created` | Run создан, работа ещё не началась |
| `running` | Выполнена часть стадий |
| `complete` | Отчёт успешно создан |
| `incomplete` | Критичная стадия, например evidence-gate, не завершилась |
| `failed` | Детерминированная стадия остановилась с ошибкой |

Финальный `review.json` имеет отдельный status:

- `complete` — известных пробелов покрытия нет;
- `complete-with-limits` — часть scope не проверена или snapshot стал stale.

### Severity

| Severity | Значение |
|---|---|
| P0 | Подтверждённая критическая уязвимость, необратимая потеря данных или гарантированный rollout blocker |
| P1 | Вероятный функциональный дефект, contract break или серьёзный production/security risk |
| P2 | Конкретный test gap, failure mode или доказуемый maintainability/performance risk |
| P3 | Низкорисковое локальное улучшение или точечный вопрос человеку |

P0/P1 требуют location, минимальный фрагмент evidence, достижимый execution или
data path, нарушенный invariant, observable impact и проверенное
counterevidence. Уверенный тон модели доказательством не считается.

### Основные артефакты

| Файл | Назначение |
|---|---|
| `manifest.json` | Scope, lifecycle и coverage limits |
| `git/` | Git metadata, status и immutable diff |
| `packet/review-packet.json` | Frozen input всех reviewer-ов |
| `routing-request.json` | Protected roles, optional candidates и frozen evidence provenance |
| `routing.json` | Выбранные и исключённые роли с причинами |
| `candidates/` | Валидированные ответы reviewer-ролей |
| `evidence/` | Precheck и verdicts evidence-gate |
| `review.json` | Полный машинный результат |
| `review.md` | Короткий человекочитаемый отчёт |
| `trace.json` | Безопасный структурированный trace стадий |

Run directory хранится вне проверяемого репозитория:

```text
${XDG_CACHE_HOME:-$HOME/.cache}/zephyr/runs/<run-id>/
```

Run artifacts могут содержать diff и рабочий контекст. Не публикуйте весь
каталог без отдельной проверки.

<a id="external-context"></a>
## Внешний контекст

Harness может до начала routing сохранить необходимые требования из трекера
задач, базы знаний или сервиса код-хостинга. Go-ядро не знает credentials и не
выполняет сетевые запросы.

В текущей версии CLI поддерживает три protocol source ID:

- `jira` — задачи и acceptance criteria;
- `confluence` — страницы документации;
- `bitbucket` — PR metadata и review comments.

Это имена текущего протокола, а не ограничение архитектуры конкретными
провайдерами. Нативные source ID для GitHub или GitLab потребуют отдельного
изменения CLI и schema; сейчас README не заявляет такую поддержку.

Перед `route` каждый из трёх источников должен получить статус:

- `available`;
- `unavailable` с причиной;
- `not-required` с причиной.

Недоступный обязательный источник становится coverage limit. Zephyr не заявляет
alignment с требованиями, которых не было в frozen packet.

<a id="configuration"></a>
## Конфигурация

Проект может дополнить встроенные defaults файлом `.zephyr/config.yaml`:

```yaml
version: 1
profile: thorough
language: auto

limits:
  max_parallel_reviewers: 4
  max_roles_standard: 15
  max_roles_thorough: 15
  max_final_findings: 10

roles:
  code-simplifier:
    enabled: false

routing:
  - when:
      paths: ["db/**", "**/*.sql"]
    add_roles: ["sql-expert"]

restricted_paths:
  - "third_party/**"

redaction:
  enabled: true
  deny_patterns:
    - "private/**"
```

`language` принимает `auto`, `go`, `typescript` или `markdown`. Неизвестные
поля, роли, globs и некорректные limits останавливают run до запуска моделей.

Списки routing и path policies дополняют defaults. Scalar values и отдельные
limits переопределяют их.

<a id="safety"></a>
## Гарантии read-only

Zephyr не должен:

- изменять проверяемый source или запускать auto-fix;
- выполнять Git write operations: add, commit, checkout, reset, clean, stash,
  merge, rebase или push;
- создавать PR и review comments;
- писать во внешние трекеры, базы знаний или сервисы код-хостинга;
- читать untracked content без явного согласия;
- передавать `.env`, credentials, private keys и известные token formats;
- выдавать непроверенный candidate за confirmed finding.

Git запускается через argv без shell interpolation, с timeout и read-only
allowlist. Перед worktree-aware операциями Zephyr проверяет Git filters, потому
что даже `git status` или `git diff` могут запустить внешний process.

Reviewer получает только role prompt, immutable packet и output schema. Ему
недоступны live filesystem, Git, shell, MCP, web, memory и другие reviewers.

<a id="limitations"></a>
## Ограничения

- Zephyr находится в активной разработке; перед публикацией результата сверяйте
  текущие test и validation outputs.
- Codex и OpenCode используют отдельные process boundaries. Полная изоляция
  Claude Code пока не подтверждена end-to-end.
- OS-level недоступность repository root для reviewer-а не заявляется как
  доказанная на всех harness.
- Static harness tests не доказывают реальный MCP discovery и model dispatch.
- Zephyr не запускает build и tests проверяемого проекта.
- Отсутствие findings не доказывает корректность, особенно при coverage limits.

<a id="manual-cli"></a>
## Ручной CLI-протокол

Обычному пользователю этот раздел не нужен: skill оркестрирует команды
автоматически. Ручной lifecycle полезен при разработке harness и диагностике.

```bash
umask 077
ZEPHYR_INPUT_DIR=$(mktemp -d)

zephyr init --repo . --mode alignment --source working-tree --plan REVIEW_SPEC.md
zephyr collect --run RUN_ID

zephyr context capability --run RUN_ID --source jira --status not-required \
  --reason "no issue referenced"
zephyr context capability --run RUN_ID --source confluence --status not-required \
  --reason "no documentation referenced"
zephyr context capability --run RUN_ID --source bitbucket --status not-required \
  --reason "no PR referenced"

zephyr route --run RUN_ID
zephyr fallback-routing --run RUN_ID \
  --reason "manual CLI example without semantic model dispatch"

zephyr validate-candidates \
  --run RUN_ID \
  --role golang-expert \
  --input "$ZEPHYR_INPUT_DIR/golang-expert-candidates.json"

zephyr validate-verdicts \
  --run RUN_ID \
  --input "$ZEPHYR_INPUT_DIR/evidence-verdicts.json"

zephyr aggregate --run RUN_ID
zephyr render --run RUN_ID
zephyr inspect --run RUN_ID
```

После работы удалите временную директорию, предварительно проверив путь,
возвращённый `mktemp -d`.

Точные команды и flags смотрите через:

```bash
zephyr --help
zephyr <command> --help
```

<a id="troubleshooting"></a>
## Решение проблем

| Симптом | Что проверить |
|---|---|
| `zephyr: command not found` | Выполните `make install-cli` или используйте `./bin/zephyr` после `make build` |
| Нет reviewable diff | Проверьте mode и source; untracked content не включается автоматически |
| `capability preflight incomplete` | Запишите статус `jira`, `confluence` и `bitbucket` |
| `repository changed` | Начните новый run: старый snapshot не обновляется |
| Найден `filter.*.clean/process` | Используйте безопасный `staged`, `commit-range` или `plan-only`, если scope это допускает |
| Reviewer завершился ошибкой | Проверьте coverage limits и безопасный fingerprint ошибки в `trace.json` |
| Evidence-gate завершился ошибкой | Run остаётся `incomplete`; candidates не являются findings |
| P3 нет в Markdown | Используйте `render --include-p3`; полный результат остаётся в `review.json` |

<a id="development"></a>
## Разработка

Основная проверка:

```bash
make check
go test -race ./... -count=1
go mod verify
```

Отдельные цели:

```bash
make fmt-check
make test
make vet
make test-golden
make test-evals
make validate-harnesses
```

После изменения shared prompts, schemas или harness definitions:

```bash
sh harnesses/sync-discovery.sh
make validate-harnesses
```

Полные продуктовые, архитектурные и protocol-инварианты находятся в
[AGENTS.md](AGENTS.md).
