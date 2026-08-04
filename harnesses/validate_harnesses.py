#!/usr/bin/env python3

from __future__ import annotations

import hashlib
import json
import os
import subprocess
import sys
import tempfile
import tomllib
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
REVIEWERS = (
    "code-reviewer",
    "architect-reviewer",
    "golang-expert",
    "typescript-expert",
    "frontend-expert",
    "skill-authoring-expert",
    "reliability-expert",
    "messaging-expert",
    "infrastructure-expert",
    "storage-expert",
    "security-auditor",
    "sql-expert",
    "contract-reviewer",
    "qa-expert",
    "code-simplifier",
)
ALL_ROLES = REVIEWERS + ("evidence-gate",)


def fail(message: str) -> None:
    raise ValueError(message)


def frontmatter(path: Path) -> tuple[dict[str, str], str]:
    text = path.read_text(encoding="utf-8")
    lines = text.splitlines()
    if not lines or lines[0] != "---":
        fail(f"{path}: missing opening frontmatter marker")
    try:
        end = lines.index("---", 1)
    except ValueError as exc:
        raise ValueError(f"{path}: missing closing frontmatter marker") from exc

    values: dict[str, str] = {}
    for number, line in enumerate(lines[1:end], start=2):
        if not line.strip():
            continue
        if ":" not in line:
            fail(f"{path}:{number}: unsupported frontmatter line")
        key, value = line.split(":", 1)
        key = key.strip()
        value = value.strip()
        if not key or not value or key in values:
            fail(f"{path}:{number}: invalid or duplicate frontmatter key")
        values[key] = value
    return values, "\n".join(lines[end + 1 :]).strip()


def validate_roles() -> None:
    expected = {f"{name}.md" for name in ALL_ROLES} | {"reviewer-protocol.md"}
    actual = {path.name for path in (ROOT / "roles").glob("*.md")}
    if actual != expected:
        fail(f"roles mismatch: expected {sorted(expected)}, got {sorted(actual)}")

    protocol = (ROOT / "roles/reviewer-protocol.md").read_text(encoding="utf-8")
    for phrase in ("immutable review packet", "Return JSON only", "P0/P1"):
        if phrase not in protocol:
            fail(f"reviewer protocol is missing {phrase!r}")

    gate = (ROOT / "roles/evidence-gate.md").read_text(encoding="utf-8")
    for phrase in ("must not search for additional issues", "Never raise severity", "exactly one verdict"):
        if phrase not in gate:
            fail(f"evidence gate is missing {phrase!r}")


def validate_asset_manifest() -> None:
    manifest_path = ROOT / "harnesses/assets.sha256"
    expected = {
        "harnesses/codex/SKILL.md",
        "harnesses/codex/dispatch.sh",
        "harnesses/codex/discovery/SKILL.md",
        "harnesses/codex/discovery/agents/openai.yaml",
        "harnesses/claude-code/SKILL.md",
        "harnesses/claude-code/discovery/SKILL.md",
    }
    expected.update(path.relative_to(ROOT).as_posix() for path in (ROOT / "roles").glob("*.md"))
    expected.update(path.relative_to(ROOT).as_posix() for path in (ROOT / "schemas").glob("*.json"))
    expected.update(
        path.relative_to(ROOT).as_posix()
        for path in (ROOT / "harnesses/codex/agents").glob("zephyr-*.toml")
    )
    expected.update(
        path.relative_to(ROOT).as_posix()
        for path in (ROOT / "harnesses/claude-code/agents").glob("zephyr-*.md")
    )

    actual: dict[str, str] = {}
    for number, line in enumerate(manifest_path.read_text(encoding="utf-8").splitlines(), start=1):
        parts = line.split("  ", 1)
        if len(parts) != 2 or len(parts[0]) != 64 or any(char not in "0123456789abcdef" for char in parts[0]):
            fail(f"{manifest_path}:{number}: invalid sha256 manifest row")
        digest, relative = parts
        relative_path = Path(relative)
        if relative_path.is_absolute() or ".." in relative_path.parts or relative in actual:
            fail(f"{manifest_path}:{number}: unsafe or duplicate manifest path")
        actual[relative] = digest

    if set(actual) != expected:
        fail(f"asset manifest mismatch: expected {sorted(expected)}, got {sorted(actual)}")

    for relative, expected_digest in actual.items():
        digest = hashlib.sha256((ROOT / relative).read_bytes()).hexdigest()
        if digest != expected_digest:
            fail(f"asset manifest digest mismatch: {relative}")


