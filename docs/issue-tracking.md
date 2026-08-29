# Issue tracking in pib

pib takes over issue tracking from `gh`. Issues, plans, dependencies and agent runs
become pib's own, stored locally under `.pib/`. GitHub keeps pull requests.

**Status: built.** All eleven phases are in. This document is now a reference for how
it works and why; where the build diverged from the plan, it says so. Rewriting the
agents to call `pib` instead of `gh` is a separate pass, and the interface for browsing
a plan and launching its ready work is still to come.

---

## Decisions

| | |
|---|---|
| **Storage** | Local, never committed. SQLite for metadata, one markdown file per issue. |
| **Split** | Frontmatter owns authored fields; SQLite owns lifecycle, parent, and blocked-by. |
| **Plan** | A first-class record. Issues belong to a plan. |
| **vs GitHub** | pib owns issues. Workers still open PRs on GitHub. |
| **Closure** | Worker links its PR; pib closes the issue once `gh` reports the PR merged. |
| **State** | Only `open`/`closed` is stored. Ready, blocked, in-progress, awaiting-review are derived. |
| **Agent surface** | `pib issue` / `pib plan` CLI, talking to the running pib over the existing socket. |
| **Type → agent** | `~/.pib/config.toml`, overridden per repo by `.pib/config.toml`. Types are open; unmapped types simply cannot launch. |
| **Comments** | Appended to the issue markdown. |
| **Enforcement** | Warn, never block. Rules harden over time. |
| **Driver** | `modernc.org/sqlite` — pure Go, keeps `CGO_ENABLED=0` working. |

The TUI for browsing a plan and launching agents is **out of scope here**, but every
capability it needs is part of this API: list plans, list a plan's issues with derived
state, get the ready set annotated with the agent that would run, and start those agents
one at a time or several at once.

---

## Layout

```
.pib/
├── config.toml              # workspace override of the type → agent map
├── data/
│   ├── pib.db               # plans, issue index + lifecycle, deps, runs
│   └── issues/
│       └── 7-implement-order-aggregate.md
├── extension/pib.ts
├── runs/<id>/               # unchanged; runs table indexes these
├── pib.sock
└── socket

~/.pib/
├── agents/*.md
└── config.toml              # default type → agent map, seeded on first run
```

Issue numbers are workspace-global and monotonic, allocated by pib. The filename slug is
derived from the title at creation and **does not follow a rename** — the DB's `path`
column is authoritative, so a stale slug is cosmetic, never a broken link.

### The issue file

```markdown
---
title: Implement Order Aggregate
type: task
acceptance:
  - Order aggregate handles the PlaceOrder command
  - OrderPlaced event is emitted and persisted
  - Idempotency key validated
---

## Task

Implement the order aggregate as the root of the order context.

### ADRs
- docs/adrs/ADR-001-event-sourcing.md

### Domain terms
- **Order Aggregate** — root of the order context

<!-- pib:comments -->

### reviewer · 2026-08-29T14:02:11Z

NEEDS CHANGES — the idempotency key is validated after the event is emitted.
```

No `state`, `parent`, or `blocked-by` in frontmatter: those live in SQLite, so lifecycle
churn and dependency edits never rewrite a file the user might have open.

`acceptance` is the one list-valued key. The existing agent-definition parser
(`internal/agent.parse`) is flat `key: value` only, so the issue parser is a separate,
slightly richer one supporting `- ` continuation lines — still no YAML dependency.

Comments append below the `<!-- pib:comments -->` marker. Individual comments are not
addressable, and concurrent appends are safe only because every write goes through the
one pib process (see below) — the CLI never touches a file directly.

---

## Schema

