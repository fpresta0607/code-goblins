// Package codegoblins carries the files a CFO home needs from this
// repository, so the binary can set up a home where there is no checkout.
package codegoblins

import "embed"

// Contract is the CFO's operating contract, its skills and the docs the
// contract links to. The binary owns these: every install brings a home's
// copies up to date.
//
//go:embed AGENTS.md CLAUDE.md .agents/skills docs/pipeline.md docs/native-board.md
var Contract embed.FS

// Policy is the gate policy and the lane table the binary reads from its
// home. They ship as defaults and then belong to the operator, who tunes
// them, so an install writes them only where they are missing.
//
//go:embed config/pipeline.json data/routing.json
var Policy embed.FS
