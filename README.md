# pib

A planning front-end for [pi](https://github.com/earendil-works/pi).

pib opens a prompt, takes a description of something you want to build, and hands it
to a planning agent running in its own tmux window. That planner can delegate to other
agents — a scout to map the codebase, a researcher to compare approaches — by calling
the `pib` tool, which asks pib to run them and returns their answers.

```
┌─ window 1 ──────┐     ┌─ window 2 ──────┐     ┌─ window 3 ──────┐
│ pib             │────▶│ planner         │────▶│ scout           │
│ "what to plan?" │     │ pib(agent:…)    │     │ pib_done        │
└─────────────────┘     └─────────────────┘     └─────────────────┘
         ▲                       ▲   findings returned    │
         └───── unix socket ─────┴────────────────────────┘
```

## Requirements

| | |
|---|---|
| **Go** | 1.26.5+, to build |
| **[pi](https://github.com/earendil-works/pi)** | the agent runtime pib drives |
| **Node** | 22.19+, required by pi |
| **tmux** | recommended — agents each get their own window; without it pib takes over the terminal |
| **git** | pib anchors its workspace to a repository root |

## Install

Install pi if you don't have it:

```bash
npm install -g @earendil-works/pi-coding-agent
```

Then run `pi` once and use `/login` to configure a provider, or set the provider's API
key in your environment. `pi auth check --provider <name>` confirms it worked.

Build pib:

```bash
git clone <this repo> && cd pib
go build -o pib ./cmd/pib
```

Put the binary somewhere on your `PATH`, or run `./pib` from the repo.

> Using Nix? `nix-shell` provides Go, gopls, gotools, and golangci-lint.

### Agents

pib ships a default set of agents — `planner`, `scout`, `researcher`, `prototype`,
`reviewer`, and `coder` — embedded in the binary. The first time you run pib on a
machine, it offers to install them:

```
No agents are installed in ~/.pib/agents
pib runs agents defined there; it cannot plan without them.

Install the default set?

  • planner
  • prototype
  • researcher
  • reviewer
  • scout
  • coder

y/enter install • n/q exit
```

`y` writes them to `~/.pib/agents/`. `n` or `q` exits — pib has nothing to run without
a planner. Installing never overwrites a definition already on disk, so it is safe to
edit them and safe to re-run.

They are ordinary markdown files after that: change the models, rewrite the prompts,
add your own. See [Agent definitions](#agent-definitions) for the format.

#### Keeping them up to date

Because installing never overwrites, a definition written by an older pib stays as it
is — including after an upgrade that rewrote it. So pib compares what is on disk with
what is built into the binary, and asks when they differ:

```
2 agents in ~/.pib/agents differ from the version built into this pib:

  • plan-recheck
  • reviewer

That is either a newer pib or your own edits — pib cannot tell.
Updating saves your copies under ~/.pib/agents-backup first.

Update them?

y/enter update • n keep mine • q exit
```

pib genuinely cannot tell a stale default from an edit you made on purpose, so `y`
never destroys what it replaces: every definition it rewrites is copied to
`~/.pib/agents-backup/<timestamp>/` first, and the run tells you where. `n` keeps
yours and carries on — the definitions on disk are the ones pib has been running all
along, and declining is not a reason to stop.

Only the agents you are asked about are touched. Edited definitions you want to keep
are safest left out of the update; if you take one by accident, it is in the backup.

## Usage

Run `pib` from inside a git repository, in a tmux session:

```bash
cd ~/dev/my-project
pib
```

### Startup

On first run pib checks two things and asks before changing anything:

1. **`.pib/` missing** — pib keeps its workspace at the repository root.
   `y` creates it; `n` or `q` exits.
2. **`~/.pib/agents/` missing** — `y` installs the default agents; `n` or `q` exits.

pib does not touch your `.gitignore`. Add `/.pib/` to it yourself — nothing under it
is meant to be checked in.

Then it loads `~/.pib/agents/planner.md`, writes `~/.pib/config.toml` if there isn't
one, opens the issue store under `.pib/data/`, and opens its socket.

### Planning

Type what you want to plan and press enter:

| key | |
|---|---|
| `enter` | start the planner |
| `alt+enter` (or `ctrl+j`) | newline — descriptions can be multi-line |
| `esc` / `ctrl+c` | quit |

The planner opens in a new tmux window and takes over from there. pib stays running in
its own window, ready for the next plan — switch back with your tmux prefix. Outside
tmux, pib falls back to handing over the current terminal; the prompt tells you which
you'll get before you commit.

### Delegating to other agents

Inside a planner session, the `pib` tool runs another agent:

```
pib(agent: "scout", name: "Scout", task: "Map the auth flow and its conventions")
```

The call **blocks**. The agent opens in its own tmux window, and its answer is the
result of the call — there is nothing to poll or tail. Several calls in one turn run
concurrently.

A sub-agent that can't continue without an answer replies with a question instead of
findings. Continue it with the session it handed back:

```
pib(session: "8f2c1a…", answer: "Use Postgres")
```

Pass `issue` to say what the agent is working on, and pib shows that issue as in
progress for as long as the agent runs:

```
pib(agent: "coder", name: "Coder", task: "Implement the order aggregate", issue: 7)
```

Sub-agents finish by calling `pib_done`; their last message before that call is what
the caller receives. They ask with `pib_ask`. Both tools are added automatically —
agent definitions never list them.

## Issue tracking

pib keeps its own issues. A plan becomes a tree of issues with dependencies between
them, and pib works out from those what can start now. GitHub still owns pull
requests; nothing is pushed to GitHub issues.

Everything lives under `.pib/`, which is not meant to be checked in: metadata in a
SQLite database, and one markdown file per issue beside it. The running pib is the
only writer — the commands below are clients that reach it over the same socket
agents use, so several agents can work at once without stepping on each other.

### Applying a plan

The planner writes a document and hands the whole thing over at once. pib allocates
the issue numbers, so issues can refer to each other before they exist:

```json
{
  "plan": {
    "slug": "orders",
    "title": "Order placement",
    "body": "## Goal\n\nCustomers can place an order.",
    "acceptance": ["An order can be placed end to end"]
  },
  "issues": [
    { "id": "schema", "type": "task", "title": "Order schema",
      "body": "## Task\n\nWrite the schema.", "acceptance": ["Tables exist"] },
    { "id": "agg", "type": "task", "title": "Order aggregate",
      "blockedBy": ["schema"] }
  ]
}
```

What the feature *is* — the goal, the scope, the criteria the whole thing is judged on
— belongs on the plan. Every issue is work someone does; there is no container issue
for the feature itself, which would launch nothing and never close.

```bash
pib plan apply /tmp/plan.json   # or - to read the document from stdin
```

`id` is local to the document; `blockedBy` takes one of those ids or an existing issue
as `"#12"`. The whole document lands in one transaction. (`parent` exists too, for a
task that genuinely decomposes — it is not how issues join a plan.)

A new plan gains one issue nobody wrote: a review of the plan itself, which every
issue with no other blocker waits on. It runs `plan-reviewer` over the plan and the
code it will change, before any of it is worked, and closing it releases the rest.
Only a plan with no issues yet gets one, so an amendment to a plan already underway
never adds a gate. Turn it off with `review = false` under `[plan]` in `config.toml`.

```bash
pib plan review <slug>       # run another one by hand, any time
```

### Running a whole round

```bash
pib plan start <slug>
```

Every issue in the plan that could start right now starts, together, each with the
agent its type maps to. The set is taken **once, before anything launches**: an issue
that becomes ready because one of these closes is left for the next invocation. That is
deliberate — a command that kept launching as work unblocked would run an entire plan
from one keystroke, and there would be no moment to read what came back. Run it again
for the next round.

It waits for the round to finish and prints a line per agent. An issue that was ready
but whose type maps to no agent is reported rather than skipped silently.

Each issue gets its own git checkout under `.pib/worktrees/<number>/`, because a branch
belongs to a directory rather than to a process: two coders in one directory both run
`git checkout -b`, and the second moves the tree out from under the first. The checkout
is detached, so each agent's own branch command lands in its own tree and nowhere else,
and a followup comes back to the same one — with its branch and whatever it had not
committed. Checkouts of closed issues are swept when pib next starts, which is the only
moment nothing is running in them.

Turn it off with `isolate = false` under `[plan]` for a project where a fresh checkout
needs expensive setup — installed dependencies, a build cache — and run one agent at a
time instead.

Applying the same plan again is an **additive merge**: known ids update, new ids are
created, and an issue you dropped from the document is left alone — never closed,
never deleted. A closed issue stays closed. So a second planner pass is safe to run
while coders are still going.

pib warns rather than refusing. A dependency cycle, a plan with nothing startable, a
type no agent is mapped to, a reference it could not resolve — each is reported and
the plan is still written. Only a document pib could not write at all is rejected:
malformed JSON, a missing plan slug or title, a duplicate id, an issue with no title
or type.

### Commands

```bash
pib plan list                  # every plan
pib plan view orders           # a plan and the issues in it

pib issue list                 # everything, with derived state
pib issue ready                # what could start right now
pib issue view 7
pib issue create --plan orders --type task --title "Order schema"
pib issue start 7              # run the agent that implements it, and wait
pib issue edit 7 --title "…" --add-blocked-by 3,4
pib issue comment 7 --body "Looks right."
pib issue link-pr 7 https://github.com/you/repo/pull/12
pib issue close 7 --reason "superseded"
pib issue reopen 7             # back in play, for another attempt
pib issue reindex
```

Every command takes `--json`, which prints the reply and nothing else — warnings go
to stderr in text mode and into the payload in JSON mode, so piping into `jq` is safe.

A listing shows what each issue is waiting on and what would run it:

```
ISSUE  STATE    TYPE     TITLE                      NOTE
#1     ready    feature  Feature: order placement   no agent for this type
#2     ready    task     Order schema               coder
#3     blocked  task     Order aggregate            waiting on #2
```

### State pib works out rather than stores

Only **open** and **closed** are recorded. Everything else is derived on every read,
so nothing can be left stale by an agent that crashed or a label nobody cleared:

| | |
|---|---|
| **blocked** | open, and something it waits on is still open |
| **in progress** | an agent run that has not ended |
| **in review** | a linked pull request that has not merged or been closed |
| **ready** | open, unblocked, nobody working on it, nothing pending |
| **launchable** | ready, and its type maps to an agent |

pib closes out any run still marked live when it opens the store, so a crash cannot
hold an issue in progress forever.

### How an issue closes

There is no `Closes #N` automation any more, so pib reproduces the rule it enforced:

1. A coder opens its pull request and records it with `pib issue link-pr <n> <url>`.
   The issue is now in review and drops out of the ready set.
2. A human merges the pull request.
3. The next listing settles it — pib asks `gh` about linked pull requests when you
   run `issue list`, `issue ready` or `plan view`, closes the issue whose pull
   request merged, notes the merge as a comment, and frees whatever it was blocking.

A pull request closed without merging puts the issue back in the ready set: the work
was abandoned, so someone can pick it up again.

`gh` is only consulted for that. If it is missing or the network is down, pib says so
and carries on — issue tracking works offline, only automatic closure pauses. Checks
are cached for 30 seconds, so a tight loop of listings does not shell out repeatedly.

You can always `pib issue close <n>` by hand. Closing a task whose pull request has
not merged is allowed and reported, because pib warns rather than blocking.

`pib issue reopen <n>` undoes a close, and everything that was waiting on the issue
goes back to waiting. It moves the state and nothing else: whatever the last attempt
wrote is still on the issue and still on disk, so a genuinely clean retry means
clearing that out too — otherwise the agent reads its own conclusions and ratifies
them.

### Running the work

`pib issue start <n>` runs the agent an issue's type maps to, and blocks until it
stops — the same path an agent takes when it delegates, so the run is recorded and the
issue reads as in progress while it works.

It refuses an issue that cannot start, saying why: *"#3 is not ready: it is waiting on
#2"*, or *"no agent is mapped to type \"feature\""*. `--force` starts it anyway, and
`--agent <name>` runs something other than the mapped agent.

To come back to an agent afterwards — because you left review comments, or because it
stopped to ask something — follow up rather than starting again:

```bash
pib issue followup 4 --message "address the comments I left on the PR"
```

That resumes the session the agent left behind, so it still has everything it worked
out the first time and the message can be as short as what you actually want changed.
Each agent knows where its own feedback lives, so you do not have to say: the coder
reads its pull request, the researcher and the prototype read the issue's activity.

The same command answers an agent that stopped to ask a question — the message is the
answer. It refuses when there is no session to continue, when an agent is still
working, or when the issue is closed, which `--force` overrides. A coder followed up
after its pull request has merged branches again and opens a new one rather than
pushing to a merged branch.

The agent is told its issue number in `PIB_ISSUE` and pointed at `pib issue view`, so
the issue itself is the specification rather than whatever the task string happened to
say.

### Types and agents

An issue's type says which agent implements it. The mapping lives in
`~/.pib/config.toml`, written on first run:

```toml
[types]
feature   = ""            # a container; never launches an agent
task      = "coder"
research  = "researcher"
prototype = "prototype"
reviewer  = "reviewer"
```

A `.pib/config.toml` in the repository overrides it key by key, so one project can
reroute a single type without restating the rest. Types are open: an issue of a type
nobody has mapped is stored happily, it just cannot be launched, and pib says so
wherever it appears.

### The files

A plan and an issue are both markdown files you can open and edit. The plan holds what
is being built:

```markdown
---
title: Order placement
type: plan
acceptance:
  - An order can be placed end to end
---

## Goal

Customers can place an order.

### In scope
- …
```

And an issue holds one piece of the work:

```markdown
---
title: Order schema
type: task
acceptance:
  - Tables exist
---

## Task

Write the schema.

<!-- pib:comments -->

### reviewer · 2026-08-29T14:02:11Z

Needs an index on the idempotency key.
```

The frontmatter is what the planner authored; state, parent and dependencies live in
the database, so lifecycle churn never rewrites a file you have open. Frontmatter keys
pib does not recognise are kept as they are.

Edit a file by hand and pib picks it up: every read checks whether the file has moved
on and re-reads it if so. The file wins. `pib issue reindex` forces a full re-read.

Renaming an issue does not rename its file — the database holds the path, so a stale
slug in a filename is cosmetic.

## Agent definitions

One markdown file per agent in `~/.pib/agents/`. YAML frontmatter configures the pi
session; the body becomes the system prompt.

```markdown
---
name: scout
description: Fast codebase reconnaissance
tools: read, bash
model: openrouter/moonshotai/kimi-k2.6
thinking: medium
system-prompt: append
---

# Scout

You are a codebase reconnaissance specialist…
```

| key | effect |
|---|---|
| `name` | display name and window title; defaults to the filename |
| `description` | documentation only |
| `tools` | **allowlist** passed to `pi --tools` |
| `deny-tools` | denylist passed to `pi --exclude-tools` |
| `model` | `pi --model`, e.g. `openrouter/anthropic/claude-opus-4.6` |
| `thinking` | `pi --thinking`: off, minimal, low, medium, high, xhigh, max |
| `system-prompt` | `append` (default) adds the body to pi's prompt; `replace` uses it alone |
| `auto-exit` | planner only: `true` makes pib quit after handing off |

Unknown keys are ignored, so newer definitions still load on an older pib.

> **`tools` is an allowlist, not a denylist.** An agent that should delegate must list
> `pib` explicitly, or the tool is silently unavailable. `pib_done` and `pib_ask` are
> the exception — pib appends them to every sub-agent's allowlist, since an agent that
> can't report completion would hang its caller.

## Workspace layout

Everything pib writes lives under `.pib/` at the repository root. None of it is meant
to be checked in — add `/.pib/` to your `.gitignore`:

```
.pib/
├── config.toml        # optional: this repository's type → agent overrides
├── data/
│   ├── pib.db         # plans, issues, dependencies, agent runs
│   ├── plans/         # one markdown file per plan: its goal and scope
│   └── issues/        # one markdown file per issue
├── extension/pib.ts   # pi extension, written from the binary at startup
├── runs/<id>/         # one directory per sub-agent: transcript + exit.json
├── pib.sock           # socket agents call pib through
└── socket             # the socket's real path
```

Two files live outside the repository, shared by every project:

```
~/.pib/
├── agents/*.md        # agent definitions
└── config.toml        # the default type → agent map
```

`pib.sock` moves to a short path under the system temp directory when the repository
sits deep enough that the full path would exceed the kernel's ~104-byte limit for unix
sockets. `.pib/socket` always records where it actually is.

## How it fits together

pib registers a pi extension that provides three tools. `pib` runs in the caller and
opens a socket connection to the pib TUI, which spawns the agent and holds the
connection until it stops — one request, one reply, no message bus.

That same socket carries the issue commands. `pib issue …` and `pib plan …` are
clients of the running pib rather than programs that open the database themselves,
which is why parallel agents need no locking: there is only ever one writer. `pib_done` and
`pib_ask` run in the sub-agent and write an `exit.json` sidecar before shutting down.

That sidecar exists because pi cannot distinguish "task complete" from "waiting on a
question": both end a turn with `stopReason: "stop"`. The child declares which it is.
pib also checks the stop reason of the final turn, because a crashed agent still exits
zero and leaves a plausible-looking last message.

If the caller disconnects — you interrupt the planner, or quit pib — the sub-agent's
window is killed rather than orphaned.

## The pi extension

pib provides its tools through a pi extension, embedded in the binary. You do not
install it: pib writes it to `.pib/extension/pib.ts` at startup and passes
`--extension` to every session it spawns, so the extension always matches the binary
that launched it.

**Do not add it to `~/.pi/agent/settings.json`.** The tools are context-dependent —
`pib` needs to know which pib to talk to, and `pib_done` / `pib_ask` only register
inside an agent pib started. Loaded globally, they would appear in unrelated sessions
and fail.

To load it by hand while debugging, with pib already running in the repository:

```bash
pi --extension .pib/extension/pib.ts --tools read,bash,pib
```

The extension finds the socket from `PIB_SOCKET`, then `.pib/socket`, then
`.pib/pib.sock`.

## Troubleshooting

**"pib is not running (no listener at …)"** — an agent called the `pib` tool, or you
ran a `pib issue` command, with no pib TUI listening for that repository. Start pib in
the repository and retry.

**"could not check the pull request … gh is not available"** — pib settles linked pull
requests with `gh`. Install it, or ignore the warning: everything except automatic
closure works without it, and `pib issue close` still works by hand.

**A sub-agent's window sits idle and its caller never resumes** — the agent finished
its work but didn't call `pib_done`. Close the window; the caller gets the agent's last
message with an `unknown` status.

**"another pib is already listening"** — one pib per repository. A socket left by a
crashed pib is cleaned up automatically.

**The tool isn't offered at all** — check that the calling agent's `tools:` list
includes `pib`.

## Development

```bash
go test ./...        # unit tests, plus the extension driven under node
go vet ./...
gofmt -l ./cmd ./internal
```

The tmux tests run against a private tmux server on their own socket, so they never
touch your sessions. The extension tests need `node` on `PATH` and skip without it.
