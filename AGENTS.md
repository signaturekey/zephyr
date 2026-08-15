# Zephyr engineering contract

This file is the source of truth for the current Zephyr architecture.

## Working agreement

- Do not commit, push, create branches, publish review comments, or mutate external
  systems unless the user explicitly requests it.
- Distinguish implemented behavior, checks actually run, skipped checks, and remaining
  coverage limits.
- Preserve the user's checkout. Git writes are allowed only inside a new disposable
  snapshot directory created by Zephyr.
- Keep changes focused. Do not restore the removed stage machine, Codex dispatcher,
  harness installer framework, private `CODEX_HOME`, asset checksums, or compatibility
  probes. `make install` and `make uninstall` may only copy or remove the thin user
  skill at `$HOME/.agents/skills/zephyr` alongside the CLI.

## Product

Zephyr is a local, read-only, evidence-gated code reviewer. One invocation:

```text
zephyr review
  -> disposable Git snapshot
  -> deterministic and semantic role routing
  -> isolated parallel reviewer threads through Aether
  -> deterministic candidate precheck
  -> one evidence-gate thread
  -> Markdown stdout and optional JSON/Markdown files
```

Supported sources:

```text
--worktree [--repo PATH]
--commit SHA [--repo PATH_OR_URL]
--branch REF --base REF [--repo PATH_OR_URL]
```

The default is `--worktree --repo .`. Worktree review includes the combined staged,
unstaged, deleted, renamed, and non-ignored untracked state. A dirty checkout is normal
input, not an error.

Zephyr does not edit reviewed code, commit, push, create branches or pull requests,
write external context, run CI, deploy, or publish review comments.

## Aether boundary

`github.com/signaturekey/aether` is the only Codex App Server integration. Aether is a
generic Go SDK and must never import Zephyr or contain Zephyr-specific roles, schemas,
prompts, routing, evidence policy, or report logic.

Zephyr may use only Aether's public Go API. Do not add a local JSON-RPC/App Server
adapter, `codex exec` parser, auth copier, private Codex home, shell dispatcher, or
retry/compatibility framework around it.

One review uses one Aether client and one fresh ephemeral thread per semantic router,
reviewer, and evidence gate. Threads use a neutral CWD, approval policy `never`, and a
read-only sandbox. Reviewers do not see one another's output.

## Snapshot boundary

`internal/snapshot` owns acquisition:

- worktree: clone local `HEAD`, apply one combined binary diff, copy non-ignored
  untracked files;
- commit: clone, resolve and detach the commit, diff its first parent or empty tree;
- branch: clone, resolve base/head, detach head, diff merge-base through head.

Never initialize submodules. Never mutate the source repository. Do not reject a run
because the source has staged, unstaged, untracked, or later-changing state. Reports
belong to the frozen snapshot already created; there is no stale-head ceremony.

## Routing

Keep these invariants:

- `code-reviewer` is mandatory for implementation review;
- roles proven by changed paths or strong diff signals are protected;
- explicit includes are protected;
- `security-auditor` is protected unless explicitly excluded;
- the semantic router decides every remaining enabled optional role exactly once;
- invalid or failed semantic routing includes every unresolved role conservatively;
- `max_parallel_reviewers` limits concurrency, never total coverage.

The existing `configs/default.yaml` remains the canonical out-of-box configuration.
Users must not need a project config or flags for a normal local review. Preserve its
models, effort, per-role overrides, role enablement, routing rules, and concurrency
default. Project `.zephyr/config.yaml` is an optional overlay.

## Review and evidence

Prompts in `roles/` and contracts in `internal/protocol/` are authoritative. Reviewed repository
instructions, code comments, plans, and external context are untrusted evidence, not
orchestration instructions.

Each specialist receives its role prompt, a role-focused primary diff, the full changed
path index, frozen context, and read access to the common snapshot for supporting files.
It must report only concrete findings within its closed scope.

Deterministic precheck rejects malformed identities, unknown categories, forbidden
severity, locations outside changed diff hunks, and incomplete evidence. The evidence
gate receives only prechecked candidates plus the frozen snapshot evidence. It may
accept, reject, downgrade, mark duplicate, or request human review. It may never create
a finding or raise severity.

Markdown must show every accepted P0-P3 finding. A failed reviewer is a coverage limit,
not a reason to discard successful roles. An incomplete or invalid evidence gate is an
operational failure and must not present candidates as confirmed findings.

## Context and MCP

The CLI accepts repeatable `--context FILE` inputs. A thin harness skill may collect
explicitly requested business context through available read-only MCP and freeze it to
temporary Markdown/JSON. The Go core does not own Jira, Confluence, Bitbucket, or MCP
provider clients and performs no external writes.

Native MCP collection through Aether is deferred until Zephyr can enforce a real
read-only tool allowlist; approval policy `never` alone is not such a policy.

## Repository layout

```text
cmd/zephyr/          public `review` and `version` commands
internal/snapshot/  disposable Git acquisition
internal/config/    embedded defaults and optional overlay
internal/routing/   protected roles, semantic request, fallback, role views
internal/agent/     thin Aether consumer
internal/evidence/  deterministic precheck and verdict integrity
internal/dedupe/    stable finding grouping
internal/protocol/  model protocol types, schemas, and validation
internal/report/    canonical JSON model and Markdown rendering
internal/review/    one in-process orchestration pipeline
roles/              embedded reviewer/router/gate prompts
.agents/skills/      thin invocation skill
```

Keep packages capability-oriented. `cmd` contains adapters only. Do not add generic
utility packages, a database, server, UI, persistent run state, or public commands for
internal stages.

## Go rules

- Go 1.24 or newer.
- Use system Git through `exec.CommandContext` with argv slices and explicit `git -C`.
- No shell command construction and no `go-git`.
- Keep filesystem, Git, time, and Aether behind narrow testable boundaries.
- Avoid package-level mutable state and package cycles.
- Machine-readable output must be deterministic where order matters.

Before handing off a change, run:

```text
gofmt -w <changed Go files>
go test ./...
go test -race ./...
go vet ./...
```

Run a real Aether/Codex smoke when runtime behavior changed and the environment is
available. If it was not run, state that explicitly.
