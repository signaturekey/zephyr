# Zephyr

Zephyr — локальный evidence-based ревьювер инженерных планов, Go/TypeScript-кода
и Markdown skills до commit или pull request. Он фиксирует точный scope проверки, раздаёт один и
тот же неизменяемый контекст нескольким узкоспециализированным reviewer-ролям,
проверяет доказательства и собирает единый отчёт с приоритетами P0–P3.

Zephyr не исправляет код, не заменяет тесты или CI и не публикует результаты во
внешние системы. Его задача — найти доказуемые проблемы и явно показать, какая
часть изменений или требований осталась непроверенной.

## Зачем нужен Zephyr и почему это не просто skill

AI умеет быстро прочитать diff, но сам по себе ответ модели ещё не является
надёжным инженерным ревью. В длинном диалоге модель может потерять часть
контекста, смешать факты с предположениями или проверить уже изменившееся
рабочее дерево. Если подключить несколько агентов без общей координации, они
могут получить разные версии изменений, продублировать замечания или оставить
часть проверки без владельца.

В результате каждый вывод приходится повторно проверять вручную, а из отчёта
непонятно, что именно было покрыто и насколько результату можно доверять.

Zephyr решает эту проблему как локальный оркестратор ревью. Он не просит одну
модель «посмотреть код целиком», а строит воспроизводимый процесс: фиксирует
область проверки, собирает требования, выбирает специализированные роли,
запускает их на одном immutable-контексте и отдельно проверяет доказательность
каждого результата.

Главная идея Zephyr: качество ревью определяется не только мощностью модели и
величиной thinking effort. Не менее важны декомпозиция задачи, узкий scope каждой
роли и изоляция контекста. Одна сильная модель с общим review-skill должна
одновременно помнить бизнес-логику, Go или TypeScript semantics, безопасность,
контракты, SQL, инфраструктуру и тесты. Чем шире такой контекст, тем легче
пропустить локальный дефект, смешать зоны ответственности или потратить reasoning
на нерелевантную часть diff.

Оркестратор вместо этого запускает несколько независимых reviewer-ов. Каждый из
них получает одинаковый frozen packet, но проверяет только свою область и не
видит рассуждения других агентов. Например, `security-auditor` не отвлекается на
стиль тестов, `golang-expert` — на бизнес-требования, а `qa-expert` ищет
конкретные непокрытые ветки, не дублируя функциональное ревью. Роли могут работать
параллельно, потому что не зависят друг от друга, а их findings объединяются уже
после завершения проверки.

Этим объясняется, почему оркестратор с узконаправленными ролями может дать более
полезное ревью даже на менее мощной модели, чем более сильная модель с максимальным
thinking effort и одним универсальным skill. Например, сравнение одиночного
review-skill с многоролевым процессом нельзя сводить только к моделям: существенную
часть качества даёт orchestration-схема с
декомпозицией и специализированными агентами. Zephyr переносит этот принцип в
открытый локальный инструмент, где можно выбирать harness, доступный inference и
собственный набор ролей. Это архитектурная мотивация проекта, а не утверждение о
гарантированном превосходстве одной модели над другой без отдельного benchmark.

### Почему недостаточно обычного skill

Обычный skill — это прежде всего текстовая инструкция для модели. Он хорошо
задаёт поведение агента, но сам по себе не гарантирует:

- что все reviewer-ы получили один и тот же diff и requirements context;
- что ветка не изменилась во время длительного ревью;
- что нужные роли действительно были запущены;
- что ответ каждой роли соответствует общей JSON Schema;
- что одинаковые замечания объединены, а недоказанные — отфильтрованы;
- что сбой роли будет показан как ограничение покрытия, а не превратится в
  ложный вывод «проблем не найдено»;
- что результат можно повторно открыть, сравнить и использовать вне текущего
  чата.

Поэтому Zephyr не хранит весь процесс в одном промпте. Skill остаётся удобной
точкой входа для пользователя, а критичные правила выполняются и проверяются
детерминированным CLI.

### Зачем нужен CLI

Zephyr CLI отвечает за механическую часть, которую не следует оставлять на
усмотрение модели:

- фиксирует Git scope и создаёт immutable snapshot;
- собирает единый review packet и его provenance;
- детерминированно выбирает роли по изменённым файлам и сигналам;
- валидирует candidate findings и verdicts по версионированным JSON Schema;
- проверяет locations и evidence до финального отчёта;
- дедуплицирует результаты и сохраняет `review.md`, `review.json`, routing и
  trace-артефакты;
- отмечает stale snapshot, упавшие роли и другие ограничения покрытия.

Установка поэтому состоит из двух частей:

| Компонент | Зачем нужен |
|---|---|
| Zephyr CLI | Фиксирует Git scope, создаёт immutable packet, выбирает роли, валидирует JSON, собирает findings и сохраняет `review.md`/`review.json` |
| Harness-пакет | Подключает Zephyr к Codex, Claude Code или OpenCode: содержит skill, определения reviewer-ролей и безопасный dispatcher |

