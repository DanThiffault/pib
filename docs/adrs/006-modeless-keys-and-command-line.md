# ADR-006: Modeless keys, a command line, and a drill-down table

## Status
Proposed. Supersedes the key handling described in [ADR-003](003-horizontal-tui-layout.md)
and the `screenNewPlan` prompt; keeps ADR-003's vertical stacking.

## Context

The TUI's keys grew by accretion. `s` means "start" on one screen and "start all" on
another; `esc` is resolved in three places; `b` is dead on the root screen; seven of the
eight per-state actions in the bar (`o e c l v k f r`) print a notice and do nothing.
Bindings are declared in three files and matched two different ways. Nothing can be
reasoned about, so it is being removed and redone.

The redesign is around a live list where the user selects a row and issues a command.
Two questions had to be settled first: whether vim-style motion forces vim-style modes,
and what the screens look like.

PINE had no modes: single-key commands and an always-visible menu at the bottom that
was the manual. mutt is PINE with vim motion — `j/k`, single-letter commands, `:` for
the long tail — and also no modes, because every piece of free text is typed in
`$EDITOR`, not in the TUI. tig, ranger, lazygit, k9s and aerc all follow that pattern.
Modes exist to type text; a TUI that never edits text does not need them.

## Decision

### 1. No modes. Motion letters are reserved; everything else is a command

All free-text input leaves the TUI: comments, followups, answers and edits go to
`$EDITOR` (ADR-007); the new-plan command opens the planner in its own tmux window.
The TUI therefore has exactly two kinds of key:

**Motion**, global and never rebound:

| key | |
|---|---|
| `j` `k` `↑` `↓` | move |
| `g` `G` | top / bottom |
| `ctrl+d` `ctrl+u` `pgdn` `pgup` | page |
| `l` `→` `enter` | drill in (plan → its issues → issue detail) |
| `h` `←` `esc` | back out |
| `q` | back out; quit from the root |
| `?` | help |
| `:` | command line |

**Commands**, single lower-case letters, meaning the same thing on every screen:

| key | verb | applies to |
|---|---|---|
| `n` | `new` | anywhere — spawn a planner |
| `s` | `start` | launchable issue; on a plan row, every launchable issue in it |
| `v` | `review` | plan (opening or closing review), never an issue |
| `r` | `retry` | needs attention — run the issue's agent again |
| `a` | `answer` | needs attention with `needs_input` |
| `f` | `followup` | in progress / awaiting review issue |
| `ctrl+k` | `kill` | any row with an open run (k9s precedent) |
| `w` | `window` | jump to the tmux window of the row's run |
| `c` | `comment` | any issue |
| `e` | `edit` | any open issue |
| `x` | `close` | any open issue (gh-dash precedent; `d` reads as delete) |
| `X` | `reopen` | closed issue |
| `p` | `pr` | issue with a linked PR — open it in the browser |
| `b` | `blockers` | blocked issue — jump to the first open blocker |
| `z` | `archive` / `unarchive` | complete plan / archived plan |
| `,` | `settings` | anywhere (GUI `Cmd+,` convention) |

Filters are toggles, not searches: `Z` shows/hides closed issues and archived plans,
`!` shows only rows that need the user (launchable or needs attention). `/` is
deliberately absent; it would cost `n`.

A command is a record — verb, key, label, predicate over the selected row, handler —
in one registry. The bottom bar, the help screen and the `:` line are all rendered
from that registry filtered by the predicate, so they cannot disagree with what a key
does.

### 2. The `:` line names the CLI verbs

`:` opens a one-line prompt at the bottom. It accepts every verb in the registry by its
CLI name — `:start`, `:close`, `:followup`, `:archive` — with tab completion, and
settings assignments (`:set types.task coder`, ADR-008). The vocabulary is shared with
`pib issue …` and `pib plan …` so what works in a shell works in the TUI. The `:` line
is the one exception to "no text in the TUI", and it is a single line with no editing
beyond backspace.

### 3. k9s-style drill-down tables

Three screens, each a full-width table with a breadcrumb above and the command bar
below. `enter`/`l` replaces the table with its child; `esc`/`h` returns, cursor kept.

```
pib › orders                                          3 ready · 1 needs you
─────────────────────────────────────────────────────────────────────────────
  #   TYPE       TITLE                          STATE              AGENT
▶ 12  task       Order schema                   launchable         coder
  13  task       Order aggregate                blocked #12
  14  research   Pick an event store            needs attention    researcher
  15  plan-rev   Review: orders                 closed
─────────────────────────────────────────────────────────────────────────────
  #14 · Pick an event store · researcher asked a question 4m ago

  Should the projection be rebuilt on every start, or …
─────────────────────────────────────────────────────────────────────────────
 s Start  e Edit  c Comment  x Close  n New  : Cmd  ? Help
```

- **Plans** — one row per plan (including `planning` placeholders), columns: title,
  state, counts (ready / running / needs attention). Detail pane: the selected plan's
  goal and acceptance.
- **Issues** — the selected plan's issues, columns: number, type, title, state (with
  blocker or attention reason), agent, PR. Detail pane: the selected issue's summary and
  most recent comment.
- **Issue** — full-height issue detail, scrollable, same command set as its row.

Rows that need the user sort to the top of each table. The detail pane takes the
bottom third; on short terminals (`isShort`) it is dropped and the tables get the height.

### 4. Startup prompts are out of the registry, for now

The `y/n` install / update / create prompts keep their own keys. Each one is retired
when the settings screen (ADR-008) can do the same job.

## Consequences

Positive: one registry, one key per verb, no overloading, help that is always correct,
and a vocabulary shared with the CLI. Removing the textarea removes the only reason the
TUI would ever have needed an insert mode.

Negative: `n` is taken by `new`, so there is no `/` search — filters are toggles. Users
who expect vim's `n`/`N` will not find them. Opening `$EDITOR` for a one-line comment
is heavier than an inline box; ADR-007 makes it worth it.
