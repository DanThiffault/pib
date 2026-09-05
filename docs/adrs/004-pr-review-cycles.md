# ADR-004: Per-PR Review Cycles

## Status
Accepted. Revises the review workflow in `internal/agent/defaults/reviewer.md` and
`planner.md`.

## Context

Today a plan carries one `reviewer` issue, blocked by every task in it. It becomes
startable only once all of them are closed — and a task closes when its pull request
merges. So the review reads code that is already on main. Every finding it files is a
follow-up issue against merged work, and the review has no influence on the merge
decision it is nominally there to inform.

The reviewer also has no way to act on what it finds. `reviewer.md` files each finding as
an issue, which is an improvement on a comment nobody is assigned, but the loop still runs
through the user: they read the new issues, launch coders, and wait. A finding on an open
pull request should be fixed on that pull request, before anyone looks at it.

There are two different jobs being done by one agent name:

- **Reviewing a diff.** Per pull request, while it is open, adversarial about correctness.
- **Reviewing a plan.** Once, against the plan's stated goals, asking whether the whole of
  it was actually delivered.

They want different prompts, different tools and different triggers.

## Decision

### 1. `code-reviewer` reviews a pull request; `plan-reviewer` reviews a plan

The `reviewer` agent and the `reviewer` issue type are renamed to `code-reviewer`, and the
agent is rewritten around a single pull request rather than a whole plan. `plan-reviewer`
keeps its name and gains a second job.

| | trigger | scope | files issues? |
|---|---|---|---|
| `plan-reviewer` (before) | plan applied | every issue vs. the codebase | no — comments |
| `code-reviewer` | a PR is linked | one diff | yes, on the PR |
| `plan-reviewer` (after) | last task closed | plan goals vs. what was built | yes |

The existing `[types]` map in `config.toml` gains `code-reviewer = "code-reviewer"`.
`reviewer` stays mapped for one release so plans already in flight still launch something,
with `internal/config` warning that it is deprecated.

### 2. The trigger is a hook on `Store.LinkPR`

`Store` gains `OnLinked`, a second hook alongside `OnClosed`, notified after
`LinkPR` writes. It mirrors `ClosedHook` exactly, including the requirement not to block:

```go
// LinkedHook is notified after a pull request is linked to an issue.
type LinkedHook interface {
    PRLinked(issue Issue)
}
```

A new `internal/review` package implements it, the way `internal/recheck` implements
`ClosedHook`. `internal/ui/startup.go` wires it where `store.OnClosed` is set today.

A hook, rather than an issue pib files per task: the review has to happen while the pull
request is open, and an issue blocked by the task would wait for the merge that the review
exists to inform. Nothing in the dependency graph can express "after the PR, before the
merge" — that is a lifecycle event, and lifecycle events are what hooks are for.

### 3. The cycle runs reviewer → coder → reviewer, at most three times

`review.Loop` owns the sequence, in a goroutine, driving `runner.Runner` synchronously —
`Run` already blocks for the life of an agent, which is what makes the loop expressible as
straight-line code:

```
for cycle := 1; cycle <= max; cycle++ {
    spawn code-reviewer      → wait
    read the verdict it recorded
    if approved             → stop
    followup the coder       → wait   (addresses the findings on the PR)
}
stop; the pull request is the user's
```

**Three cycles, from `[review] cycles = 3` in `config.toml`.** A fourth pass on a diff that
two coders have already reworked is not converging, and the loop must terminate without a
human — pib is spending model time unattended.

The loop ends in exactly one of three states, all of which leave the pull request open and
unmerged:

- **Approved.** The reviewer found nothing blocking.
- **Exhausted.** Three cycles ran and findings remain.
- **Failed.** An agent errored, or a coder could not push.

pib never merges, and the loop never closes the task issue. The issue still closes the one
way it closes today: the user merges, and reconciliation sees it.

### 4. Cycles are recorded, not counted

Migration `0003_reviews.sql` adds a table rather than a counter column on `issues`, because
the interface shows the history and not just the number:

```sql
CREATE TABLE reviews (
    id         TEXT PRIMARY KEY,
    issue      INTEGER NOT NULL REFERENCES issues(number) ON DELETE CASCADE,
    pr_url     TEXT    NOT NULL,
    cycle      INTEGER NOT NULL,
    run        TEXT REFERENCES runs(id),
    verdict    TEXT CHECK (verdict IS NULL OR verdict IN ('approved', 'changes', 'error')),
    findings   INTEGER NOT NULL DEFAULT 0,
    started_at TEXT NOT NULL,
    ended_at   TEXT,
    UNIQUE (issue, pr_url, cycle)
);
```

Keying on `(issue, pr_url, cycle)` means a coder that opens a *replacement* pull request
— which `coder.md` already provides for — starts its cycles at one again. The cap is per
pull request, not per issue.