Go CLI сам не вызывает LLM и не требует отдельного API key. Модели запускает уже
выбранный пользователем Codex, Claude Code или OpenCode. Благодаря этому Zephyr
не привязан к одному inference provider или единственному harness-у: команды
могут использовать доступную рабочую или локальную конфигурацию и
добавлять собственные reviewer-роли.

### Зачем нужен оркестратор и несколько ролей

Оркестратор нужен, чтобы одно ревью выполнялось как управляемый процесс:

1. Он замораживает проверяемую версию изменений, поэтому последующие правки не
   меняют уже запущенное ревью.
2. Он выбирает несколько специализированных ролей и передаёт им один и тот же
   immutable-контекст.
3. Он не позволяет ролям незаметно читать разные версии репозитория или ответы
   друг друга.
4. Он пропускает кандидатов через schema validation, deterministic precheck и
   evidence gate — фильтр против AI-галлюцинаций и шумных «может быть стоит
   подумать».
5. Он явно фиксирует упавшие роли и ограничения покрытия, поэтому ноль findings
   нельзя выдать за доказательство отсутствия проблем.

Разделение на роли даёт не просто больше ответов, а явное покрытие разных зон:
Go semantics проверяет `golang-expert`, контракты — `contract-reviewer`,
безопасность — `security-auditor`, тестовые пробелы — `qa-expert`. Routing
подключает только релевантные роли, но ограничение параллелизма не уменьшает их
общее количество.

У каждой роли закрытый scope и отдельный изолированный контекст. Это защищает
ревью сразу от двух проблем: контекст одного эксперта не размывается данными из
чужой области, а вывод одного агента не становится неподтверждённой отправной
точкой для остальных. Multi-agent здесь нужен не ради количества агентов, а ради
независимого специализированного анализа одного и того же snapshot.

После reviewer-ов запускается evidence gate — отдельный фильтр против
AI-галлюцинаций и шумных «может быть стоит подумать». Он не ищет новые ошибки и
не может повысить severity. Gate проверяет уже найденные candidates и может
подтвердить, отклонить, понизить или объединить их.

### Что получает команда

| Ценность | Что даёт Zephyr |
|---|---|
| Воспроизводимость | Ревью навсегда связано с конкретным snapshot, SHA и fingerprint рабочего дерева |
| Проверяемость | У каждого P0–P3 есть location, execution path, нарушенный invariant, impact и рекомендация |
| Меньше AI-шума | Schema validation, deterministic precheck и evidence gate отбрасывают неподтверждённые выводы |
| Прозрачное покрытие | В отчёте видны выбранные и упавшие роли, недоступный контекст и stale-status |
| Переносимость | Один review protocol работает через Codex, Claude Code и OpenCode без прямого вызова provider API |
| Расширяемость | Команда может добавлять роли под свой стек, инфраструктуру и внутренние инженерные требования |

Иными словами, skill запускает Zephyr, CLI обеспечивает воспроизводимость и
машинные гарантии, а оркестратор управляет всем multi-agent review от snapshot
до evidence-gated отчёта. Zephyr не заменяет инженера, тесты или обязательное PR
review — он делает предварительную AI-проверку менее случайной, более прозрачной
и пригодной для инженерного процесса.

Практические сценарии:

- проверить документ требований до начала реализации;
- проверить staged и unstaged изменения перед commit;
- сверить код с планом, задачей, документацией и явно запрошенным PR-контекстом
  из сервиса код-хостинга (GitHub, GitLab или Bitbucket);
- проверить ветку относительно `main` или фиксированный диапазон commit-ов;
- получить независимые проверки Go, TypeScript/frontend, reliability, messaging,
  infrastructure, storage, SQL, контрактов, security и тестов без
  автоматического изменения репозитория.

## Текущий статус

| Компонент | Статус |
|---|---|
| Детерминированный Go core | Реализован; unit, race, golden и integration-тесты проходят локально |
| CLI и форматы артефактов | Реализованы и версионированы |
| Codex/Claude/OpenCode skills и agent definitions | Реализованы; структура и checksums валидируются статически |
| Codex reviewer dispatch | Реализован отдельными `codex exec`-процессами; автоматический smoke-тест проверяет точную передачу пакета больше 200 КБ, reviewer и evidence-gate проверены live на синтетическом fixture |
| Claude Code reviewer dispatch | Пакет готов, но host-level isolation и полный сценарий ещё не подтверждены end-to-end |
| OpenCode reviewer dispatch | Реализован отдельными `opencode run`-процессами; автоматический smoke-тест проверяет изолированный config и точную передачу packet больше 200 КБ, reviewer и evidence-gate проверены live на синтетическом fixture |
| Внешний контекст | Задачи, документация и сервисы код-хостинга (например, GitHub, GitLab или Bitbucket) подключаются через доступный harness-у read-only MCP; обязательный capability-preflight фиксирует статус каждого источника до routing |

Локальный core и интеграционные пакеты готовы. Codex transport больше не зависит
от лимита размера parent-agent delegation: каждый reviewer получает полный
пакет напрямую через stdin отдельного изолированного процесса. Codex и OpenCode
dispatch — reviewer и evidence-gate — прошли live-smoke на синтетических данных;
полный lifecycle на реальном репозитории и Claude Code всё ещё нужно подтверждать
отдельно. При недостаточных гарантиях harness останавливает конкретную роль и
записывает ограничение покрытия, а не запускает менее безопасного обычного агента.