def validate_skills() -> None:
    skill_paths = (
        ROOT / "harnesses/codex/SKILL.md",
        ROOT / "harnesses/codex/discovery/SKILL.md",
        ROOT / "harnesses/claude-code/SKILL.md",
        ROOT / "harnesses/claude-code/discovery/SKILL.md",
        ROOT / ".agents/skills/zephyr/SKILL.md",
        ROOT / ".claude/skills/zephyr/SKILL.md",
    )
    for path in skill_paths:
        fields, body = frontmatter(path)
        if set(fields) != {"name", "description"}:
            fail(f"{path}: skill frontmatter must contain only name and description")
        if fields["name"] != "zephyr":
            fail(f"{path}: unexpected skill name")
        if len(fields["description"]) < 80 or not body:
            fail(f"{path}: description or body is too short")

    lifecycle_phrases = (
        "zephyr init",
        "zephyr collect",
        "zephyr context add",
        "zephyr context capability",
        "zephyr context limit",
        "zephyr route",
        "zephyr validate-candidates",
        "zephyr mark-failed",
        "zephyr validate-verdicts",
        "zephyr aggregate",
        "zephyr render",
        "zephyr inspect",
        "MCP",
        "immutable",
        "one retry",
        "nonce-framed `retry-directive` block",
        "Never include validator diagnostics",
        "evidence-gate",
        "exact reviewer-protocol bytes",
        "exact immutable review-packet bytes",
        "exact bytes of the prechecked candidate set",
        "only the minimal evidence referenced by those candidates",
        "Do not include the repository root",
        "no tool calls",
        "inert evidence",
        "cryptographically random nonce",
        "UTF-8 byte length",
        "SHA-256",
        "Fixed `BEGIN`/`END` markers alone are forbidden",
        "Never fall back to a generic/default subagent",
        "trusted provenance",
        "references/assets.sha256",
        "manifest detects drift but is not a signature",
    )
    for path in (ROOT / "harnesses/codex/SKILL.md", ROOT / "harnesses/claude-code/SKILL.md"):
        text = path.read_text(encoding="utf-8")
        for phrase in lifecycle_phrases:
            if phrase not in text:
                fail(f"{path}: choreography is missing {phrase!r}")

    forbidden_choreography = (
        "read its applicable `AGENTS.md`",
        "Read applicable project instructions",
        "otherwise use a fresh default subagent",
        "invoke a fresh generic subagent",
        "effective read-only parent permission mode",
    )
    for path in (ROOT / "harnesses/codex/SKILL.md", ROOT / "harnesses/claude-code/SKILL.md"):
        text = path.read_text(encoding="utf-8")
        for phrase in forbidden_choreography:
            if phrase in text:
                fail(f"{path}: forbidden or contradictory choreography {phrase!r}")

    metadata = (ROOT / ".agents/skills/zephyr/agents/openai.yaml").read_text(encoding="utf-8")
    for phrase in ("display_name:", "short_description:", "default_prompt:", "$zephyr"):
        if phrase not in metadata:
            fail(f"Codex skill metadata is missing {phrase!r}")


