#!/usr/bin/env python3

from __future__ import annotations

import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import time
import tomllib
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
REVIEWERS = (
    "code-reviewer",
    "architect-reviewer",
    "golang-expert",
    "python-expert",
    "typescript-expert",
    "react-expert",
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
MODEL_PROCESSES = ALL_ROLES + ("semantic-router",)


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
    expected = {f"{name}.md" for name in MODEL_PROCESSES} | {"reviewer-protocol.md"}
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


def validate_reviewer_role_contract() -> None:
    config_text = (ROOT / "internal/config/config.go").read_text(encoding="utf-8")
    constants = dict(re.findall(r"(?m)^\s*(Role[A-Za-z0-9]+)\s*=\s*\"([^\"]+)\"", config_text))
    known_roles = re.search(r"func KnownRoles\(\) \[\]string \{\s*return \[\]string\{(.*?)\n\s*}\s*}", config_text, re.DOTALL)
    if known_roles is None:
        fail("cannot locate config.KnownRoles")
    core_roles = tuple(constants[name] for name in re.findall(r"\b(Role[A-Za-z0-9]+)\b", known_roles.group(1)))
    if core_roles != REVIEWERS:
        fail(f"dispatcher reviewer set differs from config.KnownRoles: core={core_roles}, dispatcher={REVIEWERS}")


def validate_asset_manifest() -> None:
    manifest_path = ROOT / "harnesses/assets.sha256"
    expected = {
        "harnesses/codex/SKILL.md",
        "harnesses/codex/dispatch.sh",
        "harnesses/codex/discovery/SKILL.md",
        "harnesses/codex/discovery/agents/openai.yaml",
    }
    expected.update(path.relative_to(ROOT).as_posix() for path in (ROOT / "roles").glob("*.md"))
    expected.update(path.relative_to(ROOT).as_posix() for path in (ROOT / "schemas").glob("*.json"))
    expected.update(
        path.relative_to(ROOT).as_posix()
        for path in (ROOT / "harnesses/codex/agents").glob("zephyr-*.toml")
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
        ROOT / ".agents/skills/zephyr/SKILL.md",
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
        "zephyr validate-routing",
        "zephyr fallback-routing",
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
    for path in (ROOT / "harnesses/codex/SKILL.md",):
        text = path.read_text(encoding="utf-8")
        for phrase in lifecycle_phrases:
            if phrase not in text:
                fail(f"{path}: choreography is missing {phrase!r}")

    codex_skill = ROOT / "harnesses/codex/SKILL.md"
    codex_skill_text = codex_skill.read_text(encoding="utf-8")
    for phrase in (
        "before `zephyr route`",
        "--source codex-compatibility",
        "unknown enabled Codex feature allowed",
        "unparseable Codex feature output allowed",
        "portable Codex isolation profile selected",
    ):
        if phrase not in codex_skill_text:
            fail(f"{codex_skill}: compatibility choreography is missing {phrase!r}")
    probe_index = codex_skill_text.index("<dispatch-script> probe")
    route_index = codex_skill_text.index("zephyr route --run")
    if probe_index >= route_index:
        fail(f"{codex_skill}: compatibility probe must precede routing")
    pre_route = codex_skill_text[probe_index:route_index]
    for warning in (
        "unknown enabled Codex feature allowed",
        "unparseable Codex feature output allowed",
        "portable Codex isolation profile selected",
    ):
        if warning not in pre_route or "zephyr context limit" not in pre_route:
            fail(f"{codex_skill}: warning {warning!r} is not connected to a pre-route context limit")

    forbidden_choreography = (
        "read its applicable `AGENTS.md`",
        "Read applicable project instructions",
        "otherwise use a fresh default subagent",
        "invoke a fresh generic subagent",
        "effective read-only parent permission mode",
    )
    for path in (ROOT / "harnesses/codex/SKILL.md",):
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
    if {path.stem.removeprefix("zephyr-") for path in files} != set(MODEL_PROCESSES):
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
        
            "ZEPHYR_CODEX_SKILLS_DIR",
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
        "install",
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
        "isolated_features",
        "compatibility probe",
        "ZEPHYR_CODEX_PROBE_TIMEOUT",
        "Codex binary changed after compatibility probe",
        "setsid or Perl",
        "stdin and never materialized through the parent agent's tool output",
        "classify_codex_failure",
        "retryable_failure",
        "isolated_codex_home",
        "CODEX_HOME=",
        "stderr_sha256",
        "attempt=2",
        "dispatch.sh routing",
        "semantic-router-prompt",
        "semantic-routing.codex.schema.json",
    ):
        if phrase not in dispatcher_text:
            fail(f"{dispatcher}: missing isolated transport element {phrase!r}")

    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    for phrase in (
        "make update",
        "make uninstall-skill",
        "make uninstall-cli",
        "make install",
    ):
        if phrase not in readme:
            fail(f"README is missing harness installation instruction {phrase!r}")

    makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
    for phrase in (
        "update:",
        "uninstall:",
        "uninstall-skill:",
        "uninstall-cli:",
        "install-cli:",
        "$(MAKE) install",
        "sh harnesses/update.sh",
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
        backups = root / "backups"
        for path in (codex_skills, codex_agents, backups):
            path.mkdir()

        environment = os.environ.copy()
        environment.update(
            {
                "ZEPHYR_CODEX_SKILLS_DIR": str(codex_skills),
                "ZEPHYR_CODEX_AGENTS_DIR": str(codex_agents),
                "ZEPHYR_BACKUP_DIR": str(backups),
            }
        )
        fresh_install = subprocess.run(
            ["sh", str(ROOT / "harnesses/install.sh")],
            check=False,
            capture_output=True,
            text=True,
            env=environment,
        )
        if fresh_install.returncode != 0:
            fail(f"fresh updater install failed: {fresh_install.stderr.strip()}")
        if "Zephyr для Codex установлен" not in fresh_install.stdout:
            fail("fresh updater install did not report installation")
        if "Zephyr установлен для" in fresh_install.stdout or "/stage/" in fresh_install.stdout:
            fail("fresh updater install exposed internal staging output")
        if fresh_install.stdout.count("Начните новую сессию") != 1:
            fail("fresh updater install must print the session restart reminder exactly once")
        if (codex_skills / "zephyr/SKILL.md").read_bytes() != (ROOT / "harnesses/codex/SKILL.md").read_bytes():
            fail("fresh updater install did not publish the Codex skill")

    with tempfile.TemporaryDirectory(prefix="zephyr-updater-test-", dir=canonical_temp) as temporary:
        root = Path(temporary)
        codex_skills = root / "codex-skills"
        codex_agents = root / "codex-agents"
        backups = root / "backups"
        for path in (codex_skills, codex_agents, backups):
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
            ["sh", str(ROOT / "harnesses/install.sh")],
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

        update = subprocess.run(
            ["sh", str(ROOT / "harnesses/update.sh")],
            check=False,
            capture_output=True,
            text=True,
            env=environment,
        )
        if update.returncode != 0:
            fail(f"isolated harness update failed: {update.stderr.strip()}")
        if "Zephyr для Codex установлен" in update.stdout or "/stage/" in update.stdout:
            fail("updater exposed internal staging installation output")
        if update.stdout.count("Начните новую сессию") != 1:
            fail("updater must print the session restart reminder exactly once")
        if (codex_skills / "zephyr/SKILL.md").read_bytes() != (ROOT / "harnesses/codex/SKILL.md").read_bytes():
            fail("updater did not replace the Codex skill")
        if len(list(backups.glob("zephyr-update.*"))) != 1:
            fail("successful update must retain exactly one backup")

        modified_agent = codex_agents / "zephyr-code-reviewer.toml"
        modified_agent.write_text(modified_agent.read_text(encoding="utf-8") + "\n# local edit\n", encoding="utf-8")
        reject = subprocess.run(
            ["sh", str(ROOT / "harnesses/update.sh")],
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
            ["sh", str(ROOT / "harnesses/install.sh")],
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
                ["sh", str(ROOT / "harnesses/update.sh")],
                check=False,
                capture_output=True,
                text=True,
                env=failure_environment,
            )
            if update.returncode == 0 or "Previous installation restored" not in update.stderr:
                fail(f"injected updater failure at mv {fail_at} did not roll back: {update.stderr.strip()}")

            verify_restored = subprocess.run(
                ["sh", str(ROOT / "harnesses/install.sh")],
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
case "$1" in
  --version)
    [ "$#" -eq 1 ]
    printf 'codex-cli 0.146.0\\n'
    exit 0
    ;;
  features)
    [ "$2" = list ]
    printf '%s\n' "$CODEX_HOME:$HOME" >> "$ZEPHYR_FAKE_HOME_CAPTURE"
    case "${ZEPHYR_FAKE_FEATURE_PROFILE:-new}" in
      old)
        printf '%s\\n' 'apps stable true' 'shell_tool stable true' 'multi_agent stable true'
        ;;
      unknown)
        printf '%s\\n' 'apps stable true' 'future_tool stable true'
        ;;
      mixed-features)
        printf '%s\\n' 'apps stable true' 'future_tool stable true' 'format changed unexpectedly'
        ;;
      unparseable-features)
        printf '%s\\n' 'feature-output-v2 {"apps":true}'
        ;;
      smoke-invalid)
        printf '%s\\n' 'apps stable true'
        ;;
      timeout)
        trap '' TERM
        (
          trap '' TERM
          while :; do
            sleep 30 &
            wait "$!"
          done
        ) &
        printf '%s\\n' "$!" > "$ZEPHYR_FAKE_CHILD_PID"
        wait "$!"
        ;;
      *)
        printf '%s\\n' 'apps stable true' 'auto_compaction stable true' 'shell_tool stable true' 'multi_agent stable true' 'code_mode_buffered_exec under-development false' 'code_mode_host stable true' 'skill_search stable true' 'enable_fanout stable true' 'js_repl stable true' 'js_repl_tools_only stable true' 'multi_agent_mode stable true' 'multi_agent_v2 stable true' 'search_tool stable true' 'web_search_request stable true'
        ;;
    esac
    exit 0
    ;;
  exec)
    shift
    if [ "${1:-}" = --help ]; then
      if [ "${ZEPHYR_FAKE_FEATURE_PROFILE:-new}" = missing-option ]; then
        printf '%s\\n' --ignore-user-config --ignore-rules --ephemeral --sandbox --output-schema --output-last-message --json
      else
        printf '%s\\n' --strict-config --ignore-user-config --ignore-rules --ephemeral --sandbox --output-schema --output-last-message --json
      fi
      exit 0
    fi
    ;;
  *)
    exit 2
    ;;