## Как проходит проверка

| Этап | Что происходит |
|---|---|
| 1. Запрос | Пользователь задаёт scope и режим проверки |
| 2. Snapshot | `init + collect` фиксируют immutable snapshot выбранных изменений |
| 3. Preflight | Zephyr проверяет доступность внешних источников контекста |
| 4. Контекст | Через read-only MCP добавляются доступные business-context snapshots |
| 5. Routing | Детерминированно выбираются подходящие reviewer-роли |
| 6. Review | Роли запускаются в изолированных reviewer contexts |
| 7. Precheck | Candidate findings проходят JSON Schema и deterministic precheck |
| 8. Evidence gate | Отдельный агент проверяет доказательность найденных замечаний |
| 9. Отчёт | Findings дедуплицируются, после чего создаются `review.json` и `review.md` |

Граница ответственности намеренно разделена:

| Часть | За что отвечает |
|---|---|
| Go core | Git snapshot, immutable packet, config, routing, schema validation, evidence integrity, deduplication, trace и отчёты |
| Codex/Claude/OpenCode harness | Пользовательская orchestration, read-only MCP, запуск моделей и изолированных agent contexts |
| Reviewer role | Только поиск candidate findings внутри своего scope и переданного packet |
| Evidence-gate | Только проверка уже найденных candidates |

Go-процесс не вызывает LLM, MCP или provider API. Он не хранит и не запрашивает
OpenAI/Anthropic API keys. Модель вызывается текущим Codex/Claude/OpenCode harness-ом как
часть пользовательского review.

## Роли ревьюеров

Zephyr запускает все роли, которые соответствуют сигналам конкретного review.
По умолчанию доступны все пятнадцать reviewer-ролей, а отдельный лимит
`max_parallel_reviewers` управляет только числом одновременно работающих
процессов. Поэтому ограничение параллелизма не уменьшает покрытие.

| Роль | Что проверяет |
|---|---|
| `code-reviewer` | Функциональные дефекты, control/data flow, ошибки, edge cases и бизнес-инварианты |
| `architect-reviewer` | План, границы слоёв, зависимости, cross-service impact, rollout/rollback и failure modes |
| `golang-expert` | `context.Context`, errors, goroutines, синхронизацию, lifetime ресурсов, nil/zero semantics и Go API |
| `typescript-expert` | Type safety, narrowing, assertions, nullability, async semantics и соответствие runtime/API schemas |
| `frontend-expert` | React hooks/lifecycle, state и server-state, UI-состояния, routing, a11y, browser security и performance |
| `skill-authoring-expert` | `SKILL.md` frontmatter/triggers, workflow, tool contracts, progressive disclosure, references, scripts и evals |
| `reliability-expert` | Timeout budgets, retries, idempotency, backpressure, graceful degradation, availability и shutdown safety |
| `messaging-expert` | Delivery semantics, ordering, deduplication, offsets/acknowledgements, retry/DLQ, poison messages и transactional boundaries |
| `infrastructure-expert` | Docker/Kubernetes/Helm, probes, resource policy, rollout/rollback, runtime config и CI/CD safety |
| `storage-expert` | Redis/cache consistency, TTL/invalidation, Elasticsearch/OpenSearch mappings, backfills, lifecycle и fallback |
| `security-auditor` | AuthN/AuthZ, IDOR, injection, secrets/PII, логирование, filesystem/network и privilege boundaries |
| `sql-expert` | SQL, транзакции, isolation/locks, индексы, online migrations, целостность и rollback |
| `contract-reviewer` | OpenAPI/Proto/JSON Schema, compatibility, optional/nullable/zero semantics, enums и events |
| `qa-expert` | Конкретные непокрытые ветки, negative/boundary/failure cases и соответствие acceptance criteria |
| `code-simplifier` | Доказуемое переусложнение, дублирование и локальные риски сопровождения; только P2/P3 |

`evidence-gate` не является reviewer-ролью и не занимает место в role limit. Он
запускается один раз после всех выбранных reviewer-ов.

Для `.ts` и `.tsx` автоматически подключается `typescript-expert`; для
`.tsx`, `.jsx`, CSS/SCSS/LESS и frontend semantic signals — `frontend-expert`.
Если diff также затрагивает API schemas, безопасность или тесты, routing
добавляет существующие `contract-reviewer`, `security-auditor` и `qa-expert`.
Generated TypeScript (`generated/`, `__generated__/`, `*.gen.ts[x]`,
`*.generated.ts[x]`) по умолчанию остаётся metadata-only и не ревьюится как
ручной код.

`skill-authoring-expert` выбирается для `SKILL.md`, содержимого `*/skills/*` и
`template/`. Он проверяет Markdown-инструкции вместе с изменёнными references,
scripts и evals, но применяет repository-specific metadata только когда это
подтверждено frozen project instructions.

## Требования

Для запуска готового binary нужны:

- установленный system `git` в `PATH`;
- Codex App, Claude Code или OpenCode для настоящего agent review;
- POSIX `sh` для installer/uninstaller harness-пакета.