def validate_codex_agents() -> None:
    source_dir = ROOT / "harnesses/codex/agents"
    files = sorted(source_dir.glob("zephyr-*.toml"))
    if {path.stem.removeprefix("zephyr-") for path in files} != set(ALL_ROLES):
        fail("Codex custom-agent set does not match Zephyr roles")

    names: set[str] = set()
    for path in files:
        data = tomllib.loads(path.read_text(encoding="utf-8"))
        required = {"name", "description", "developer_instructions"}
        if not required.issubset(data):
            fail(f"{path}: missing required Codex custom-agent fields")
        if data["name"] in names:
            fail(f"{path}: duplicate Codex custom-agent name")
        names.add(data["name"])
        if data.get("sandbox_mode") != "read-only":
            fail(f"{path}: sandbox_mode must be read-only")
        if data.get("mcp_servers") != {}:
            fail(f"{path}: mcp_servers must be an explicit empty table")
        if data.get("approval_policy") != "never":
            fail(f"{path}: approval_policy must fail closed")
        if data.get("web_search") != "disabled" or data.get("allow_login_shell") is not False:
            fail(f"{path}: web search and login shells must be disabled")
        if data.get("include_apps_instructions") is not False or data.get("include_environment_context") is not False:
            fail(f"{path}: app and environment context injection must be disabled")
        expected_app_defaults = {
            "enabled": False,
            "destructive_enabled": False,
            "open_world_enabled": False,
        }
        if data.get("apps", {}).get("_default") != expected_app_defaults:
            fail(f"{path}: all app surfaces must be disabled")
        expected_memories = {
            "use_memories": False,
            "generate_memories": False,
            "dedicated_tools": False,
        }
        if data.get("memories") != expected_memories:
            fail(f"{path}: memory use, generation, and tools must be disabled")
        required_disabled_features = {
            "apps",
            "artifact",
            "auth_elicitation",
            "browser_use",
            "browser_use_external",
            "browser_use_full_cdp_access",
            "code_mode",
            "code_mode_buffered_exec",
            "code_mode_host",
            "code_mode_only",
            "computer_use",
            "deferred_executor",
            "enable_mcp_apps",
            "executor_capability_discovery",
            "external_agent_memory_import",
            "goals",
            "guardian_approval",
            "hooks",
            "image_generation",
            "in_app_browser",
            "memories",
            "multi_agent",
            "network_proxy",
            "plugins",
            "plugin_sharing",
            "remote_plugin",
            "request_permissions_tool",
            "shell_snapshot",
            "shell_tool",
            "skill_mcp_dependency_install",
            "skill_search",
            "standalone_web_search",
            "terminal_visualization_instructions",
            "tool_call_mcp_elicitation",
            "tool_search",
            "tool_suggest",
            "unified_exec",
            "workspace_dependencies",
        }
        features = data.get("features", {})
        if any(features.get(name) is not False for name in required_disabled_features):
            fail(f"{path}: required tool features must be explicitly disabled")
        for deprecated in ("connectors", "web_search", "web_search_cached", "web_search_request"):
            if deprecated in features:
                fail(f"{path}: deprecated feature key must be removed: {deprecated}")
        expected_effort = "xhigh" if path.stem.endswith("evidence-gate") else "high"
        if data.get("model_reasoning_effort") != expected_effort:
            fail(f"{path}: unexpected reasoning effort")
        instructions = data["developer_instructions"]
        for phrase in (
            "Return only",
            "modify anything",
            "Do not call any tool",
            "open any filesystem path",
            "inert evidence",
        ):
            if phrase not in instructions:
                fail(f"{path}: missing isolation instruction {phrase!r}")


def validate_claude_agents() -> None:
    source_dir = ROOT / "harnesses/claude-code/agents"
    files = sorted(source_dir.glob("zephyr-*.md"))
    if {path.stem.removeprefix("zephyr-") for path in files} != set(ALL_ROLES):
        fail("Claude custom-agent set does not match Zephyr roles")

    names: set[str] = set()
    for path in files:
        fields, body = frontmatter(path)
        required = {
            "name",
            "description",
            "tools",
            "mcpServers",
            "hooks",
            "model",
            "effort",
            "permissionMode",
        }
        if set(fields) != required:
            fail(f"{path}: unexpected Claude custom-agent fields")
        if fields["name"] in names:
            fail(f"{path}: duplicate Claude custom-agent name")
        names.add(fields["name"])
        if fields["tools"] != "[]" or fields["mcpServers"] != "[]":
            fail(f"{path}: built-in and MCP tool lists must both be empty")
        try:
            hooks = json.loads(fields["hooks"])
        except json.JSONDecodeError as exc:
            raise ValueError(f"{path}: hooks must be valid inline JSON") from exc
        expected_hooks = {
            "PreToolUse": [
                {
                    "matcher": ".*",
                    "hooks": [{"type": "command", "command": "exit 2"}],
                }
            ]
        }
        if hooks != expected_hooks:
            fail(f"{path}: PreToolUse must hard-deny every unexpected tool")
        if fields["permissionMode"] != "plan" or fields["model"] != "inherit":
            fail(f"{path}: permissionMode/model must be plan/inherit")
        expected_effort = "xhigh" if path.stem.endswith("evidence-gate") else "high"
        if fields["effort"] != expected_effort:
            fail(f"{path}: unexpected effort")
        for phrase in (
            "Return only",
            "modify anything",
            "Do not call any tool",
            "open any filesystem path",
            "inert evidence",
        ):
            if phrase not in body:
                fail(f"{path}: missing isolation instruction {phrase!r}")