```sql
CREATE TABLE schema_version (version INTEGER NOT NULL);

CREATE TABLE plans (
  id          INTEGER PRIMARY KEY,
  slug        TEXT    NOT NULL UNIQUE,
  title       TEXT    NOT NULL,
  created_at  TEXT    NOT NULL,
  planner_run TEXT                       -- run dir id that produced this plan
);

CREATE TABLE issues (
  number        INTEGER PRIMARY KEY,     -- workspace-global
  plan_id       INTEGER NOT NULL REFERENCES plans(id),
  local_id      TEXT,                    -- id from the apply document; re-apply matches on it
  parent        INTEGER REFERENCES issues(number),
  path          TEXT    NOT NULL,        -- relative to .pib/data

  -- indexed copy of frontmatter; files win
  title         TEXT    NOT NULL,
  type          TEXT    NOT NULL,
  acceptance    TEXT,                    -- JSON array
  indexed_mtime INTEGER NOT NULL,
  indexed_size  INTEGER NOT NULL,

  -- lifecycle; DB owns
  state         TEXT    NOT NULL CHECK (state IN ('open','closed')),
  closed_at     TEXT,
  pr_url        TEXT,
  pr_state      TEXT,                    -- open | merged | closed, last observed
  pr_checked_at TEXT,
  created_at    TEXT    NOT NULL,
  updated_at    TEXT    NOT NULL,
  UNIQUE (plan_id, local_id)
);

CREATE TABLE deps (
  blocked INTEGER NOT NULL REFERENCES issues(number),
  blocker INTEGER NOT NULL REFERENCES issues(number),
  PRIMARY KEY (blocked, blocker)
);

CREATE TABLE runs (
  id          TEXT PRIMARY KEY,          -- the existing .pib/runs/<id>
  issue       INTEGER REFERENCES issues(number),
  agent       TEXT NOT NULL,
  tmux_window TEXT,
  started_at  TEXT NOT NULL,
  ended_at    TEXT,
  status      TEXT                       -- done | needs_input | error | unknown
);
```

Comments have no table — they are in the markdown.

Three things the plan did not spell out, added while building:

- `window` became `tmux_window`, because `WINDOW` is a SQLite keyword.
- `CHECK` constraints on `state`, `pr_state`, `runs.status` and `blocked <> blocker`.
  Constraining `state` to open/closed at the schema level means a derived state can
  never be written by accident.
- Indexes on `issues(plan_id)`, `issues(state)`, `deps(blocker)`, `runs(issue)`, and a
  partial `runs(issue) WHERE ended_at IS NULL` — the in-progress lookup, which every
  readiness query hits.

### Derived state

Nothing below is stored, so nothing can go stale:

| | |
|---|---|
| `blocked` | open, and some blocker is open |
| `in_progress` | has a run row with `ended_at IS NULL` |
| `awaiting_review` | `pr_url` set and `pr_state = 'open'` |
| `ready` | open, not blocked, not in progress, not awaiting review |
| `launchable` | `ready` and its type maps to an agent |

On startup pib closes out orphaned runs — `ended_at IS NULL` from a previous process —
as `unknown`, so a crash cannot wedge an issue in progress forever.

---

## Architecture

The store is an ordinary Go package owned by the running pib process. The CLI is a thin
client over the socket that already exists; the future TUI is an in-process caller of the
same API. One writer, no file locking, no second copy of the logic.

```
  pib issue list ──socket──┐
  pib plan apply ──socket──┤
                           ▼
                    server (routes by op)
                           │
              ┌────────────┴────────────┐
              ▼                         ▼
        issues.Store              runner.Runner
      (SQLite + md files)       (spawns tmux windows)
              ▲                         │
              └── future TUI ───────────┘
                  (in-process)
```

### Packages

| package | responsibility |
|---|---|
| `internal/issues` | **New.** The store: schema, migrations, CRUD, deps, comments, derived queries, reindex, plan documents, reconciliation. Pure Go API, knows nothing about sockets. |
| `internal/config` | **New.** Type → agent map: load `~/.pib/config.toml`, merge `.pib/config.toml` over it key by key, seed defaults on first run. |
| `internal/pr` | **New.** `gh pr view` behind an interface, so tests never shell out. |
| `internal/issueops` | **New.** Where the parts meet: decodes a payload per op, calls the store, marshals the reply. The only package importing all of `issues`, `config` and `pr`. |
| `internal/cli` | **New.** The command line: flags, the socket client, and text or JSON output. |
| `internal/protocol` | New ops and a payload field. |
| `internal/server` | `Router` dispatches by op: agent ops to the runner, issue ops to the handler. |
| `internal/runner` | Accepts an optional issue number; records run rows. |
| `cmd/pib` | Argument dispatch: no args → TUI, otherwise CLI client. |

**The apply document did not get its own package.** It was planned as
`internal/issues/plandoc`, but a subpackage can only reach the store through its
exported API, where every create is its own transaction. Applying a whole plan
atomically needs the store's own transaction, so it lives in `internal/issues/apply.go`.

**The store reads no configuration and shells out to nothing.** Three callbacks are
passed in instead — `KnownType`, `AgentFor`, and a one-method `PRLookup` the store
declares itself. That keeps `internal/issues` free of `config` and `pr`, and lets every
test supply a fake rather than a real `gh`.

### Protocol

