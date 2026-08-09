package zephyrassets

import "embed"

//go:embed harnesses/assets.sha256 harnesses/codex/SKILL.md harnesses/codex/dispatch.sh harnesses/codex/discovery/SKILL.md harnesses/codex/discovery/agents/openai.yaml harnesses/codex/agents/*.toml harnesses/claude-code/SKILL.md harnesses/claude-code/discovery/SKILL.md harnesses/claude-code/agents/*.md harnesses/opencode/SKILL.md harnesses/opencode/agents/*.md harnesses/opencode/dispatch.sh harnesses/opencode/sync-agents.sh roles/*.md schemas/*.json
var Harness embed.FS