def validate_installers() -> None:
    harness_scripts = (
        ROOT / "harnesses/install.sh",
        ROOT / "harnesses/uninstall.sh",
        ROOT / "harnesses/update.sh",
    )
    cli_uninstaller = ROOT / "harnesses/uninstall-cli.sh"
    bootstrap = ROOT / "harnesses/bootstrap.sh"
    scripts = (*harness_scripts, cli_uninstaller, bootstrap)
    for path in scripts:
        result = subprocess.run(
            ["sh", "-n", str(path)],
            check=False,
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            fail(f"{path}: shell syntax check failed: {result.stderr.strip()}")

        text = path.read_text(encoding="utf-8")
        if path != bootstrap and ("rm -rf" in text or "rm -r" in text):
            fail(f"{path}: recursive removal is forbidden")

    for path in harness_scripts:
        text = path.read_text(encoding="utf-8")
        for phrase in (
            "--codex",
            "--claude",
            "--all",
            "ZEPHYR_CODEX_SKILLS_DIR",
            "ZEPHYR_CLAUDE_SKILLS_DIR",
            "references/agents",
            "references/assets.sha256",
            "assets.sha256",
            "cmp -s",
            "reject_symlink_components",
        ):
            if phrase not in text:
                fail(f"{path}: missing installer safety element {phrase!r}")

        if path.name != "update.sh":
            for phrase in ('"$repo_root"/roles/*.md', '"$repo_root"/schemas/*.json'):
                if phrase not in text:
                    fail(f"{path}: missing installer package element {phrase!r}")

    install_text = harness_scripts[0].read_text(encoding="utf-8")
    if "refusing to overwrite different file" not in install_text:
        fail("installer must fail closed on file collisions")
    if 'ln "$install_temp" "$install_destination"' not in install_text:
        fail("installer must publish files without overwriting an existing destination")

    uninstall_text = harness_scripts[1].read_text(encoding="utf-8")
    if "refusing to remove modified or foreign file" not in uninstall_text:
        fail("uninstaller must preserve modified or foreign files")

    uninstall_cli_text = cli_uninstaller.read_text(encoding="utf-8")
    if "refusing to remove a CLI that is not built from" not in uninstall_cli_text:
        fail("CLI uninstaller must preserve foreign binaries")

    bootstrap_text = bootstrap.read_text(encoding="utf-8")
    for phrase in (
        "ZEPHYR_REPOSITORY_URL",
        "ZEPHYR_REF",
        "mktemp -d",
        '"install-$surface"',
        '"$temporary_parent"/zephyr-bootstrap.*',
    ):
        if phrase not in bootstrap_text:
            fail(f"bootstrap is missing installation element {phrase!r}")

    update_text = harness_scripts[2].read_text(encoding="utf-8")
    for phrase in (
        "refusing to update modified or foreign file",
        "ZEPHYR_BACKUP_DIR",
        "mktemp -d",
        "rollback_update",
        "Previous installation restored",
    ):
        if phrase not in update_text:
            fail(f"updater is missing transactional safety element {phrase!r}")

    dispatcher = ROOT / "harnesses/codex/dispatch.sh"
    result = subprocess.run(["sh", "-n", str(dispatcher)], check=False, capture_output=True, text=True)
    if result.returncode != 0:
        fail(f"{dispatcher}: shell syntax check failed: {result.stderr.strip()}")
    dispatcher_text = dispatcher.read_text(encoding="utf-8")
    for phrase in (
        "--ignore-user-config",
        "--ignore-rules",
        "--ephemeral",
        "--sandbox read-only",
        "--output-schema",
        "--output-last-message",
        "--disable shell_tool",
        "--disable multi_agent",
        "--disable plugins",
        "--disable browser_use",
        "--disable computer_use",
        "--disable workspace_dependencies",
        "stdin and never materialized through the parent agent's tool output",
        "classify_codex_failure",
        "retryable_failure",
        "isolated_codex_home",
        "CODEX_HOME=",
        "stderr_sha256",
        "attempt=2",
    ):
        if phrase not in dispatcher_text:
            fail(f"{dispatcher}: missing isolated transport element {phrase!r}")

    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    for phrase in (
        "make update",
        "make update-claude",
        "make update-all",
        "sh harnesses/install.sh --codex",
        "sh harnesses/install.sh --claude",
        "sh harnesses/update.sh --codex",
        "sh harnesses/update.sh --all",
        "sh harnesses/uninstall.sh --all",
        "make uninstall-skill",
        "make uninstall-cli",
        "make install-codex",
        "make install-claude",
        "make install-all",
        "zephyr harness install codex",
    ):
        if phrase not in readme:
            fail(f"README is missing harness installation instruction {phrase!r}")

    makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
    for phrase in (
        "update: update-codex",
        "update-codex:",
        "update-claude:",
        "update-all:",
        "uninstall:",
        "uninstall-skill:",
        "uninstall-cli:",
        "install-cli:",
        "install-codex:",
        "install-claude:",
        "install-all:",
        "$(MAKE) install",
        "sh harnesses/update.sh --codex",
        "sh harnesses/update.sh --claude",
        "sh harnesses/update.sh --all",
    ):
        if phrase not in makefile:
            fail(f"Makefile is missing update contract {phrase!r}")

    validate_updater_behavior()


def validate_updater_behavior() -> None:
    canonical_temp = Path(tempfile.gettempdir()).resolve()

    with tempfile.TemporaryDirectory(prefix="zephyr-updater-fresh-", dir=canonical_temp) as temporary:
        root = Path(temporary)
        codex_skills = root / "codex-skills"
        codex_agents = root / "codex-agents"
        claude_skills = root / "claude-skills"
        claude_agents = root / "claude-agents"
        backups = root / "backups"
        for path in (codex_skills, codex_agents, claude_skills, claude_agents, backups):
            path.mkdir()

        environment = os.environ.copy()
        environment.update(
            {
                "ZEPHYR_CODEX_SKILLS_DIR": str(codex_skills),
                "ZEPHYR_CODEX_AGENTS_DIR": str(codex_agents),
                "ZEPHYR_CLAUDE_SKILLS_DIR": str(claude_skills),
                "ZEPHYR_CLAUDE_AGENTS_DIR": str(claude_agents),
                "ZEPHYR_BACKUP_DIR": str(backups),
            }
        )
        fresh_install = subprocess.run(
            ["sh", str(ROOT / "harnesses/update.sh"), "--all"],
            check=False,
            capture_output=True,
            text=True,
            env=environment,
        )
        if fresh_install.returncode != 0:
            fail(f"fresh updater install failed: {fresh_install.stderr.strip()}")
        if "Installed Zephyr harness package." not in fresh_install.stdout:
            fail("fresh updater install did not report installation")
        if "Installed Zephyr for" in fresh_install.stdout or "/stage/" in fresh_install.stdout:
            fail("fresh updater install exposed internal staging output")
        if fresh_install.stdout.count("Start a new harness session") != 1:
            fail("fresh updater install must print the session restart reminder exactly once")
        if (codex_skills / "zephyr/SKILL.md").read_bytes() != (ROOT / "harnesses/codex/SKILL.md").read_bytes():
            fail("fresh updater install did not publish the Codex skill")
        if (claude_skills / "zephyr/SKILL.md").read_bytes() != (ROOT / "harnesses/claude-code/SKILL.md").read_bytes():
            fail("fresh updater install did not publish the Claude skill")

    with tempfile.TemporaryDirectory(prefix="zephyr-updater-test-", dir=canonical_temp) as temporary:
        root = Path(temporary)
        codex_skills = root / "codex-skills"
        codex_agents = root / "codex-agents"
        claude_skills = root / "claude-skills"
        claude_agents = root / "claude-agents"
        backups = root / "backups"
        for path in (codex_skills, codex_agents, claude_skills, claude_agents, backups):
            path.mkdir()

        environment = os.environ.copy()
        environment.update(
            {
                "ZEPHYR_CODEX_SKILLS_DIR": str(codex_skills),
                "ZEPHYR_CODEX_AGENTS_DIR": str(codex_agents),
                "ZEPHYR_CLAUDE_SKILLS_DIR": str(claude_skills),
                "ZEPHYR_CLAUDE_AGENTS_DIR": str(claude_agents),
                "ZEPHYR_BACKUP_DIR": str(backups),
            }
        )

        install = subprocess.run(
            ["sh", str(ROOT / "harnesses/install.sh"), "--all"],
            check=False,
            capture_output=True,
            text=True,
            env=environment,
        )
        if install.returncode != 0:
            fail(f"isolated harness install failed: {install.stderr.strip()}")

        def make_installed_skill_older(skill_root: Path, source_asset: str) -> None:
            skill_file = skill_root / "SKILL.md"
            skill_file.write_text(skill_file.read_text(encoding="utf-8") + "\n<!-- previous version -->\n", encoding="utf-8")
            digest = hashlib.sha256(skill_file.read_bytes()).hexdigest()
            manifest = skill_root / "references/assets.sha256"
            lines = manifest.read_text(encoding="utf-8").splitlines()
            replacement = f"{digest}  {source_asset}"
            matches = [index for index, line in enumerate(lines) if line.split(maxsplit=1)[-1] == source_asset]
            if len(matches) != 1:
                fail(f"test manifest has no unique entry for {source_asset}")
            lines[matches[0]] = replacement
            manifest.write_text("\n".join(lines) + "\n", encoding="utf-8")

        make_installed_skill_older(codex_skills / "zephyr", "harnesses/codex/SKILL.md")
        make_installed_skill_older(claude_skills / "zephyr", "harnesses/claude-code/SKILL.md")

        update = subprocess.run(
            ["sh", str(ROOT / "harnesses/update.sh"), "--all"],
            check=False,
            capture_output=True,
            text=True,
            env=environment,
        )
        if update.returncode != 0:
            fail(f"isolated harness update failed: {update.stderr.strip()}")
        if "Installed Zephyr" in update.stdout or "/stage/" in update.stdout:
            fail("updater exposed internal staging installation output")
        if update.stdout.count("Start a new harness session") != 1:
            fail("updater must print the session restart reminder exactly once")
        if (codex_skills / "zephyr/SKILL.md").read_bytes() != (ROOT / "harnesses/codex/SKILL.md").read_bytes():
            fail("updater did not replace the Codex skill")
        if (claude_skills / "zephyr/SKILL.md").read_bytes() != (ROOT / "harnesses/claude-code/SKILL.md").read_bytes():
            fail("updater did not replace the Claude skill")
        if len(list(backups.glob("zephyr-update.*"))) != 1:
            fail("successful update must retain exactly one backup")

        modified_agent = codex_agents / "zephyr-code-reviewer.toml"
        modified_agent.write_text(modified_agent.read_text(encoding="utf-8") + "\n# local edit\n", encoding="utf-8")
        reject = subprocess.run(
            ["sh", str(ROOT / "harnesses/update.sh"), "--codex"],
            check=False,
            capture_output=True,
            text=True,
            env=environment,
        )
        if reject.returncode == 0 or "refusing to update modified or foreign file" not in reject.stderr:
            fail("updater must reject a modified installed agent")
        if "# local edit" not in modified_agent.read_text(encoding="utf-8"):
            fail("updater changed a modified installed agent")

    with tempfile.TemporaryDirectory(prefix="zephyr-updater-rollback-", dir=canonical_temp) as temporary:
        root = Path(temporary)
        codex_skills = root / "codex-skills"
        codex_agents = root / "codex-agents"
        backups = root / "backups"
        fake_bin = root / "fake-bin"
        for path in (codex_skills, codex_agents, backups, fake_bin):
            path.mkdir()

        environment = os.environ.copy()
        environment.update(
            {
                "ZEPHYR_CODEX_SKILLS_DIR": str(codex_skills),
                "ZEPHYR_CODEX_AGENTS_DIR": str(codex_agents),
                "ZEPHYR_BACKUP_DIR": str(backups),
            }
        )
        install = subprocess.run(
            ["sh", str(ROOT / "harnesses/install.sh"), "--codex"],
            check=False,
            capture_output=True,
            text=True,
            env=environment,
        )
        if install.returncode != 0:
            fail(f"rollback fixture install failed: {install.stderr.strip()}")

        fake_mv = fake_bin / "mv"
        fake_mv.write_text(
            """#!/bin/sh
set -eu
count=0
if [ -f "$ZEPHYR_FAKE_MV_COUNT" ]; then
  count=$(sed -n '1p' "$ZEPHYR_FAKE_MV_COUNT")
fi
count=$((count + 1))
printf '%s\n' "$count" > "$ZEPHYR_FAKE_MV_COUNT"
if [ "$count" -eq "$ZEPHYR_FAKE_MV_FAIL_AT" ]; then
  echo "injected mv failure" >&2
  exit 1
fi
exec /bin/mv "$@"
""",
            encoding="utf-8",
        )
        fake_mv.chmod(0o700)
        failure_environment = environment.copy()
        failure_environment["PATH"] = f"{fake_bin}:{failure_environment.get('PATH', '')}"
        failure_environment["ZEPHYR_FAKE_MV_COUNT"] = str(root / "mv-count")
        codex_agent_count = len(list((ROOT / "harnesses/codex/agents").glob("zephyr-*.toml")))
        publish_fail_at = str(codex_agent_count + 3)
        for fail_at in ("3", publish_fail_at):
            Path(failure_environment["ZEPHYR_FAKE_MV_COUNT"]).unlink(missing_ok=True)
            failure_environment["ZEPHYR_FAKE_MV_FAIL_AT"] = fail_at
            update = subprocess.run(
                ["sh", str(ROOT / "harnesses/update.sh"), "--codex"],
                check=False,
                capture_output=True,
                text=True,
                env=failure_environment,
            )
            if update.returncode == 0 or "Previous installation restored" not in update.stderr:
                fail(f"injected updater failure at mv {fail_at} did not roll back: {update.stderr.strip()}")

            verify_restored = subprocess.run(
                ["sh", str(ROOT / "harnesses/install.sh"), "--codex"],
                check=False,
                capture_output=True,
                text=True,
                env=environment,
            )
            if verify_restored.returncode != 0:
                fail(f"rollback after mv {fail_at} did not restore an exact install: {verify_restored.stderr.strip()}")


def validate_codex_dispatcher() -> None:
    dispatcher = ROOT / "harnesses/codex/dispatch.sh"
    with tempfile.TemporaryDirectory(prefix="zephyr-dispatch-test-") as temporary:
        root = Path(temporary)
        packet = root / "review-packet.json"
        packet_bytes = b'{"version":1,"run_id":"run-1","payload":"' + (b"x" * 200_000) + b'"}\n'
        packet.write_bytes(packet_bytes)
        output = root / "candidate.json"
        capture = root / "captured-prompt.txt"
        arguments = root / "arguments.txt"
        home_capture = root / "isolated-home.txt"
        source_home = root / "source-codex-home"
        source_home.mkdir()
        (source_home / "auth.json").write_text("{}\n", encoding="utf-8")
        fake_codex = root / "codex"
        fake_codex.write_text(
            """#!/bin/sh
set -eu
output=
printf '%s\\n' "$@" > "$ZEPHYR_FAKE_ARGUMENTS"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message)
      output=$2
      shift 2
      ;;
    *) shift ;;
  esac
done
[ -n "$output" ]
[ -f "$CODEX_HOME/auth.json" ]
printf '%s\n' "$CODEX_HOME" > "$ZEPHYR_FAKE_HOME_CAPTURE"
command cat > "$ZEPHYR_FAKE_CAPTURE"
printf '{}\\n' > "$output"
""",
            encoding="utf-8",
        )
        fake_codex.chmod(0o700)
        environment = os.environ.copy()
        environment.update(
            {
                "ZEPHYR_CODEX_BIN": str(fake_codex),
                "ZEPHYR_FAKE_CAPTURE": str(capture),
                "ZEPHYR_FAKE_ARGUMENTS": str(arguments),
                "ZEPHYR_FAKE_HOME_CAPTURE": str(home_capture),
                "CODEX_HOME": str(source_home),
            }
        )
        result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--output",
                str(output),
            ],
            check=False,
            capture_output=True,
            env=environment,
        )
        if result.returncode != 0:
            fail(f"Codex dispatcher smoke test failed: {result.stderr.decode(errors='replace').strip()}")
        prompt = capture.read_bytes()
        if prompt.count(packet_bytes) != 1:
            fail("Codex dispatcher did not stream the large packet exactly once")
        packet_hash = hashlib.sha256(packet_bytes).hexdigest().encode()
        expected_header = b"label=review-packet bytes=" + str(len(packet_bytes)).encode() + b" sha256=" + packet_hash
        if expected_header not in prompt:
            fail("Codex dispatcher packet frame lacks exact byte length or SHA-256")
        if output.read_bytes() != b"{}\n":
            fail("Codex dispatcher did not publish the isolated final message")
        isolated_home = Path(home_capture.read_text(encoding="utf-8").strip())
        if isolated_home == source_home or isolated_home.exists():
            fail("Codex dispatcher did not use and clean a private writable CODEX_HOME")
        argv = arguments.read_text(encoding="utf-8")
        for required in ("--ignore-user-config", "--ignore-rules", "--ephemeral", "shell_tool"):
            if required not in argv:
                fail(f"Codex dispatcher invocation lacks {required!r}")

        retry_output = root / "candidate-retry.json"
        retry_count = root / "retry-count"
        retry_codex = root / "codex-retry"
        retry_codex.write_text(
            """#!/bin/sh
set -eu
output=
count=0
if [ -f "$ZEPHYR_FAKE_COUNT" ]; then
  count=$(sed -n '1p' "$ZEPHYR_FAKE_COUNT")
fi
count=$((count + 1))
printf '%s\n' "$count" > "$ZEPHYR_FAKE_COUNT"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message)
      output=$2
      shift 2
      ;;
    *) shift ;;
  esac
done
command cat > /dev/null
if [ "$ZEPHYR_FAKE_MODE" = transient ] && [ "$count" -eq 1 ]; then
  echo "stream disconnected before completion: private diagnostic" >&2
  exit 1
fi
if [ "$ZEPHYR_FAKE_MODE" = auth ]; then
  echo "authentication failed: private diagnostic" >&2
  exit 1
fi
if [ "$ZEPHYR_FAKE_MODE" = sandbox ]; then
  echo "failed to initialize in-process app-server client: Operation not permitted" >&2
  exit 1
fi
printf '{}\n' > "$output"
""",
            encoding="utf-8",
        )
        retry_codex.chmod(0o700)
        retry_environment = os.environ.copy()
        retry_environment.update(
            {
                "ZEPHYR_CODEX_BIN": str(retry_codex),
                "ZEPHYR_FAKE_COUNT": str(retry_count),
                "ZEPHYR_FAKE_MODE": "transient",
            }
        )
        retry_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--output",
                str(retry_output),
            ],
            check=False,
            capture_output=True,
            env=retry_environment,
        )
        if retry_result.returncode != 0 or retry_count.read_text(encoding="utf-8").strip() != "2":
            fail(f"dispatcher did not recover from one transient failure: {retry_result.stderr.decode(errors='replace').strip()}")
        if retry_output.read_bytes() != b"{}\n":
            fail("dispatcher did not publish the transient retry result")

        auth_output = root / "candidate-auth.json"
        retry_count.unlink()
        retry_environment["ZEPHYR_FAKE_MODE"] = "auth"
        auth_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--output",
                str(auth_output),
            ],
            check=False,
            capture_output=True,
            env=retry_environment,
        )
        auth_stderr = auth_result.stderr.decode(errors="replace")
        if auth_result.returncode == 0 or retry_count.read_text(encoding="utf-8").strip() != "1":
            fail("dispatcher must fail fast on authentication errors")
        if "category=auth" not in auth_stderr or "stderr_sha256=" not in auth_stderr:
            fail("dispatcher authentication failure lacks safe diagnostics")
        if "private diagnostic" in auth_stderr:
            fail("dispatcher leaked raw Codex stderr")

        sandbox_output = root / "candidate-sandbox.json"
        retry_count.unlink()
        retry_environment["ZEPHYR_FAKE_MODE"] = "sandbox"
        sandbox_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--output",
                str(sandbox_output),
            ],
            check=False,
            capture_output=True,
            env=retry_environment,
        )
        sandbox_stderr = sandbox_result.stderr.decode(errors="replace")
        if sandbox_result.returncode == 0 or retry_count.read_text(encoding="utf-8").strip() != "1":
            fail("dispatcher must fail fast on sandbox errors")
        if "category=sandbox" not in sandbox_stderr:
            fail("dispatcher sandbox failure lacks a safe category")