Для сборки из исходников дополнительно нужны Go 1.24+ и `make`.

## Быстрый старт

### 1. Быстрая установка

Клонируйте trusted checkout и установите CLI вместе с одним нужным harness:

```bash
git clone git@github.com:signaturekey/zephyr.git
cd zephyr
```

```bash
make install-codex # для Codex
```

Команда устанавливает обе обязательные части: Zephyr CLI и harness-пакет Codex.
После установки откройте новую сессию Codex: уже открытая сессия не увидит skill
сразу. Варианты для Claude Code, OpenCode и раздельной установки перечислены
ниже.

### 2. Точечная установка

Для полной установки под другой harness используйте одну соответствующую
команду:

```bash
make install-claude
make install-opencode
make install-all
```

Если CLI или harness-пакет уже установлен, компоненты можно установить
раздельно.

Установить только CLI из checkout:

```bash
make install-cli
```

`make install` сохранён как совместимый alias для `make install-cli`.

Установить только один harness-пакет из checkout:

```bash
make install-skill-codex
make install-skill-claude
make install-skill-opencode
make install-skill-all
```

После установки CLI bundled harness-пакет можно установить и без checkout:

```bash
zephyr harness install codex
zephyr harness install claude
zephyr harness install opencode
zephyr harness install all
```

Низкоуровневые shell-команды эквивалентны `make install-skill-*` и устанавливают
только skill и agent definitions:

```bash
sh harnesses/install.sh --codex
sh harnesses/install.sh --claude
sh harnesses/install.sh --opencode
sh harnesses/install.sh --all
```

Codex-файлы устанавливаются в `~/.agents/skills/zephyr` и `~/.codex/agents`,
Claude-файлы — в `~/.claude/skills/zephyr` и `~/.claude/agents`, OpenCode-файлы —
в `~/.config/opencode/skills/zephyr` и `~/.config/opencode/agents`. Installer до
первой записи проверяет checksum manifest и все destinations. Существующий
отличающийся файл не перезаписывается.

Проверьте CLI:

```bash
zephyr version
zephyr --help
```

### 3. Обновление

Из checkout обновите CLI и нужный harness:

```bash
make update
make update-codex
make update-claude
make update-opencode
make update-all
```

`make update` эквивалентен `make update-codex`. Update-targets не запускают
тесты, `go vet` и форматирование. Полная проверка запускается отдельно через
`make check`.

Низкоуровневый updater обновляет только harness-пакет:

```bash
sh harnesses/update.sh --codex
sh harnesses/update.sh --claude
sh harnesses/update.sh --opencode
sh harnesses/update.sh --all
```

Updater проверяет установленную версию, собирает новую в staging-каталоге и
сохраняет предыдущую в `~/.codex/backups/zephyr-update.*`. При ошибке публикации
предыдущая версия восстанавливается.

### 4. Удаление

```bash
make uninstall
make uninstall-skill
make uninstall-cli
```

`make uninstall` удаляет CLI и пакеты всех harness-ов. `make uninstall-skill`
удаляет только skills и agents Codex/Claude/OpenCode, а `make uninstall-cli` — только Go
binary.

Для удаления одной поверхности используйте:

```bash
sh harnesses/uninstall.sh --codex
sh harnesses/uninstall.sh --claude
sh harnesses/uninstall.sh --opencode
sh harnesses/uninstall.sh --all
```

Если checkout уже недоступен, embedded-пакет установленного CLI поддерживает
ту же операцию командой `zephyr harness uninstall opencode`.

Uninstaller удаляет только совпадающие Zephyr-файлы. Изменённые и чужие файлы
не затрагиваются. После удаления skill откройте новую сессию harness-а.

### 4. Запросить review

Основной UX — один запрос на естественном языке, а не ручная передача JSON между
командами. Примеры запросов на поверхности с поддерживаемым trusted-agent
dispatch:

```text
$zephyr Проверь staged и unstaged изменения в текущем Go-репозитории.

$zephyr Проверь только staged изменения перед commit.

$zephyr Проверь REVIEW_SPEC.md без Git diff.

$zephyr Сверь текущую реализацию с REVIEW_SPEC.md и доступными требованиями RINT-1234.

$zephyr Проверь текущую ветку относительно main.

$zephyr Проверь TypeScript/React изменения во frontend-ветке.

$zephyr Проверь новый SKILL.md, references и evals перед PR.
```

Для формулировки «проверь локальные изменения» по умолчанию используется scope
`working-tree`. Harness должен показать краткий итог, coverage limits и пути к
`review.md`/`review.json`.

В Codex каждая выбранная роль запускается отдельным `codex exec` в пустой рабочей
директории. Полный immutable packet передаётся процессу через stdin, поэтому
пакеты крупнее лимита вывода родительского агента не обрезаются. Обычный subagent
как запасной вариант не используется.

В OpenCode применяется такой же process boundary через `opencode run`. Dispatcher
создаёт временные `HOME` и XDG-каталоги, копирует только `auth.json`, отключает
внешние plugins, не подключает MCP и запускает одноразовый primary transport-agent
с deny для всех permissions. Установленные `zephyr-*` subagents не используются
как fallback. Модель и variant при необходимости задаются явно:

```bash
export ZEPHYR_OPENCODE_MODEL=provider/model
export ZEPHYR_OPENCODE_VARIANT=high
```

## Режимы и область Git-изменений

Mode отвечает на вопрос «что проверяем по смыслу», а source — «какие Git-данные
входят в snapshot». Это независимые параметры.

### Режимы

| Mode | Назначение |
|---|---|
| `plan` | Review плана или change-spec без обязательной реализации |
| `implementation` | Review кода без требования сверять его с планом |
| `alignment` | Сверка реализации с планом и доступными business requirements |
| `auto` | Выбор `plan`, `implementation` или `alignment` по реально собранным входам |

Для `implementation` и `alignment` базовой обязательной ролью является
`code-reviewer`; для `plan` — `architect-reviewer`.

### Источники Git-изменений

| Source | Что попадает в review |
|---|---|
| `working-tree` | Staged + unstaged изменения относительно `HEAD`, без дублирования |
| `staged` | Только index относительно `HEAD`; unrelated unstaged/untracked изменения не входят в Git diff, но текущие project config и применимые tracked instructions всё равно snapshot-ятся как policy context |
| `branch` | Commit-ы и локальные изменения от `merge-base(base, HEAD)` до working tree |
| `commit-range` | Явный диапазон `A..B` или `A...B`; working tree не входит в scope |
| `plan-only` | План без Git diff и без чтения working-tree status |

Примеры инициализации:

```bash
# Только план
zephyr init --repo . --mode plan --source plan-only --plan REVIEW_SPEC.md

# Все локальные изменения
zephyr init --repo . --mode implementation --source working-tree

# Только staged
zephyr init --repo . --mode implementation --source staged

# Ветка относительно main; --base подразумевает source=branch
zephyr init --repo . --mode implementation --base main

# Диапазон commit-ов; --range подразумевает source=commit-range
zephyr init --repo . --mode implementation --range main..HEAD

# Локальная реализация относительно плана
zephyr init --repo . --mode alignment --source working-tree --plan REVIEW_SPEC.md
```

`init` только создаёт run и печатает его `run_id`. Git snapshot формируется
следующей командой `collect`.

### Сгенерированные, vendor- и untracked-файлы

Generated, vendor и binary changes сохраняются в Git metadata, а их пути, не
запрещённые политикой, могут влиять на routing, даже когда body исключён по
default policy. Restricted changes превращаются в coverage limit без передачи
их имён и содержимого reviewer-ам. Для текущего snapshot можно явно разрешить
generated/vendor body:

```bash
zephyr collect --run RUN_ID --include-generated --include-vendor
```

Untracked metadata доступна только для `working-tree` и `branch`; scopes
`staged`, `commit-range` и `plan-only` её не собирают. Имена untracked-файлов
видны в metadata, но содержимое по умолчанию не читается. Явное включение
остаётся bounded и проходит path/secret filtering:

```bash
zephyr collect --run RUN_ID --include-untracked --max-untracked-bytes 262144
```

## Ручной жизненный цикл CLI

Этот раздел нужен для разработки harness-а, интеграции и диагностики. CLI сам
не создаёт LLM-ответы: `candidate.json` и `verdicts.json` должен получить trusted
harness от изолированных agents. Ручное придумывание этих файлов проверяет лишь
протокол, а не качество настоящего review.

Каждая команда печатает JSON. После `init` скопируйте `run_id` в последующие
команды вместо `RUN_ID`. Временные входы могут содержать diff и business
context, поэтому создавайте их с закрытыми правами вне reviewed repository:

```bash
umask 077
ZEPHYR_INPUT_DIR=$(mktemp -d)

# Trusted harness/integrator сохраняет подготовленные inputs в эту директорию.
zephyr init --repo . --mode alignment --source working-tree --plan REVIEW_SPEC.md
zephyr collect --run RUN_ID

# Обязательно: зафиксировать статус каждого поддерживаемого внешнего источника.
zephyr context capability --run RUN_ID --source jira --status available
zephyr context capability --run RUN_ID --source confluence --status unavailable \
  --reason "read-only MCP unavailable"
zephyr context capability --run RUN_ID --source bitbucket --status not-required \
  --reason "review uses the local Git snapshot and has no PR URL"

# Опционально: импорт уже прочитанного harness-ом business context.
zephyr context add \
  --run RUN_ID \
  --source jira \
  --key RINT-1234 \
  --input "$ZEPHYR_INPUT_DIR/rint-1234.md"

# Недоступный или обрезанный отдельный объект фиксируется явно.
zephyr context limit \
  --run RUN_ID \
  --source confluence \
  --reason "read-only MCP unavailable"

# Packet и routing замораживаются ровно один раз.
zephyr route --run RUN_ID

# Повторить для каждой выбранной роли из routing.json.
zephyr validate-candidates \
  --run RUN_ID \
  --role golang-expert \
  --input "$ZEPHYR_INPUT_DIR/golang-expert-candidates.json"

# После всех ролей — один evidence-gate.
zephyr validate-verdicts \
  --run RUN_ID \
  --input "$ZEPHYR_INPUT_DIR/evidence-verdicts.json"

zephyr aggregate --run RUN_ID
zephyr render --run RUN_ID
zephyr inspect --run RUN_ID
```