esac
output=
count=0
if [ -n "${ZEPHYR_FAKE_COUNT:-}" ]; then
  if [ -f "$ZEPHYR_FAKE_COUNT" ]; then
    count=$(sed -n '1p' "$ZEPHYR_FAKE_COUNT")
  fi
  count=$((count + 1))
  printf '%s\\n' "$count" > "$ZEPHYR_FAKE_COUNT"
fi
profile=portable
for argument in "$@"; do
  case "$argument" in
    include_apps_instructions=false) profile=full ;;
  esac
done
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
printf '%s\n' "$CODEX_HOME:$HOME" >> "$ZEPHYR_FAKE_HOME_CAPTURE"
command cat > "$ZEPHYR_FAKE_CAPTURE"
is_smoke=no
case "$output" in
  */smoke-last-message.json) is_smoke=yes ;;
esac
  case "${ZEPHYR_FAKE_MODE:-success}" in
  success)
    ;;
  transient)
    if [ "$is_smoke" = no ] && [ "$count" -eq 1 ]; then
      echo "stream disconnected before completion: private diagnostic" >&2
      exit 1
    fi
    ;;
  smoke-transient)
    if [ "$is_smoke" = yes ] && [ "$count" -eq 1 ]; then
      echo "stream disconnected before completion: private smoke diagnostic" >&2
      exit 1
    fi
    ;;
  smoke-always-transient)
    if [ "$is_smoke" = yes ]; then
      echo "stream disconnected before completion: private smoke diagnostic" >&2
      exit 1
    fi
    ;;
  smoke-invalid)
    if [ "$is_smoke" = yes ]; then
      printf '%s\\n' '[]' > "$output"
      exit 0
    fi
    ;;
  auth)
    if [ "$is_smoke" = no ]; then
      echo "authentication failed: private diagnostic" >&2
      exit 1
    fi
    ;;
  sandbox)
    if [ "$is_smoke" = no ]; then
      echo "failed to initialize in-process app-server client: Operation not permitted" >&2
      exit 1
    fi
    ;;
  full-config-incompatible)
    if [ "$profile" = full ]; then
      echo "invalid configuration: include_apps_instructions" >&2
      exit 2
    fi
    ;;
  all-config-incompatible)
    echo "invalid configuration: approval_policy" >&2
    exit 2
    ;;
  timeout)
    trap '' TERM
    (
      trap '' TERM
      while :; do
        sleep 30 &
        wait "$!"
      done
    ) &
    printf '%s\\n' "$!" >> "$ZEPHYR_FAKE_CHILD_PID"
    wait "$!"
    ;;
  *)
    exit 2
    ;;
