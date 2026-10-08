// Package codegoblins carries the files a CFO home needs from this
// repository, so the binary can set up a home where there is no checkout.
package codegoblins

import "embed"

// Contract is the CFO's operating contract and the docs the contract links
// to. The binary owns these: every install brings a home's copies up to date.
//
//go:embed AGENTS.md CLAUDE.md docs/pipeline.md docs/native-board.md
var Contract embed.FS

// Skills are the skills Code Goblins ships, one folder each under
// .agents/skills. An install keeps one copy of each in the user's shared
// skills folder, which every harness reads, never in the home.
//
//go:embed .agents/skills
var Skills embed.FS

// Policy is the gate policy and the lane table the binary reads from its
// home. They ship as defaults and then belong to the operator, who tunes
// them, so an install writes them only where they are missing.
//
//go:embed config/pipeline.json data/routing.json
var Policy embed.FS

// Voice is the speech model and engine this build pins for dictation. It
// stays the binary's, so an install of a newer build brings a newer pin; a
// home that keeps a config/voice.json of its own uses that one instead.
//
//go:embed config/voice.json
var Voice []byte