После завершения удалите временную директорию с inputs, предварительно проверив,
что это именно путь, который вернул `mktemp -d`.

На format/schema failure harness может один раз повторить роль с тем же exact
packet и константной инструкцией вернуть валидный JSON. Внутри dispatcher также
разрешён один process retry для `rate-limit`, `provider-unavailable`, `transport`
или неизвестного инфраструктурного сбоя: новый ephemeral-процесс получает
byte-identical prompt после role-staggered задержки 1–4 секунды. Auth/config
ошибки, substantive empty result и внешний timeout не повторяются.

Сбой одной reviewer-роли не уничтожает валидные результаты остальных:

```bash
zephyr mark-failed \
  --run RUN_ID \
  --stage review \
  --role golang-expert \
  --reason "trusted agent unavailable"
```

Сбой evidence-gate отмечается через `--stage evidence`, переводит run в
`incomplete` и запрещает считать candidates подтверждёнными findings.

`zephyr inspect --run RUN_ID` можно вызывать для диагностики после любого шага:
он показывает state, stages, выбранные/успешные роли, coverage limits, counts и
доступные артефакты.

## Как читать результат

### Состояние run и отчёта

`manifest.json` хранит lifecycle state:

| State | Значение |
|---|---|
| `created` | Run создан, но работа ещё не началась |
| `running` | Выполнена только часть стадий |
| `complete` | Финальный отчёт успешно отрендерен |
| `incomplete` | Критическая для доверия стадия, например evidence-gate, не завершена |
| `failed` | Детерминированная стадия остановилась с ошибкой |

У самого `review.json` есть отдельный status:

- `complete` — отчёт агрегирован без известных пробелов coverage;
- `complete-with-limits` — отчёт создан, но часть scope не проверена или snapshot
  успел стать stale.

`stale` не означает, что старый отчёт переписался новыми данными: он всё ещё
относится к исходному SHA/fingerprint. Для актуального review создайте новый run.

### Находки и служебные секции

- `Findings` — только accepted/downgraded candidates, прошедшие evidence-gate.
- `Needs human` — вопросы, которым не хватило данных; это не подтверждённые
  дефекты.
- `Coverage limits` — непроверенные роли, отсутствующие requirements, truncation
  и другие границы вывода. Пустой список findings при наличии limits нельзя
  интерпретировать как «проблем точно нет».
- `Rejected candidates` — отброшенные precheck/evidence-gate candidates; они не
  входят в итоговые findings.

`review.md` оптимизирован для короткого чтения и может скрыть часть P1/P2/P3 по
настроенному лимиту. `review.json` — canonical полный результат. Следующий флаг
разрешает renderer-у включать P3 при наличии свободного места в
`max_final_findings`; если P0–P2 уже исчерпали лимит, P3 останутся только в JSON:

```bash
zephyr render --run RUN_ID --include-p3
```

### Приоритеты P0–P3

| Severity | Как трактовать |
|---|---|
| P0 | Подтверждённая критическая уязвимость, необратимая потеря данных, гарантированный main-path panic/deadlock или rollout blocker |
| P1 | Вероятный функциональный дефект, contract break, race/resource leak либо серьёзный production/security/data-integrity risk |
| P2 | Конкретный test gap, важный непокрытый failure mode или доказуемый maintainability/performance risk |
| P3 | Низкорисковое локальное улучшение или точечный вопрос для человека |

Для code finding уровня P0/P1 обязательны location, минимальный code/diff
fragment, достижимый execution/data path, нарушенный invariant/requirement,
observable impact и проверенное counterevidence. Для plan finding нужны
artifact/section, requirement или system invariant, конкретный failure scenario,
impact и доказательство того, что план не закрывает этот сценарий. Высокая
confidence сама по себе не заменяет доказательства.

## Capability-preflight и внешний контекст

Задачи, документацию и PR-контекст из сервиса код-хостинга (например, GitHub,
GitLab или Bitbucket) читает только harness через уже доступный read-only MCP.
Core не знает credentials и не выполняет сетевые вызовы.

До `route` harness обязан записать для каждого требуемого источника один статус:
`available`, `unavailable` или `not-required`. Для двух последних нужен reason.
Неполный preflight блокирует routing, а `unavailable` автоматически попадает в
coverage limits. Статусы сохраняются в `context/capabilities.json` и видны через
`zephyr inspect`.

До `route` harness может сохранить нормализованные snapshots со следующей
provenance:

- source и object key/ID;
- URL, если он доступен;
- `fetched_at`;
- `sha256:` content hash;
- нормализованный Markdown content.

Искать следует только явно связанную с review задачу, acceptance criteria,
contract-relevant linked issues, непосредственно релевантные страницы
документации и явно запрошенные PR metadata/comments из сервиса код-хостинга.
Бесконтрольный поиск по всей базе знаний не входит в протокол.

Если MCP недоступен, review может продолжиться только с явным coverage limit.
Отсутствующее требование нельзя выдавать за проверенное.