`protocol.Request`/`Response` gain a `Payload json.RawMessage`; existing spawn/resume
fields are untouched, so `pib.ts` needs no change in this pass. Issue ops carry their
arguments and results in the payload.

Server's `Runner` interface stays as it is. A new `Store` interface is added, and
`handle` picks by op prefix. Issue ops reply immediately; the existing
hold-the-connection-open behaviour applies only to agent ops.

Ops: `plan.apply`, `plan.list`, `plan.view`, `issue.create`, `issue.list`, `issue.view`,
`issue.edit`, `issue.comment`, `issue.link_pr`, `issue.close`, `issue.ready`,
`issue.reindex`.

---

## CLI

`cmd/pib/main.go` launches the TUI today no matter what. It gains dispatch: bare `pib`
is unchanged, anything else is a client that resolves the socket with
`server.Discover` and prints the reply.

```
pib plan apply <file.json>
pib plan list
pib plan view <slug>

pib issue create --plan <slug> --type task --title <t> [--body-file f]
                 [--parent n] [--blocked-by n,m] [--acceptance ...]
pib issue list   [--plan slug] [--state open|closed] [--type task] [--ready]
pib issue view   <n> [--comments]
pib issue edit   <n> [--title t] [--type t] [--parent n]
                 [--add-blocked-by n,m] [--remove-blocked-by n]
pib issue comment <n> --body <text> | --body-file <f>
pib issue link-pr <n> <url>
pib issue close   <n> [--reason <text>]
pib issue ready   [--plan slug]
pib issue reindex [--plan slug]
```

Every command takes `--json`. Human output is the default so the CLI is usable by hand;
agents pass `--json`. `pib issue ready --json` returns, per issue, the number, title,
type, plan, and the **agent that would run it** (`null` when the type is unmapped) —
enough for the TUI to launch one or fan out across all of them without a second call.

With pib not running, every command fails with the same message the `pib` tool already
gives: *pib is not running (no listener at …). Start pib in this repository and try
again.*

---

## The apply document

```json
{
  "plan": { "slug": "orders", "title": "Order placement" },
  "issues": [
    {
      "id": "feature",
      "type": "feature",
      "title": "Feature: order placement",
      "body": "## Goal\n…"
    },
    {
      "id": "order-agg",
      "type": "task",
      "title": "Implement Order Aggregate",
      "parent": "feature",
      "blockedBy": ["schema"],
      "acceptance": ["Order aggregate handles the PlaceOrder command"],
      "body": "## Task\n…"
    }
  ]
}
```

`id` is local to the document. `parent` and `blockedBy` reference a local id or an
existing issue as `"#12"`. pib allocates real numbers on apply.

**Re-apply is an additive merge.** Matching on `(plan, local id)`: known ids update,
unknown ids are created, and issues absent from the document are left completely alone.
Closed issues are never reopened. Safe to re-run while workers are in flight.

**Validation warns, it does not block** — the whole document is written in one
transaction, and warnings come back in the response so the planner can see them:

- dependency cycles → warned, written (they simply produce no ready issues)
- no startable issue → warned
- unmapped type → warned (the issue exists, it just cannot launch)
- dependency crossing plans → warned
- reference to an unknown local id → **dropped** with a warning; there is no row to point at
- malformed JSON, missing title or type → hard error, nothing written

That list is where future hardening lands: each item can graduate from warning to error
without changing the shape of anything.

---

## Closure and PR reconciliation

1. The worker runs `pib issue link-pr <n> <url>`. `pr_state` becomes `open` and the issue
   is now `awaiting_review` — derived, so it drops out of the ready set immediately.
2. `issue.list`, `issue.ready` and `plan.view` reconcile first, shelling out to
   `gh pr view <url> --json state`. Results are cached with `pr_checked_at`; a check
   inside the last 30s is skipped, and lookups run bounded and in parallel.
   `mergedAt` turned out to be unnecessary — `state == "MERGED"` is the whole signal.

   Reconciliation is an explicit `Store.Reconcile` call made by the operations layer,
   not something hidden inside a read. Network I/O inside `Ready()` would have made the
   store dishonest and its tests slow.
3. A merged PR closes the issue, sets `closed_at`, and appends a comment recording the
   merge. Dependents become ready on the next query.
4. If `gh` is missing or fails, the command warns and leaves state untouched. Issue
   tracking still works with no network; only automatic closure pauses.

`pib issue close` on a `task` with no merged PR warns — *task issues normally close when
their PR merges* — and proceeds. This is the first rule to promote to a hard error when
you want to harden it.

