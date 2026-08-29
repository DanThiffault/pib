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
`reviewer`, and `worker` — embedded in the binary. The first time you run pib on a
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
  • worker

y/enter install • n/q exit
```

`y` writes them to `~/.pib/agents/`. `n` or `q` exits — pib has nothing to run without
a planner. Installing never overwrites a definition already on disk, so it is safe to
edit them and safe to re-run.

They are ordinary markdown files after that: change the models, rewrite the prompts,
add your own. See [Agent definitions](#agent-definitions) for the format.

## Usage

Run `pib` from inside a git repository, in a tmux session:

```bash
cd ~/dev/my-project
pib
```

### Startup

On first run pib checks three things and asks before changing anything:

1. **`.pib/` missing** — pib keeps its workspace at the repository root.
   `y` creates it; `n` or `q` exits.
2. **`.pib/` not gitignored** — `y` appends `/.pib/` to the root `.gitignore`,
   `n` continues without it, `q` exits.
3. **`~/.pib/agents/` missing** — `y` installs the default agents; `n` or `q` exits.

Then it loads `~/.pib/agents/planner.md` and opens its socket.

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

Sub-agents finish by calling `pib_done`; their last message before that call is what
the caller receives. They ask with `pib_ask`. Both tools are added automatically —
agent definitions never list them.

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

Everything pib writes lives under `.pib/` at the repository root, and is gitignored:

```
.pib/
├── extension/pib.ts   # pi extension, written from the binary at startup
├── runs/<id>/         # one directory per sub-agent: transcript + exit.json
├── pib.sock           # socket agents call pib through
└── socket             # the socket's real path
```

`pib.sock` moves to a short path under the system temp directory when the repository
sits deep enough that the full path would exceed the kernel's ~104-byte limit for unix
sockets. `.pib/socket` always records where it actually is.

## How it fits together

pib registers a pi extension that provides three tools. `pib` runs in the caller and
opens a socket connection to the pib TUI, which spawns the agent and holds the
connection until it stops — one request, one reply, no message bus. `pib_done` and
`pib_ask` run in the sub-agent and write an `exit.json` sidecar before shutting down.

That sidecar exists because pi cannot distinguish "task complete" from "waiting on a
question": both end a turn with `stopReason: "stop"`. The child declares which it is.
pib also checks the stop reason of the final turn, because a crashed agent still exits
zero and leaves a plausible-looking last message.

If the caller disconnects — you interrupt the planner, or quit pib — the sub-agent's
window is killed rather than orphaned.

## Troubleshooting

**"pib is not running (no listener at …)"** — an agent called the `pib` tool with no
pib TUI listening for that repository. Start pib in the repository and retry.

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
