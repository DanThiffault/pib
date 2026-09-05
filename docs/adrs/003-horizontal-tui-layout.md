# ADR-003: Horizontal TUI Layout

## Status
Accepted. Supersedes [ADR-001](001-tabbed-tui-plan-browser.md).

## Context

ADR-001 gave the TUI two tabs (Plan, Plans) and split each browser screen into two
vertical panes: a 35% list on the left, detail on the right, joined with
`lipgloss.JoinHorizontal`. Both decisions have not held up.

**The panes are the wrong way round for the content.** Everything pib renders is wide.
A DAG row is a prefix, a number, a title, a state, an agent and a PR reference; an issue
detail is a table of fields. At 116 columns the right pane gets 70, and
`docs/prototypes/layout` shows DAG rows truncating mid-title at exactly the depth where
the tree gets interesting. The material that is coming — review cycles per PR, threaded
out-of-scope comments ([ADR-004](004-pr-review-cycles.md)) — is wider still. Vertical
height is the resource pib has spare; horizontal width is the one it is short of.

**The tab bar hides the list behind the prompt.** "Plan" is one action — describe
something, launch a planner — given equal billing with the entire plan browser, and
costing a keystroke to leave. Starting a plan is rare; looking at plans is what pib is
open for.

**The action bar only exists on two screens.** `actionBarView` renders in
`planDetailTwoPaneView` and `issueFullScreenView`. On the plan list and the prompt there
is a `helpStyle` line with different formatting, or nothing at all, so the BBS convention
the action bar establishes is broken on the first screen anyone sees.

Layouts were compared as pictures rather than as prose. `docs/prototypes/layout` renders
candidate screens at a fixed 116×32 with the real palette; `go run ./docs/prototypes/layout`
prints them.

## Decision

### 1. Panes stack; the split is horizontal

`paneWidths` is replaced by `paneHeights`. Both panes get the full terminal width, and
the terminal's height is divided between them.

```go
const (
    minTopHeight = 4
    maxTopHeight = 12
    topPercent   = 0.35
)

func paneHeights(total int) (top, bottom int) {
    top = int(float64(total) * topPercent)
    if top < minTopHeight {
        top = minTopHeight
    }
    if top > maxTopHeight {
        top = maxTopHeight
    }
    bottom = total - top - 1 // 1-row rule
    if bottom < 3 {
        bottom = 3
    }
    return
}
```

The top pane is a list, which is the thing that reads fine in a few rows and badly in a
narrow column. The bottom pane is whatever the cursor is on. They are joined with
`lipgloss.JoinVertical` and separated by a **titled rule** — `── orders ─────` — so the
divider costs one line and names the lower pane at the same time, rather than spending a
row on a header and another on a border.

`maxTopHeight` matters more than the percentage: a list of forty plans must not push the
detail off the screen. Beyond twelve rows the list scrolls, which `listPane` already does.

**Short terminals, not narrow ones, are now the degenerate case.** `isNarrow` (width < 80)
governed whether the second pane appeared; stacking removes that constraint and
introduces the opposite one. `isShort` (height < 20) collapses to a single pane showing
whichever the cursor is in, and the top pane returns when there is room.

### 2. The tab bar becomes a status line

The first row is no longer navigation, because there is nothing left to navigate between.
It says where pib is and what it is doing:

```
pib · ~/dev/pib · main                                    ● 1 agent running
```

The right side is the only place in the interface that reports background work. Agents
run in tmux windows pib does not own, and until now the only sign one was running was an
issue's state changing under you.

Screens below the top level add a breadcrumb row under the status line:

```
  Plans › orders › #13 Implement Order Aggregate
```

`tab`, `tabKeys`, `tab1Keys`, `tab2Keys`, `switchTab` and `tabBarView` are deleted. The
`TabActive`, `TabInactive` and `TabBar` styles stay in the theme — ADR-002 defines the
vocabulary, and a status line is a use for them.

### 3. `+ New plan` is the first row of the plan list

The Plan tab becomes the first item of the thing that replaced it:

```
  + New plan   describe something to plan
❯ orders                 12 issues   3 ready   1 in review
  ui-horizontal-layout    8 issues   2 ready
```

Selecting it puts the prompt in the **lower pane**, with the plan list still on screen
above (prototype option B; A gave the prompt the whole screen, C kept only a
breadcrumb). B was chosen because it makes the prompt continuous with the list it will
add to — a new plan appears in the rows above where it was typed — and because it keeps
one navigation model instead of two.

The pi art does not survive that: an eight-line mark does not fit a lower pane that is
sixteen rows on a normal terminal and still has to hold a textarea. It moves to the
startup screen, which has the terminal to itself while the workspace checks run.

The screen enum replaces the tab enum:

```go
type screen int

const (
    screenPlans screen = iota   // plans list  + DAG of the selected plan
    screenNewPlan               // plans list  + prompt
    screenPlanDetail            // issue list  + detail of the selected issue
    screenIssue                 // breadcrumb  + full-width issue detail
)
```

`screenNewPlan` is a sibling of `screenPlans` rather than a mode flag on it, so `View`
stays a single `switch` and the prompt's focus rules stay in one place.

### 4. The action bar is on every screen

`actionBarView` moves out of the two browser views and into `View`, as the last row,
unconditionally. Every screen contributes its own actions; a small set is global and
appended everywhere.

| Screen | Actions |
|---|---|
| Plans | `[N]`New plan `[↵]`Open `[R]`Refresh |
| New plan | `[↵]`Plan `[⌥↵]`Newline `[Esc]`Back |
| Plan detail | contextual per `issueActions` |
| Issue | contextual per `issueActions` |
| Global | `[?]`Help `[Q]`Quit |

The notice line keeps its current behaviour of replacing the bar for one render, which is
what makes a transient message visible without spending a permanent row on it.

**Keys move with it.** The bar advertises `[Q]Quit`, so `q` must quit — but only where it
is not also text. The rule is the one already in `Update` for the number keys: character
shortcuts apply exactly when `m.input` does not have focus. `esc` becomes unambiguously
"back", quitting only at the top level, and `ctrl+c` quits from anywhere including the
prompt.

## Consequences

Positive:
- Every row of output gets the full terminal. DAG rows, issue field tables and the
  per-PR review history in ADR-004 stop being truncated at 65% width.
- One navigation model. There is no mode where a keystroke means "switch tab" instead of
  "move the cursor".
- The action bar is a real convention rather than something two screens happen to have.
- The status line gives running agents somewhere to be reported.

Negative:
- `internal/ui/tab_test.go` is 1802 lines built on the tab model and needs reworking
  wholesale, not adjusting.
- Height becomes the contended dimension: a 24-row terminal has ~16 rows of detail after
  the status line, breadcrumb, rule and action bar. `isShort` handles the extreme, but
  the comfortable floor is higher than it was.
- The pi art is off the main screen. It was the only decoration pib had.
- A list and its detail are no longer visible side by side, so comparing two plans means
  moving the cursor rather than reading across.