def validate_codex_output_schemas() -> None:
    for name in ("candidate-findings.codex.schema.json", "evidence-verdict.codex.schema.json"):
        path = ROOT / "schemas" / name
        document = json.loads(path.read_text(encoding="utf-8"))

        def walk(node: object, context: str) -> None:
            if isinstance(node, dict):
                properties = node.get("properties")
                if node.get("type") == "object" and isinstance(properties, dict):
                    required = node.get("required")
                    if not isinstance(required, list) or set(required) != set(properties):
                        fail(f"{path}: Codex object at {context} must require every property")
                    if node.get("additionalProperties") is not False:
                        fail(f"{path}: Codex object at {context} must forbid additional properties")
                for forbidden in ("allOf", "if", "then", "else", "oneOf"):
                    if forbidden in node:
                        fail(f"{path}: unsupported Codex output-schema keyword at {context}: {forbidden}")
                for key, value in node.items():
                    walk(value, f"{context}/{key}")
            elif isinstance(node, list):
                for index, value in enumerate(node):
                    walk(value, f"{context}/{index}")

        walk(document, "#")


def validate_no_placeholders() -> None:
    roots = (ROOT / "roles", ROOT / "harnesses", ROOT / ".agents", ROOT / ".codex", ROOT / ".claude")
    placeholder_tokens = ("[" + "TODO", "TODO" + ":")
    for base in roots:
        for path in base.rglob("*"):
            if not path.is_file():
                continue
            text = path.read_text(encoding="utf-8")
            if any(token in text for token in placeholder_tokens):
                fail(f"{path}: unresolved placeholder")


def main() -> int:
    try:
        validate_roles()
        validate_asset_manifest()
        validate_skills()
        validate_codex_agents()
        validate_claude_agents()
        validate_installers()
        validate_codex_output_schemas()
        validate_codex_dispatcher()
        validate_no_placeholders()
    except (OSError, UnicodeError, ValueError, tomllib.TOMLDecodeError) as exc:
        print(f"harness validation failed: {exc}", file=sys.stderr)
        return 1
    print("Zephyr role prompts, skills, custom agents, and installers are valid.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
