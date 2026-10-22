// Package skills contains the agent instructions shipped with Watermelon.
package skills

import _ "embed"

// Watermelon is shared by the Codex and Claude Code integrations.
//
//go:embed watermelon/SKILL.md
var Watermelon string