## Артефакты и диагностика

По умолчанию run хранится вне reviewed repository:

```text
${XDG_CACHE_HOME:-$HOME/.cache}/zephyr/runs/<run-id>/
```

Путь можно изменить глобальным `--run-root` или `ZEPHYR_RUN_ROOT`, но core
отклонит директорию внутри reviewed repository, чтобы review не делал working
tree dirty.

Ключевые артефакты:

| Файл | Для чего нужен |
|---|---|
| `manifest.json` | State, stages, исходный scope и coverage limits |
| `git/metadata.json` | Git provenance, HEAD/base/target и fingerprints |
| `git/status.json` | Список изменений и metadata исключённых файлов |
| `git/diff.patch` | Immutable review diff |
| `context/` | Snapshot плана, project instructions, config и business sources |
| `packet/review-packet.json` | Единственный frozen input reviewer-ов |
| `routing.json` | Выбранные/исключённые роли и причины решения |
| `candidates/` | Валидированные ответы отдельных reviewer-ролей |
| `evidence/precheck.json` | Детерминированные результаты precheck |
| `evidence/verdicts.json` | Verdicts evidence-gate |
| `rejected-findings.json` | Полный список отклонённых candidates с reason codes |
| `review.json` | Canonical машинный результат |
| `review.md` | Короткий человекочитаемый отчёт |
| `trace.json` | Структурированный локальный trace стадий, времени и sanitised errors |

Отдельной logging-библиотеки нет. CLI пишет успешные command results в JSON на
`stdout`, ошибки — в `stderr`, а per-run события сохраняет собственный
структурированный `trace.json`. Metadata и ошибки перед сохранением проходят
redaction; trace записывается атомарно с правами `0600`.

Run artifacts могут содержать diff и business context. Не публикуйте весь run
directory без отдельной проверки, даже несмотря на встроенную redaction.

## Конфигурация проекта

Встроенные defaults находятся в `configs/default.yaml`. Репозиторий может
дополнить их файлом `.zephyr/config.yaml`:

