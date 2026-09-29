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
not own is GitHub's view of a pull request. Nothing polls that: `issues.Store.Reconcile`
settles PR state against `gh` only when a client asks for `issue list` or `plan view`
(`issueops.Handler.reconcile`).

## Decision

### 1. Free text goes to `$EDITOR`, git-commit style

`internal/editor` opens `$EDITOR` (falling back to `$VISUAL`, then `vi`) on a temp file
whose contents are the user's empty message, a scissors line, and the context below it.
It returns everything above the scissors line verbatim, `#` lines included, so Markdown
headings survive; only trailing whitespace is trimmed. This is `git commit
--cleanup=scissors`. Empty result means abort, exactly like `git commit`. If the user
deletes the scissors line, the whole file is kept rather than guessing what to strip.

```

# ------------------------ >8 ------------------------
# Do not modify or remove the line above. Everything below it is ignored.
# followup to #14 · Pick an event store · researcher
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

`issues.Store` gains a broadcast: every write path — create plan, apply plan, create,
edit, comment, close, reopen, link PR, open/close review, reconcile, reindex, run started,
run ended — publishes an `Event{Kind, Plan, Issue}` on a channel each subscriber owns.
`runner.Runner` needs no change: it records runs through `Store.StartRun` and
`Store.FinishRun`, and those publish. This is not a new
hook interface; `OnClosed`/`OnLinked` stay as they are for the recheck and review
loops. It is a fan-out for observers that only want to know *something changed*.

The TUI subscribes once at startup and turns each event into a `tea.Msg` that reloads
the affected plan's rows. The 3-second and 1-second ticks are deleted. PR state is
still settled only by `Store.Reconcile`, which publishes on every `pr_state` write like
any other writer. With the ticks gone, a PR merged on GitHub shows up the next time
some client triggers a reconcile, which is no worse than today. The out-of-scope
triage scan stays on its timer: it reads GitHub, and no store event covers it.

### 3. Planner placeholder rows come from the same channel

Spawning a planner records a run with no issue; that run-started event is what makes a
`planning` row appear in the plan table (ADR-005) the moment `n` is pressed, and the
plan-applied event is what replaces it with the real plan. The runner exports the run
id as `PIB_RUN`; `pib plan apply` sends it, and the apply sets `plans.planner_run` and
`runs.plan` on that run, so the planning row disappears on apply even though the
planner window usually stays open. This works inside tmux only: outside tmux the
planner takes over the terminal as today, no run is recorded and no planning row
appears.

## Consequences

Positive: the tables react within a frame to anything pib itself did, and no view code
reads the store on a timer. `$EDITOR` gives comments and followups a real editor with
the conversation visible, and the abort-on-empty rule is one users already know.

Negative: every write path has to remember to publish; a missed one is a stale table
until the next event, which is a class of bug the ticks used to hide. Slow subscribers
must not block writers, so the channel drops rather than blocks and the TUI treats any
event as "reload", never as a delta.
