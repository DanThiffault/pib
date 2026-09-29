# ADR-008: Settings screen

## Status
Proposed. Depends on [ADR-006](006-modeless-keys-and-command-line.md).

## Context

Configuration lives in three places and is edited by hand: the type → agent map and
`[plan]`/`[review]` knobs in `~/.pib/config.toml` (overridden by `.pib/config.toml`),
and each agent's model and thinking level in the frontmatter of `~/.pib/agents/*.md`.
The startup `y/n` prompts (install agents, update agents, create workspace) are the only
UI any of this has, and they use a key vocabulary of their own.

## Decision

### 1. One more drill-down table, reached by `,` or `:settings`

The settings screen is a table like the others (ADR-006 §3), grouped into sections,
using the same motion keys, command bar and `:` line:

```
pib › settings
─────────────────────────────────────────────────────────────────────────────
  TYPES                          agent                       from
▶ task                           coder                       ~/.pib
  research                       researcher                  ~/.pib
  reviewer                       code-reviewer  (deprecated) .pib
  PLAN
  review                         true
  isolate                        true
  REVIEW
  cycles                         3
  AGENTS                         model                       thinking
  coder                          claude-sonnet-4-5           medium
  planner                        claude-opus-4-1             high
─────────────────────────────────────────────────────────────────────────────
 e Edit  u Update agents  :set key value
```

- `e` on a row opens a one-line `:set <key> ` prefilled in the command line; enter
  writes it. `:set types.task coder`, `:set review.cycles 2`, `:set agents.coder.model …`.
- The `from` column says which file a value came from; writes go to the workspace file
  for `types`/`plan`/`review` (it is the override layer) and to the agent's markdown for
  `agents.*`.
- `u` (`:update-agents`) re-runs the embedded-defaults comparison the startup prompt
  does today, showing which definitions differ and letting the user accept per agent.

### 2. Startup prompts retire as settings replaces them

The "update agents?" prompt is removed in the same change that adds `u`. "Install
agents?" and "create workspace?" stay: they run before there is a store to show a table
against.

### 3. Writes go through the existing loaders

`internal/config` gains a `Set(path, key, value)` that rewrites one key while keeping
the file's comments; `internal/agent` gains the equivalent for frontmatter. Neither
grows a general TOML or YAML writer — the files are flat `key = value` and
`key: value`, and the existing parsers already assume that.

## Consequences

Positive: the type map, review depth and models are editable without leaving pib, in the
same vocabulary as everything else, and one startup prompt disappears.

Negative: two small file rewriters that must preserve hand-written comments. Config
changes take effect on the next spawn, not for running agents — the screen says so.
