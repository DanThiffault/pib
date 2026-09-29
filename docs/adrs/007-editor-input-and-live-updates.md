# ADR-007: `$EDITOR` for text, store events for liveness

## Status
Proposed. Companion to [ADR-006](006-modeless-keys-and-command-line.md).

## Context

Two mechanics the redesign depends on and the TUI does not have.

**Text entry.** Comments, followups, answers to an agent's question and edits all need
more than a line of text, and ADR-006 rules out typing it inside the TUI.

**Liveness.** The tables are supposed to be live, but nothing pushes. `internal/ui`
re-reads the store on a 3-second tick, 1-second while it knows an agent is running,
and the plan list never refreshes at all. Yet almost every change comes from inside the
process: the CLI talks to the running pib over the socket, agents finish inside
`runner.Runner`, reviews are recorded through `Store`. The only source of truth pib does
not own is GitHub's view of a pull request, and `internal/recheck` already polls that.

## Decision

### 1. Free text goes to `$EDITOR`, git-commit style

`internal/editor` opens `$EDITOR` (falling back to `$VISUAL`, then `vi`) on a temp file
whose contents are the user's empty message followed by commented context, and returns
the file with comment lines stripped. Empty result means abort, exactly like `git commit`.

```
# followup to #14 · Pick an event store · researcher
#
# Lines starting with # are ignored. Save an empty file to abort.
#
# ── recent comments ─────────────────────────────────────────────
# researcher · 4m ago
#   Should the projection be rebuilt on every start, or persisted?
#
# dan · 1h ago
#   Prefer whatever keeps CGO off.
```

Each verb supplies its own header and context: `comment` and `followup` show the last
three comments, `answer` shows the agent's question, `edit` opens the issue markdown
itself (frontmatter and body, not a temp file) and reindexes on save.

The TUI suspends for the editor with `tea.ExecProcess`, so it works identically inside
and outside tmux.

### 2. The store publishes change events; the TUI subscribes

`issues.Store` gains a broadcast: every write path — create, edit, comment, close,
reopen, link PR, record review, run started, run ended — publishes an `Event{Kind,
Plan, Issue}` on a channel each subscriber owns. `runner.Runner` publishes run start
and end through the same store (it already writes the `runs` row). This is not a new
hook interface; `OnClosed`/`OnLinked` stay as they are for the recheck and review
loops. It is a fan-out for observers that only want to know *something changed*.

The TUI subscribes once at startup and turns each event into a `tea.Msg` that reloads
the affected plan's rows. The 3-second and 1-second ticks are deleted. The one poll
that remains is `recheck`'s check of PR state through `gh`, which publishes an event
when it observes a change like any other writer.

### 3. Planner placeholder rows come from the same channel

Spawning a planner records a run with no issue; that run-started event is what makes a
`planning` row appear in the plan table (ADR-005) the moment `n` is pressed, and the
plan-applied event is what replaces it with the real plan.

## Consequences

Positive: the tables react within a frame to anything pib itself did, and no view code
reads the store on a timer. `$EDITOR` gives comments and followups a real editor with
the conversation visible, and the abort-on-empty rule is one users already know.

Negative: every write path has to remember to publish; a missed one is a stale table
until the next event, which is a class of bug the ticks used to hide. Slow subscribers
must not block writers, so the channel drops rather than blocks and the TUI treats any
event as "reload", never as a delta.