The agent records its own verdict before it exits, through a new CLI verb reaching the
store over pib's socket the way every other agent operation does:

```bash
pib review record 44 --verdict changes --findings 2
```

The issue number is a literal the agent copies out of its own briefing, and `$PIB_ISSUE`
is not available to write it: §3's loop deliberately spawns the reviewer without claiming
the issue — `runner.go` sets `PIB_ISSUE` only for a run that carries one — because the
loop's own coder leg is a followup that a claimed issue would misroute to the reviewer.
So the number travels in the task text, exactly as `recheck.Briefing` already passes one,
and `pib review record` takes it as a required positional argument with no environment
fallback.

The verdict comes from the agent rather than from parsing its final message: a review
that says "looks good apart from the SQL injection" must not be read as approval by a
regexp.

`issues.Status` gains `ReviewCycle` and `ReviewVerdict`, derived in `statusQuery` from the
newest row, so the DAG can render `PR #44 · review 2 of 3` without a second query per row.

### 5. Findings go on the pull request; out-of-scope findings go on it differently

In-scope findings are review comments on the diff, which is where the coder reads them and
where the user reads them next to the code. They are **not** filed as pib issues: the fix is
about to happen on the same pull request, and an issue that a coder closes twenty minutes
later is noise in the plan.

A finding the reviewer believes is real but outside the scope of this pull request is a
comment carrying a marker:

```markdown
<!-- pib:out-of-scope plan=orders id=money-type-is-float -->
The money type is a float. Outside this PR, but it will lose cents under rounding.
```

The marker is what makes the next part possible without pib guessing which comments are
proposals. `plan` and `id` are the arguments a `pib issue create` would take, so filing is
a mechanical translation of a comment pib already understands.

### 6. pib files an out-of-scope finding when the user asks it to

`internal/pr` gains `Comments`, reading `gh pr view --json comments,reviews`. Reconciliation
already visits every open linked pull request on a window; it now also collects marked
comments and looks for a reply beneath them.

Deciding whether "yeah, good catch, though maybe later" is a request to file an issue is a
judgment, so it is made by an agent and not by a pattern match. A new `pr-triage` agent —
small, `read` and `bash`, one thread at a time — reads the marked comment and the replies
under it and either files the issue or does nothing.

Idempotency is on the pull request, where the state already lives. Having filed, the agent
replies to the thread:

```markdown
<!-- pib:filed #42 -->
Filed as #42.
```

A thread with a `pib:filed` marker is skipped forever after, which survives pib restarting,
the database being rebuilt, and two pib instances watching one repository. Nothing is
stored locally to get out of sync.

Filed issues land in the plan the marker names, `--type task`, with no blockers — the work
they describe is against code that is merged or about to be.

### 7. `plan-reviewer` gains a closing pass

`plan-reviewer` is launched twice in a plan's life, by two different triggers, with the
same prompt branching on which:

- **Opening pass.** Unchanged: `Store.Apply` files the `plan-review` issue that every root
  waits on, checking the plan against the codebase while changing it is still free.
- **Closing pass.** `recheck.Hook` already fires on every close and already knows how much
  of the plan is left. When the close leaves nothing open but the review itself, it spawns
  a `plan-reviewer` against the plan instead of a `plan-recheck`.

The closing pass is cheap by construction, because the diffs were reviewed as they landed.
It reads the plan's own acceptance criteria and asks whether the plan achieved what it set
out to do — a goal quietly dropped, an acceptance criterion nothing actually satisfies,
scope that drifted across ten pull requests nobody was reading end to end. What it finds it
files as issues, since there is no open pull request left to comment on.

The `reviewer` issue the planner writes today — blocked by every task — is removed from
`planner.md`. It is what the closing pass replaces, and a plan carrying both would review
itself twice.

## Consequences

Positive:
- Review happens where it can change the outcome: on an open pull request, before a human
  looks at it.
- The reviewer's findings get fixed by the loop rather than by the user launching coders
  from a list of filed issues.
- The user's review is of a diff that has already survived up to three adversarial passes.
- Out-of-scope findings stop being either lost or filed presumptuously; they are filed when
  the user says so, in their own words, on the pull request.
- Splitting the two reviewers lets each prompt be about one thing.

Negative:
- Up to six agent runs per pull request where there was a fraction of one. This is the
  real cost of the decision, and `[review] cycles` is the dial.
- pib now spawns agents from a hook the user did not press a key for. The status line in
  [ADR-003](003-horizontal-tui-layout.md) exists partly because of this.
- A wrong verdict from the reviewer burns a coder run on a finding that was not real.
- `pr-triage` reads comments from a pull request anyone can comment on, and files issues
  from them. It files only under a marker pib's own reviewer wrote, but the reply it acts
  on is arbitrary text from GitHub.
- The rename touches `config.toml`, both agent definitions, `planner.md`, and any plan
  already carrying a `reviewer` issue.
