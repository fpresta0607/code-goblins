// Package codegoblins carries the files a CEO home needs from this
// repository, so the binary can set up a home where there is no checkout.
package codegoblins

import "embed"

// Contract is the CEO's operating contract and the docs the contract links
// to. Installed homes refresh these from the binary; tracked checkout homes
// keep their own copies.
//
//go:embed AGENTS.md CLAUDE.md LICENSE docs/pipeline.md docs/native-board.md docs/remote-access.md docs/load-map.md docs/crawler.md docs/contract docs/commands
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

// Crawler is the crawler connector every goblin carries: its code and the
// lockfile that pins every package it installs. It stays the binary's, so an
// install of a newer build brings the newer connector.
//
//go:embed connectors/crawler/package.json connectors/crawler/package-lock.json connectors/crawler/goblin-crawl.mjs connectors/crawler/crawl-site.mjs
var Crawler embed.FS

// KeyPages are the key page recipes, which stay the binary's: a recipe runs
// only as it was reviewed and merged, so no home keeps a copy of its own.
//
//go:embed config/key-pages.json
var KeyPages []byte