---

## Run tracking

`protocol.Request` gains an optional issue number. When the server spawns an agent for an
issue it inserts a run row before the window opens and updates it with the outcome from
`session.Collect` when the agent stops. Runs are never deleted, so an issue carries the
history of every attempt — the failed first worker as well as the one that opened the PR.

This is what makes `in_progress` derivable rather than a label somebody forgot to clear,
and it gives the TUI the tmux window to reattach to.

---

## Config

`~/.pib/config.toml`, written with defaults the first time pib runs, alongside the agent
install prompt that already exists:

```toml
[types]
feature   = ""            # container; never launches an agent
task      = "worker"
research  = "researcher"
prototype = "prototype"
reviewer  = "reviewer"
```

`.pib/config.toml` merges over it key by key, so a repo can reroute one type without
restating the rest. Types are open: an unmapped type is accepted, marked not launchable,
and surfaced as a warning wherever it appears.

Needs one dependency, `github.com/BurntSushi/toml`.

---

## What was built

Eleven phases, each independently testable, each left green.

| | | |
|---|---|---|
| 1 | Config | `internal/config` — load, merge, seed. |
| 2 | Store foundation | Schema, embedded migrations, `Open` / `Close`. |
| 3 | Issue files | Frontmatter with list support, slugs, comment append. |
| 4 | CRUD and dependencies | Create, view, edit, list, comment, close; edges; reindex. |
| 5 | Plans and apply | The JSON document, validation warnings, additive merge. |
| 6 | Derived queries | Blocked, ready, in progress, awaiting review, launchable. |
| 7 | PR reconciliation | `internal/pr`, the cache window, auto-close on merge. |
| 8 | Protocol and routing | Payload field, twelve ops, `Router`, `internal/issueops`. |
| 9 | CLI | `internal/cli`, text and `--json` output for every command. |
| 10 | Run tracking | Issue on the request, run rows, orphan cleanup. |
| 11 | Docs | The README, and this document. |

### Decisions taken during the build

**Unknown frontmatter keys are preserved.** The plan said pib ignores them, but ignoring
on read and discarding on write are different things — an edit would have silently eaten
a hand-added `owner: dan`. They round-trip untouched.

**`Parse` is lenient; `Validate` is strict.** Parsing fails only on structural damage, so
a half-edited file can still be read and reported on rather than being unopenable.

**Comments append rather than re-render.** Adding a comment never rewrites the prose
above the marker, so nothing a person typed can be normalised away.

**Two methods beyond the plan.** `Store.Agent(number)` returns the agent that would run
an issue or an error saying why not — *"#4 is not ready: it is blocked by #2 and #3"* —
which is the question an interface has to ask before offering a launch button.
`Store.Cycles(plan)` finds loops after the fact, since a later edit can close one that
was not there at apply time.

**Orphan cleanup lives in `Open`.** Opening the store is taking ownership of it, so any
run still marked live belongs to a process that is gone. Putting it there means it
cannot be forgotten.

**A run pib cannot record does not run.** If the run row fails to write — which is what a
request naming a nonexistent issue looks like — the window is killed and the spawn
fails. An untracked agent working away would be worse than a refusal. Finishing a run is
best effort by contrast: the agent's answer matters more, and orphan cleanup catches it.

## Deliberately not here

- **Agent rewrites.** The planner, worker and reviewer keep calling `gh` until a later pass.
- **The TUI.** Browsing a plan and launching ready agents singly or in parallel. Every
  query and command it needs is in: `Statuses` and `Ready` return issues annotated with
  the agent that would run them, `Status.Run` gives the tmux window to reattach to, and
  `protocol.Request.Issue` carries the issue into a spawn.
- **The orchestration loop.** `/implement` — pib choosing work on its own.
- **ADRs and domain terms as records.** They stay as prose in issue bodies and files in
  the repo.
- **Free-form labels.** Type is the only classification.
- **Any sync to GitHub issues.** One direction, one owner.

---

## Open notes

**Acceptance criteria are frontmatter, not checkboxes.** They are therefore authored data
rather than something a worker ticks off as it goes. Workers already report criteria with
evidence in the PR body, so nothing is lost today — but if you later want per-criterion
progress on the issue itself, that is a schema change, not a formatting one.

**Comments are not addressable.** Appending to the markdown means no comment ids, so
nothing can reply to, edit, or resolve a specific comment. Fine for a reviewer posting a
verdict; a constraint if review threads ever need to converge.
