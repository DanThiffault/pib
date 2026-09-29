# ADR-005: Plan and issue state machines

## Status
Proposed. Extends the derived-state model in `docs/issue-tracking.md` and
`internal/issues/status.go`.

## Context

The TUI is being rebuilt around one idea: a live table of plans, and under each plan a
live table of issues, where the user selects a row and issues the command that moves it
to its next step. For that to work the set of states a row can be in — and the commands
legal in each — has to be explicit. Today the issue side is partly there (`blocked`,
`in_progress`, `awaiting_review`, `ready`, `launchable` derived in one CTE) and the plan
side does not exist at all: a plan is a slug and a title.

Two situations have no state today and so nothing can act on them:

- **A run that ended badly.** `runs.status` records `error` and `needs_input`, but the
  issue simply falls back to `ready`, indistinguishable from one that was never tried.
- **A pull request that cannot be resolved by review.** A PR closed unmerged, or a
  `changes` verdict after the last review cycle, leaves the issue `open` with nothing
  telling the user it is now theirs.

Both are the same thing: the machine has stopped and needs a human. Ambiguity in a plan
that only surfaces mid-implementation is the same thing again.

## Decision

### 1. Plan states, derived from the plan's issues and runs

| State | Derived from | Commands |
|---|---|---|
| **planning** | a `planner` run with no `plans.planner_run` pointing at it yet | jump, kill |
| **awaiting review** | applied; opening `plan-reviewer` issue open, no run | review, archive |
| **under review** | `plan-reviewer` issue has an open run | jump, kill |
| **in progress** | opening review closed; at least one other issue open | start all, review, archive |
| **awaiting closing review** | every non-reviewer issue closed; closing review issue open | review, jump, kill |
| **complete** | every issue closed | archive |
| **archived** | `plans.archived_at` set | unarchive |

`planning` rows are placeholders: they exist in the table from the moment the new-plan
command spawns a planner, so the run is reachable (jump, kill) before `pib plan apply`
gives it a record. `archived` is the only stored plan state; it is a `plans.archived_at`
column added by migration, and archived plans are hidden by default. There is no delete.

There is no "skip review": `[plan] review = false` in config already suppresses the
opening review issue, in which case a fresh plan starts in **in progress**.

### 2. Issue states

The existing derivation stands, with one addition, **needs attention**, evaluated before
`ready`:

```
needs_attention = state = 'open' AND NOT in_progress AND (
    last_run.status IN ('error', 'needs_input')
    AND last_run.ended_at > issues.updated_at              -- not yet edited since
 OR pr_url IS NOT NULL AND pr_state = 'closed'             -- PR rejected
 OR review_cycle >= [review].cycles AND review_verdict = 'changes'
)
ready = state = 'open' AND NOT blocked AND NOT in_progress
        AND NOT awaiting_review AND NOT needs_attention
```

Each row carries an `attention_reason` string (`agent failed`, `agent asked a question`,
`pull request closed`, `review cycles exhausted`) so the table can say why.

| State | Commands |
|---|---|
| **blocked** | edit, comment, blockers, close |
| **ready** (type unmapped) | edit, comment, close — bar explains the type has no agent |
| **launchable** | start, edit, comment, close |
| **in progress** | jump, kill, followup |
| **awaiting review** | open PR, followup, comment, jump / kill (while a review cycle runs) |
| **needs attention** | retry, answer (when `needs_input`), edit, comment, close, open PR (when a PR exists) |
| **closed** | reopen, comment |

Leaving **needs attention**:

- **retry** starts a new run of the issue's agent. If the PR was closed unmerged the
  link is cleared first so the coder opens a fresh one.
- **answer** resumes the stopped agent with the user's text (`protocol.OpResume`).
- **edit** bumps `issues.updated_at` past the failed run, and the issue is `ready`
  again — the user changed something so a different thing will happen.
- **close** abandons it.

Nothing about attention is stored: it is derived from data already written by runs,
reviews and PR rechecks, so the CLI and the TUI can never disagree about it.

### 3. Manual close stays, and is expected to be rare

`close` is available on any open issue from the TUI. Most closes are still automatic —
task PRs merging, reviewers closing their own issues.

## Consequences

Positive: every row has exactly one state and an enumerable set of commands; the
command bar, the `:` line and the help are all generated from one table. The user is
told when the machine has stopped instead of discovering it.

Negative: one more column in `plans`, and one more sub-select in an already dense CTE.
"Planning" rows are synthesised from the `runs` table rather than read from `plans`,
so the plan list is a union of two queries.