```yaml
version: 1
profile: thorough
language: auto

limits:
  max_parallel_reviewers: 4
  max_roles_standard: 15
  max_roles_thorough: 15
  max_final_findings: 12

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

Merge semantics:

- scalar values и отдельные limits переопределяют defaults;
- упомянутые `roles.<name>` переопределяются по одной роли;
- `routing`, `restricted_paths` и `redaction.deny_patterns` дополняют defaults;
- неизвестные поля, роли, невалидные globs и противоречивые limits останавливают
  run до запуска reviewer-ов;
- `language` принимает только `auto`, `go`, `typescript` или `markdown`.

По умолчанию и `standard`, и `thorough` разрешают все пятнадцать reviewer-ролей.
Routing выбирает столько подходящих ролей, сколько требуется затронутым путям и
сигналам. `max_parallel_reviewers: 4` ограничивает только одновременную работу:
оставшиеся выбранные роли запускаются следующими партиями. Проект может явно
уменьшить `max_roles_standard` или `max_roles_thorough`; это будет осознанным
ограничением покрытия. Базовая обязательная роль входит в role limit,
`evidence-gate` — нет. Ручные `route --add-role`/`--exclude-role` применяются до
лимита профиля, при этом обязательную для режима роль исключить нельзя.

`language` может быть `auto`, `go`, `typescript` или `markdown`. Значение
`auto` используется по умолчанию и позволяет одному Zephyr checkout проверять
смешанные репозитории; конкретные роли всё равно выбираются по frozen paths и
semantic signals.

`redaction.enabled: false` отключает только дополнительные project
`deny_patterns`. Неотключаемая baseline-защита `.env`, credentials, private keys
и распространённых token formats остаётся активной.

## Гарантии read-only и модель доверия

Zephyr не должен:

- изменять reviewed source или запускать auto-fix/formatting;
- выполнять `git add`, commit, branch/tag mutation, checkout/reset/clean/stash,
  push или другие Git write operations;
- создавать PR, review comments или записи во внешних системах;
- писать в трекеры задач, базы знаний или сервисы код-хостинга;
- читать untracked content без явного согласия;
- включать `.env`, credentials, private keys и known token formats в packet,
  trace или report;
- представлять непроверенный candidate как confirmed finding.

System Git вызывается через argv без shell interpolation, с timeout,
cancellation, bounded output и read-only allowlist. Перед `status`/`diff`, который
может сравнивать working tree с index, runner проверяет effective Git config с
includes. При `filter.<driver>.clean` или `filter.<driver>.process` worktree
collection останавливается до опасной команды: даже read-only Git способен
запустить внешний процесс. Index-only `staged` и object-only `commit-range`
остаются доступны, если подходят требуемому scope.

Dirty submodule working tree не обходится рекурсивно. Zephyr сохраняет изменение
gitlink SHA, но не запускает submodule-local hooks или filters.

### Изоляция reviewer-ов

Reviewer получает только точные байты общего протокола, своего role prompt,
immutable packet и output schema. Блоки размечаются случайным nonce, длиной в
байтах и SHA-256; корень репозитория, run directory, читаемые live-пути и tool
handles не передаются. Reviewer-у запрещены filesystem, Git, shell, MCP, web,
skills и другие agents.

Перед dispatch harness сверяет active skill/assets/agent definition с manifest
и требует trusted provenance. Project-local shadow, symlink, duplicate name или
неоднозначное разрешение закрывают роль. Checksum обнаруживает drift, но не
является цифровой подписью: исходный checkout и installation path всё равно
должны быть доверенными.

Codex adapter запускает отдельный ephemeral `codex exec` в пустой директории с
read-only sandbox, `approval_policy=never`, отключёнными project rules, user
config, web, apps, MCP, memory, multi-agent и shell-возможностями. Запрос
передаётся напрямую через stdin, поэтому большой packet не проходит через
обрезаемый вывод tool call. Обычный/default-agent fallback запрещён.

Для каждого процесса создаётся отдельный временный `CODEX_HOME` внутри каталога
с правами `0700`. Туда с правами `0600` копируется только обычный нессылочный
`auth.json`; state/log SQLite и cache больше не пытаются писать в реальный
`~/.codex`, который у стандартной workspace-write задачи доступен только для
чтения. В prompt auth не попадает, временная копия удаляется вместе с workdir.

При process failure dispatcher не выводит raw `stderr`: он возвращает только
безопасную категорию, exit status, размер и SHA-256 fingerprint. Для
транзиентных/неизвестных инфраструктурных ошибок выполняется один внутренний
retry; auth и config ошибки завершаются сразу.

## Известные ограничения

- Process-level Codex dispatch и exact-byte transport покрыты статическим и
  200-КБ автоматическим smoke-тестом. Live-smoke текущего Codex CLI выполнен для
  reviewer и evidence-gate, но такой сетевой model-вызов намеренно не входит в
  обычный локальный test suite.
- Claude Code добавляет agent-у working directory, `CLAUDE.md`/project memory и
  Git status; полное отключение этого host context per agent не подтверждено.
- Полноценная OS-level недоступность repository root для reviewer-а пока не
  заявляется как verified.
- Harness validation проверяет структуру и security contracts статически, но не
  доказывает MCP discovery, model dispatch, context isolation и handoff
  end-to-end.
- Zephyr не запускает build/test target проверяемого проекта и не является
  smoke-test runner. Он анализирует frozen evidence; runtime-проверки остаются
  обязанностью разработчика и CI.

## Решение типичных проблем

| Симптом | Что проверить |
|---|---|
| `zephyr: command not found` | Запустите `make build` и используйте `./bin/zephyr` либо добавьте `GOBIN`/`GOPATH/bin` в `PATH` |
| Run root отклонён как находящийся внутри repo | Уберите override или укажите внешнюю cache directory |
| `auto mode requires a plan, Git changes, or both` | Передайте `--plan`, выберите правильный source или создайте reviewable Git changes |
| Нет reviewable diff | Проверьте mode/source; untracked content не входит в diff без явного `--include-untracked` |
| Найден `filter.*.clean/process` | Worktree review намеренно fail-closed; если scope позволяет, используйте `staged`, `commit-range` или `plan-only`, не отключая filter ради Zephyr |
| `repository changed` или snapshot stale | Остановите изменения в repo и начните новый run; старый snapshot не пересобирается |
| Invalid project config | Исправьте неизвестное поле/роль, glob или limits; reviewers ещё не запускались |
| Reviewer завершился ошибкой | Посмотрите категорию и `stderr_sha256` в `coverage_limits`/выводе dispatcher; валидные результаты других ролей сохраняются |
| Все reviewer-ы падают с `category=sandbox` | Проверьте, что установлен свежий dispatcher с временным `CODEX_HOME`; выполните `make update` и откройте новую задачу |
| Evidence-gate завершился ошибкой | Run остаётся `incomplete`; candidates нельзя публиковать как findings |
| `capability preflight incomplete` | До `route` зафиксируйте каждый требуемый источник через `context capability` |
| Внешний источник недоступен | Зафиксируйте capability как `unavailable` с причиной; не заявляйте alignment с отсутствующим источником |
| P3 не видно в Markdown | Используйте `render --include-p3`; полный результат всегда находится в `review.json` |
| Installer отказывается писать target | Для существующей установки используйте `update.sh`; чужой collision не удаляйте вслепую |
| Updater отклоняет modified/foreign file | Проверьте файл вручную; updater намеренно не затирает локальные правки и чужие agent definitions |
| Updater прервался после начала замены | Проверьте сообщение `Previous installation restored` и diagnostic backup path; старая версия должна быть восстановлена автоматически |
| Codex reviewer не запустился | Проверьте наличие `codex`, checksum bundled assets и `trace.json`; обычный-agent fallback намеренно запрещён |

## Разработка

Базовая проверка:

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

Golden tests проверяют protocol, deterministic precheck, verdict integrity,
aggregation и stable Markdown, но не моделируют «хорошие ответы LLM». Unit и
static harness tests также не доказывают MCP/agent behavior end-to-end.

Инженерный и архитектурный source of truth: [AGENTS.md](AGENTS.md).

Инструкции для разработчиков и coding agents: [AGENTS.md](AGENTS.md).