esac
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
        policy = root / "model-policy.txt"
        policy_lines = [
            "zephyr-codex-model-policy-v1",
            "probe\t-\tgpt-5.6-luna\tlow\ttrue",
            "semantic-router\t-\tgpt-5.6-luna\tmedium\tfalse",
            "evidence-gate\t-\tgpt-5.6-sol\txhigh\ttrue",
        ]
        policy_lines.extend(
            f"reviewer\t{reviewer}\t{'gpt-5.6-sol' if reviewer == 'security-auditor' else 'gpt-5.6-terra'}\t{'xhigh' if reviewer == 'security-auditor' else 'high'}\t{'true' if reviewer == 'security-auditor' else 'false'}"
            for reviewer in REVIEWERS
        )
        policy.write_text("\n".join(policy_lines) + "\n", encoding="utf-8")
        compatibility = root / "compatibility.txt"
        probe = subprocess.run(
            ["sh", str(dispatcher), "probe", "--output", str(compatibility), "--policy", str(policy)],
            check=False,
            capture_output=True,
            env=environment,
        )
        if probe.returncode != 0:
            fail(f"Codex compatibility probe failed: {probe.stderr.decode(errors='replace').strip()}")
        result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--compat",
                str(compatibility),
                "--policy",
                str(policy),
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
        homes = [line.split(":", 1)[0] for line in home_capture.read_text(encoding="utf-8").splitlines()]
        if not homes or any(Path(home) == source_home or Path(home).exists() for home in homes):
            fail("Codex dispatcher did not use and clean private CODEX_HOME directories")
        argv = arguments.read_text(encoding="utf-8").splitlines()
        for required in ("--ignore-user-config", "--ignore-rules", "--ephemeral"):
            if required not in argv:
                fail(f"Codex dispatcher invocation lacks {required!r}")
        if "--model" not in argv or argv[argv.index("--model") + 1] != "gpt-5.6-terra":
            fail("Codex dispatcher did not apply the reviewer model policy")
        if 'model_reasoning_effort="high"' not in argv:
            fail("Codex dispatcher did not apply the reviewer effort policy")
        if 'service_tier="default"' not in argv or any(argv[index : index + 2] == ["--enable", "fast_mode"] for index in range(len(argv) - 1)):
            fail("Codex dispatcher did not apply the reviewer Fast policy")
        for expected_feature in (
            "auto_compaction",
            "shell_tool",
            "code_mode_buffered_exec",
            "code_mode_host",
            "skill_search",
            "enable_fanout",
            "js_repl",
            "js_repl_tools_only",
            "multi_agent_mode",
            "multi_agent_v2",
            "search_tool",
            "web_search_request",
        ):
            if not any(argv[index : index + 2] == ["--disable", expected_feature] for index in range(len(argv) - 1)):
                fail(f"Codex dispatcher did not disable available feature {expected_feature!r}")

        fast_output = root / "candidate-security-fast.json"
        fast_result = subprocess.run(
            [
                "sh", str(dispatcher), "reviewer", "--role", "security-auditor",
                "--packet", str(packet), "--compat", str(compatibility),
                "--policy", str(policy), "--output", str(fast_output),
            ],
            check=False, capture_output=True, env=environment,
        )
        if fast_result.returncode != 0 or fast_output.read_bytes() != b"{}\n":
            fail(f"Codex dispatcher did not execute a Fast role: {fast_result.stderr.decode(errors='replace')}")
        fast_argv = arguments.read_text(encoding="utf-8").splitlines()
        if "--model" not in fast_argv or fast_argv[fast_argv.index("--model") + 1] != "gpt-5.6-sol":
            fail("Codex dispatcher did not apply the role-specific Fast model")
        if 'model_reasoning_effort="xhigh"' not in fast_argv or 'service_tier="fast"' not in fast_argv:
            fail("Codex dispatcher did not apply Fast reasoning or service tier")
        if not any(fast_argv[index : index + 2] == ["--enable", "fast_mode"] for index in range(len(fast_argv) - 1)):
            fail("Codex dispatcher did not enable Fast mode")

        for reviewer in REVIEWERS:
            if reviewer == "code-reviewer":
                continue
            role_output = root / f"candidate-{reviewer}.json"
            role_result = subprocess.run(
                [
                    "sh",
                    str(dispatcher),
                    "reviewer",
                    "--role",
                    reviewer,
                    "--packet",
                    str(packet),
                    "--compat",
                    str(compatibility),
                    "--policy",
                    str(policy),
                    "--output",
                    str(role_output),
                ],
                check=False,
                capture_output=True,
                env=environment,
            )
            if role_result.returncode != 0 or role_output.read_bytes() != b"{}\n":
                fail(f"Codex dispatcher rejected known reviewer role {reviewer!r}: {role_result.stderr.decode(errors='replace').strip()}")

        request = root / "routing-request.json"
        request_bytes = b'{"version":1,"run_id":"run-1","candidates":[]}\n'
        request.write_bytes(request_bytes)
        routing_output = root / "routing-result.json"
        routing_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "routing",
                "--packet",
                str(packet),
                "--request",
                str(request),
                "--compat",
                str(compatibility),
                "--policy",
                str(policy),
                "--output",
                str(routing_output),
            ],
            check=False,
            capture_output=True,
            env=environment,
        )
        if routing_result.returncode != 0 or routing_output.read_bytes() != b"{}\n":
            fail(f"Codex semantic routing dispatcher smoke test failed: {routing_result.stderr.decode(errors='replace').strip()}")
        routing_prompt = capture.read_bytes()
        if routing_prompt.count(packet_bytes) != 1 or routing_prompt.count(request_bytes) != 1:
            fail("Codex semantic routing dispatcher did not stream exact packet and request blocks")
        routing_argv = arguments.read_text(encoding="utf-8")
        if "semantic-routing.codex.schema.json" not in routing_argv:
            fail("Codex semantic routing dispatcher did not enforce the routing output schema")

        routing_failure_output = root / "routing-process-failure.json"
        routing_failure_environment = environment.copy()
        routing_failure_environment["ZEPHYR_FAKE_MODE"] = "auth"
        routing_failure = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "routing",
                "--packet",
                str(packet),
                "--request",
                str(request),
                "--compat",
                str(compatibility),
                "--policy",
                str(policy),
                "--output",
                str(routing_failure_output),
            ],
            check=False,
            capture_output=True,
            env=routing_failure_environment,
        )
        if routing_failure.returncode == 0 or routing_failure_output.exists():
            fail("Codex routing dispatcher published output after process failure")
        if "category=auth" not in routing_failure.stderr.decode(errors="replace"):
            fail("Codex routing process failure lacks a safe category")

        routing_timeout_output = root / "routing-timeout.json"
        routing_timeout_pids = root / "routing-timeout-child.pids"
        routing_timeout_environment = environment.copy()
        routing_timeout_environment.update(
            {
                "ZEPHYR_FAKE_MODE": "timeout",
                "ZEPHYR_FAKE_CHILD_PID": str(routing_timeout_pids),
                "ZEPHYR_CODEX_DISPATCH_TIMEOUT": "1",
            }
        )
        routing_timeout = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "routing",
                "--packet",
                str(packet),
                "--request",
                str(request),
                "--compat",
                str(compatibility),
                "--policy",
                str(policy),
                "--output",
                str(routing_timeout_output),
            ],
            check=False,
            capture_output=True,
            env=routing_timeout_environment,
            timeout=15,
        )
        routing_timeout_stderr = routing_timeout.stderr.decode(errors="replace")
        if routing_timeout.returncode == 0 or routing_timeout_output.exists() or "category=timeout" not in routing_timeout_stderr:
            fail(f"Codex routing timeout was not bounded safely: {routing_timeout_stderr!r}")
        for raw_pid in routing_timeout_pids.read_text(encoding="utf-8").splitlines():
            child_pid = int(raw_pid)
            for _ in range(20):
                try:
                    os.kill(child_pid, 0)
                except ProcessLookupError:
                    break
                time.sleep(0.05)
            else:
                fail("Codex routing timeout left a TERM-ignoring child process running")

        old_environment = environment.copy()
        old_arguments = root / "old-arguments.txt"
        old_environment.update(
            {
                "ZEPHYR_FAKE_FEATURE_PROFILE": "old",
                "ZEPHYR_FAKE_ARGUMENTS": str(old_arguments),
            }
        )
        old_compatibility = root / "old-compatibility.txt"
        old_probe = subprocess.run(
            ["sh", str(dispatcher), "probe", "--output", str(old_compatibility)],
            check=False,
            capture_output=True,
            env=old_environment,
        )
        if old_probe.returncode != 0:
            fail(f"Codex old-version compatibility probe failed: {old_probe.stderr.decode(errors='replace').strip()}")
        old_output = root / "candidate-old.json"
        old_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--compat",
                str(old_compatibility),
                "--output",
                str(old_output),
            ],
            check=False,
            capture_output=True,
            env=old_environment,
        )
        if old_result.returncode != 0:
            fail(f"Codex old-version dispatcher smoke test failed: {old_result.stderr.decode(errors='replace').strip()}")
        old_argv = old_arguments.read_text(encoding="utf-8")
        for unsupported_feature in ("code_mode_buffered_exec", "code_mode_host", "skill_search"):
            if unsupported_feature in old_argv:
                fail(f"Codex dispatcher passed unavailable old-version feature {unsupported_feature!r}")

        changed_codex = root / "codex-changed"
        changed_codex.write_text(fake_codex.read_text(encoding="utf-8") + "\n# different binary\n", encoding="utf-8")
        changed_codex.chmod(0o700)
        changed_environment = environment.copy()
        changed_environment["ZEPHYR_CODEX_BIN"] = str(changed_codex)
        changed_output = root / "candidate-changed-binary.json"
        changed_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--compat",
                str(compatibility),
                "--policy",
                str(policy),
                "--output",
                str(changed_output),
            ],
            check=False,
            capture_output=True,
            env=changed_environment,
        )
        if changed_result.returncode == 0 or "Codex binary changed after compatibility probe" not in changed_result.stderr.decode(errors="replace"):
            fail("Codex dispatcher accepted a binary changed after compatibility probe")

        descriptor_lines = compatibility.read_bytes().splitlines(keepends=True)
        descriptor_payload_lines = [descriptor_lines[0], descriptor_lines[2], descriptor_lines[1], descriptor_lines[3], descriptor_lines[4], *descriptor_lines[6:]]
        reordered_payload = b"".join(descriptor_payload_lines)
        reordered_hash = hashlib.sha256(reordered_payload).hexdigest().encode()
        reordered_compatibility = root / "reordered-compatibility.txt"
        reordered_compatibility.write_bytes(
            b"".join(
                [
                    *descriptor_payload_lines[:5],
                    b"descriptor_sha256=" + reordered_hash + b"\n",
                    *descriptor_payload_lines[5:],
                ]
            )
        )
        reordered_output = root / "candidate-reordered-compatibility.json"
        reordered_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--compat",
                str(reordered_compatibility),
                "--policy",
                str(policy),
                "--output",
                str(reordered_output),
            ],
            check=False,
            capture_output=True,
            env=environment,
        )
        if reordered_result.returncode != 0 or reordered_output.read_bytes() != b"{}\n":
            fail(f"Codex dispatcher still depends on descriptor field positions: {reordered_result.stderr.decode(errors='replace')}")

        tampered_compatibility = root / "tampered-compatibility.txt"
        tampered_compatibility.write_bytes(compatibility.read_bytes().replace(b"apps=true", b"apps=false", 1))
        tampered_output = root / "candidate-tampered-compatibility.json"
        tampered_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--compat",
                str(tampered_compatibility),
                "--policy",
                str(policy),
                "--output",
                str(tampered_output),
            ],
            check=False,
            capture_output=True,
            env=environment,
        )
        if tampered_result.returncode == 0 or "compatibility descriptor feature hash mismatch" not in tampered_result.stderr.decode(errors="replace"):
            fail("Codex dispatcher accepted a tampered compatibility descriptor")

        changed_policy = root / "model-policy-changed.txt"
        changed_policy.write_bytes(policy.read_bytes().replace(b"gpt-5.6-terra", b"gpt-5.6-lunar", 1))
        changed_policy_output = root / "candidate-changed-policy.json"
        changed_policy_result = subprocess.run(
            [
                "sh", str(dispatcher), "reviewer", "--role", "code-reviewer",
                "--packet", str(packet), "--compat", str(compatibility),
                "--policy", str(changed_policy), "--output", str(changed_policy_output),
            ],
            check=False, capture_output=True, env=environment,
        )
        if changed_policy_result.returncode == 0 or "model policy changed after compatibility probe" not in changed_policy_result.stderr.decode(errors="replace"):
            fail("Codex dispatcher accepted a policy changed after compatibility probe")

        unknown_environment = environment.copy()
        unknown_arguments = root / "unknown-arguments.txt"
        unknown_environment.update(
            {
                "ZEPHYR_FAKE_FEATURE_PROFILE": "unknown",
                "ZEPHYR_FAKE_ARGUMENTS": str(unknown_arguments),
            }
        )
        unknown_compatibility = root / "unknown-compatibility.txt"
        unknown_probe = subprocess.run(
            ["sh", str(dispatcher), "probe", "--output", str(unknown_compatibility)],
            check=False,
            capture_output=True,
            env=unknown_environment,
        )
        unknown_stderr = unknown_probe.stderr.decode(errors="replace")
        if unknown_probe.returncode != 0:
            fail(f"Codex compatibility probe rejected an unknown enabled feature: {unknown_stderr}")
        if "warning: unknown enabled feature allowed: future_tool" not in unknown_stderr:
            fail("Codex compatibility probe omitted the named unknown-feature warning")
        unknown_output = root / "candidate-unknown.json"
        unknown_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--compat",
                str(unknown_compatibility),
                "--output",
                str(unknown_output),
            ],
            check=False,
            capture_output=True,
            env=unknown_environment,
        )
        if unknown_result.returncode != 0:
            fail(f"Codex dispatcher rejected a descriptor with an unknown enabled feature: {unknown_result.stderr.decode(errors='replace')}")
        unknown_argv = unknown_arguments.read_text(encoding="utf-8").splitlines()
        if any(unknown_argv[index : index + 2] == ["--disable", "future_tool"] for index in range(len(unknown_argv) - 1)):
            fail("Codex dispatcher disabled an unknown feature without an isolation policy")
        if not any(unknown_argv[index : index + 2] == ["--disable", "apps"] for index in range(len(unknown_argv) - 1)):
            fail("Codex dispatcher stopped disabling known features after a compatibility warning")

        for profile, parsed, ignored, expect_known_disable in (
            ("mixed-features", 2, 1, True),
            ("unparseable-features", 0, 1, False),
        ):
            profile_arguments = root / f"{profile}-arguments.txt"
            profile_environment = environment.copy()
            profile_environment.update(
                {
                    "ZEPHYR_FAKE_FEATURE_PROFILE": profile,
                    "ZEPHYR_FAKE_ARGUMENTS": str(profile_arguments),
                }
            )
            profile_compatibility = root / f"{profile}-compatibility.txt"
            profile_probe = subprocess.run(
                ["sh", str(dispatcher), "probe", "--output", str(profile_compatibility)],
                check=False,
                capture_output=True,
                env=profile_environment,
            )
            profile_stderr = profile_probe.stderr.decode(errors="replace")
            diagnostic_pattern = rf"warning: partially unparseable feature output allowed: parsed={parsed} ignored={ignored} raw_sha256=[0-9a-f]{{64}}"
            if profile_probe.returncode != 0:
                fail(f"Codex compatibility probe rejected {profile!r} output: {profile_stderr}")
            if re.search(diagnostic_pattern, profile_stderr) is None:
                fail(f"Codex compatibility probe omitted sanitized parser diagnostics for {profile!r}: {profile_stderr!r}")
            if "format changed unexpectedly" in profile_stderr or "feature-output-v2" in profile_stderr:
                fail(f"Codex compatibility probe leaked raw feature output for {profile!r}")
            profile_output = root / f"candidate-{profile}.json"
            profile_result = subprocess.run(
                [
                    "sh",
                    str(dispatcher),
                    "reviewer",
                    "--role",
                    "code-reviewer",
                    "--packet",
                    str(packet),
                    "--compat",
                    str(profile_compatibility),
                    "--output",
                    str(profile_output),
                ],
                check=False,
                capture_output=True,
                env=profile_environment,
            )
            if profile_result.returncode != 0:
                fail(f"Codex dispatcher rejected {profile!r} compatibility descriptor: {profile_result.stderr.decode(errors='replace')}")
            profile_argv = profile_arguments.read_text(encoding="utf-8").splitlines()
            has_known_disable = any(
                profile_argv[index : index + 2] == ["--disable", "apps"] for index in range(len(profile_argv) - 1)
            )
            if has_known_disable != expect_known_disable:
                fail(f"Codex dispatcher used the wrong known-feature disables for {profile!r}")

        invalid_environment = environment.copy()
        invalid_environment["ZEPHYR_FAKE_MODE"] = "smoke-invalid"
        invalid_compatibility = root / "invalid-smoke-compatibility.txt"
        invalid_probe = subprocess.run(
            ["sh", str(dispatcher), "probe", "--output", str(invalid_compatibility)],
            check=False,
            capture_output=True,
            env=invalid_environment,
        )
        invalid_stderr = invalid_probe.stderr.decode(errors="replace")
        if invalid_probe.returncode == 0 or invalid_compatibility.exists():
            fail("Codex compatibility probe accepted a non-object smoke result")
        if "compatibility runtime smoke" not in invalid_stderr:
            fail(f"Codex invalid smoke failure lacks a safe diagnostic: {invalid_stderr!r}")

        smoke_retry_environment = environment.copy()
        smoke_retry_count = root / "smoke-retry-count"
        smoke_retry_environment.update(
            {
                "ZEPHYR_FAKE_COUNT": str(smoke_retry_count),
                "ZEPHYR_FAKE_MODE": "smoke-transient",
            }
        )
        smoke_retry_compatibility = root / "smoke-retry-compatibility.txt"
        smoke_retry_probe = subprocess.run(
            ["sh", str(dispatcher), "probe", "--output", str(smoke_retry_compatibility)],
            check=False,
            capture_output=True,
            env=smoke_retry_environment,
        )
        if smoke_retry_probe.returncode != 0 or smoke_retry_count.read_text(encoding="utf-8").strip() != "2":
            fail("Codex compatibility probe did not retry and recover from a transient smoke failure")

        smoke_exhausted_environment = environment.copy()
        smoke_exhausted_count = root / "smoke-exhausted-count"
        smoke_exhausted_environment.update(
            {
                "ZEPHYR_FAKE_COUNT": str(smoke_exhausted_count),
                "ZEPHYR_FAKE_MODE": "smoke-always-transient",
            }
        )
        smoke_exhausted_compatibility = root / "smoke-exhausted-compatibility.txt"
        smoke_exhausted_probe = subprocess.run(
            ["sh", str(dispatcher), "probe", "--output", str(smoke_exhausted_compatibility)],
            check=False,
            capture_output=True,
            env=smoke_exhausted_environment,
        )
        smoke_exhausted_stderr = smoke_exhausted_probe.stderr.decode(errors="replace")
        if smoke_exhausted_probe.returncode == 0 or smoke_exhausted_count.read_text(encoding="utf-8").strip() != "2":
            fail("Codex compatibility probe did not exhaust two transient smoke attempts")
        if "after 2 attempts" not in smoke_exhausted_stderr or "private smoke diagnostic" in smoke_exhausted_stderr:
            fail("Codex exhausted smoke retry leaked raw diagnostics or wrong attempt count")

        portable_arguments = root / "portable-arguments.txt"
        portable_environment = environment.copy()
        portable_environment.update(
            {
                "ZEPHYR_FAKE_MODE": "full-config-incompatible",
                "ZEPHYR_FAKE_ARGUMENTS": str(portable_arguments),
            }
        )
        portable_compatibility = root / "portable-compatibility.txt"
        portable_probe = subprocess.run(
            ["sh", str(dispatcher), "probe", "--output", str(portable_compatibility)],
            check=False,
            capture_output=True,
            env=portable_environment,
        )
        portable_stderr = portable_probe.stderr.decode(errors="replace")
        if portable_probe.returncode != 0:
            fail(f"Codex compatibility probe did not fall back to the portable profile: {portable_stderr}")
        if "warning: full Codex isolation profile unsupported; portable profile selected" not in portable_stderr:
            fail("Codex compatibility probe omitted the portable-profile warning")
        if portable_compatibility.read_text(encoding="utf-8").splitlines().count("profile=portable") != 1:
            fail("Codex compatibility descriptor did not freeze the portable profile")
        if portable_compatibility.read_text(encoding="utf-8").splitlines().count("omitted_config=include_apps_instructions") != 1:
            fail("Codex compatibility descriptor did not freeze the single omitted portable setting")
        portable_output = root / "candidate-portable.json"
        portable_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--compat",
                str(portable_compatibility),
                "--output",
                str(portable_output),
            ],
            check=False,
            capture_output=True,
            env=portable_environment,
        )
        if portable_result.returncode != 0:
            fail(f"Codex dispatcher did not reuse the frozen portable profile: {portable_result.stderr.decode(errors='replace')}")
        portable_argv = portable_arguments.read_text(encoding="utf-8").splitlines()
        if "include_apps_instructions=false" in portable_argv:
            fail("Codex portable profile retained a version-volatile full-profile setting")
        for required_argument in (
            "--strict-config",
            "--ignore-user-config",
            'approval_policy="never"',
            'web_search="disabled"',
            'include_environment_context=false',
            'allow_login_shell=false',
            'apps={ _default = { enabled = false, destructive_enabled = false, open_world_enabled = false } }',
            'memories={ use_memories = false, generate_memories = false, dedicated_tools = false }',
        ):
            if required_argument not in portable_argv:
                fail(f"Codex portable profile omitted required argument {required_argument!r}")
        if 'include_apps_instructions=false' in portable_argv:
            fail("Codex portable profile retained the unsupported configuration setting")
        if not any(portable_argv[index : index + 2] == ["--sandbox", "read-only"] for index in range(len(portable_argv) - 1)):
            fail("Codex portable profile omitted read-only sandboxing")
        if not any(portable_argv[index : index + 2] == ["--disable", "apps"] for index in range(len(portable_argv) - 1)):
            fail("Codex portable profile omitted known feature isolation")

        incompatible_environment = environment.copy()
        incompatible_environment["ZEPHYR_FAKE_MODE"] = "all-config-incompatible"
        incompatible_compatibility = root / "incompatible-compatibility.txt"
        incompatible_probe = subprocess.run(
            ["sh", str(dispatcher), "probe", "--output", str(incompatible_compatibility)],
            check=False,
            capture_output=True,
            env=incompatible_environment,
        )
        incompatible_stderr = incompatible_probe.stderr.decode(errors="replace")
        if incompatible_probe.returncode == 0 or incompatible_compatibility.exists():
            fail("Codex compatibility probe accepted an incompatible portable profile")
        if "category=config" not in incompatible_stderr or "stderr_sha256=" not in incompatible_stderr:
            fail(f"Codex portable-profile failure lacks sanitized config diagnostics: {incompatible_stderr!r}")
        if "invalid configuration" in incompatible_stderr or "approval_policy" in incompatible_stderr:
            fail("Codex portable-profile failure leaked raw stderr")

        tampered_profile = root / "tampered-profile.txt"
        tampered_profile.write_bytes(compatibility.read_bytes().replace(b"profile=full", b"profile=portable", 1))
        tampered_profile_output = root / "candidate-tampered-profile.json"
        tampered_profile_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--compat",
                str(tampered_profile),
                "--policy",
                str(policy),
                "--output",
                str(tampered_profile_output),
            ],
            check=False,
            capture_output=True,
            env=environment,
        )
        tampered_profile_stderr = tampered_profile_result.stderr.decode(errors="replace")
        if tampered_profile_result.returncode == 0 or not any(
            reason in tampered_profile_stderr
            for reason in ("compatibility descriptor hash mismatch", "malformed compatibility descriptor")
        ):
            fail("Codex dispatcher accepted a tampered compatibility profile")

        missing_option_environment = environment.copy()
        missing_option_environment["ZEPHYR_FAKE_FEATURE_PROFILE"] = "missing-option"
        missing_option_compatibility = root / "missing-option-compatibility.txt"
        missing_option_probe = subprocess.run(
            ["sh", str(dispatcher), "probe", "--output", str(missing_option_compatibility)],
            check=False,
            capture_output=True,
            env=missing_option_environment,
        )
        missing_option_stderr = missing_option_probe.stderr.decode(errors="replace")
        if (
            missing_option_probe.returncode == 0
            or "unsupported Codex CLI option --strict-config" not in missing_option_stderr
            or missing_option_compatibility.exists()
        ):
            fail("Codex compatibility probe did not reject a missing required option")

        timeout_environment = environment.copy()
        timeout_child_pid = root / "timeout-child.pid"
        timeout_environment.update(
            {
                "ZEPHYR_FAKE_FEATURE_PROFILE": "timeout",
                "ZEPHYR_CODEX_PROBE_TIMEOUT": "1",
                "ZEPHYR_FAKE_CHILD_PID": str(timeout_child_pid),
            }
        )
        timeout_compatibility = root / "timeout-compatibility.txt"
        timeout_probe = subprocess.run(
            ["sh", str(dispatcher), "probe", "--output", str(timeout_compatibility)],
            check=False,
            capture_output=True,
            env=timeout_environment,
            timeout=8,
        )
        timeout_stderr = timeout_probe.stderr.decode(errors="replace")
        if timeout_probe.returncode == 0 or "category=transport" not in timeout_stderr or "after 2 attempts" not in timeout_stderr:
            fail(f"Codex compatibility probe did not bound and retry a timeout: status={timeout_probe.returncode}, stderr={timeout_stderr!r}")
        child_pid = int(timeout_child_pid.read_text(encoding="utf-8").strip())
        for _ in range(20):
            try:
                os.kill(child_pid, 0)
            except ProcessLookupError:
                break
            time.sleep(0.05)
        else:
            fail("Codex compatibility probe left a TERM-ignoring child process running")

        retry_output = root / "candidate-retry.json"
        retry_count = root / "retry-count"
        retry_environment = environment.copy()
        retry_environment.update(
            {
                "ZEPHYR_FAKE_COUNT": str(retry_count),
                "ZEPHYR_FAKE_MODE": "transient",
            }
        )
        retry_compatibility = root / "retry-compatibility.txt"
        retry_probe = subprocess.run(
            ["sh", str(dispatcher), "probe", "--output", str(retry_compatibility)],
            check=False,
            capture_output=True,
            env=retry_environment,
        )
        if retry_probe.returncode != 0:
            fail(f"Codex retry compatibility probe failed: {retry_probe.stderr.decode(errors='replace').strip()}")
        retry_result = subprocess.run(
            [
                "sh",
                str(dispatcher),
                "reviewer",
                "--role",
                "code-reviewer",
                "--packet",
                str(packet),
                "--compat",
                str(retry_compatibility),
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
                "--compat",
                str(retry_compatibility),
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
                "--compat",
                str(retry_compatibility),
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
    for name in ("candidate-findings.codex.schema.json", "evidence-verdict.codex.schema.json", "semantic-routing.codex.schema.json"):
        path = ROOT / "schemas" / name
        document = json.loads(path.read_text(encoding="utf-8"))

        version = document.get("properties", {}).get("version", {})
        if version.get("type") != "integer":
            fail(f"{path}: Codex root version property must declare type integer")

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
    roots = (ROOT / "roles", ROOT / "harnesses", ROOT / ".agents", ROOT / ".codex")
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
        validate_reviewer_role_contract()
        validate_asset_manifest()
        validate_skills()
        validate_codex_agents()
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
