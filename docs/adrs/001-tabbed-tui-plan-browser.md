# ADR-001: Tabbed TUI Architecture for Plan Browser

## Status
Accepted

## Context

The pib TUI currently presents a single textarea prompt for launching planners. We need to add a read-only plan browser with tabbed navigation and drill-down views without rewriting the existing startup phase logic or breaking the test strategy.

Constraints:
- Single Bubble Tea `tea.Model` with phase-driven startup (`internal/ui/model.go`)
- Views are built manually with `strings.Builder` and `lipgloss` — no `bubbles/table`, `list`, or `viewport` components yet
- Store queries (`Store.Plans`, `Store.Statuses`) are synchronous SQLite calls on the main goroutine
- Tests call `Update`/`View` directly and mock package-level variables (`launchPlanner`)
- The terminal size is tracked in `Model.width` / `Model.height`

## Decision

### 1. Tab State Model and Sub-View Transitions

We will add a flat tab enum and a sub-view enum to `Model`. `Update` and `View` dispatch on `currentTab`, and `tabPlans` further dispatches on `plansView`.

```go
type tab int
const (
    tabPlan tab = iota   // original prompt
    tabPlans             // plan browser
)

type plansView int
const (
    viewPlanList plansView = iota
    viewPlanDetail
)
```

**Why a flat state machine over nested `tea.Model`s?**
- The existing codebase has one model and one `Update`/`View` pair. Adding nested models would require wiring `tea.Msg` routing, `Init()` chaining, and `WindowSizeMsg` broadcasting, all for views that share the same `Store` and terminal size.
- A flat enum keeps the dispatch explicit (`switch m.currentTab`) and keeps tests simple: they continue to call `Update` directly with concrete messages.
- Sub-view transitions inside `tabPlans` use a second enum (`plansView`) so that `←` / `esc` has a deterministic target.

Transition rules:
- `tabPlan` → `tabPlans` via `1`/`2`/`tab` keys
- `tabPlans` + `viewPlanList` → `viewPlanDetail` via `→`/`enter` on a selected plan
- `tabPlans` + `viewPlanDetail` → `viewPlanList` via `←`/`esc`
- Startup phases remain unchanged; `phasePrompt` is simply reached while `currentTab == tabPlan`

### 2. Async Loading Patterns for Bubble Tea

All store I/O will be wrapped in `tea.Cmd` functions that return custom message types. The UI will never call `Store.Plans()` or `Store.Statuses()` directly from `Update`.

Pattern:

```go
type plansLoadedMsg struct {
    plans []issues.Plan
    err   error
}

func loadPlans(store *issues.Store) tea.Cmd {
    return func() tea.Msg {
        plans, err := store.Plans()
        return plansLoadedMsg{plans: plans, err: err}
    }
}
```

State tracking:
- `plansLoading bool` (or `plansSpinner string`) in `Model`
- When the user switches to `tabPlans` for the first time, return `loadPlans(m.store)`
- On `plansLoadedMsg`, clear the loading flag and store the result
- On error, store `err` in a tab-local field (`plansErr error`) and render an error inline rather than crashing to `phaseFailed`

**Why not `tea.Batch` for parallel loads?**
- For the initial implementation we load only what the current view needs (plan list first, issue list on drill-down). This keeps message handling simple and avoids over-fetching.
- If we later need parallel loads (e.g., plan list + issue counts), `tea.Batch` is the standard Bubble Tea tool and can be adopted without changing this pattern.

### 3. Two-Pane Layout Algorithm and Responsive Sizing

Each tab view that needs two panes will split `m.width` after reserving space for the tab bar and help line.

Algorithm:

```go
const (
    minLeftWidth  = 20
    maxLeftWidth  = 45
    leftPercent   = 0.35
)

func paneWidths(total int) (left, right int) {
    left = int(float64(total) * leftPercent)
    if left < minLeftWidth {
        left = minLeftWidth
    }
    if left > maxLeftWidth {
        left = maxLeftWidth
    }
    right = total - left - 1 // 1-col divider/gutter
    if right < 10 {
        right = 10
    }
    return
}
```

Height:
- Subtract tab bar height (1 line + border) and help text height (1 line + padding)
- Remaining height is shared by both panes; overflow is truncated with `lipgloss.NewStyle().MaxHeight()`

Pane rendering:
- Each pane is rendered independently as a string with `lipgloss.NewStyle().Width(w).Height(h)`
- Panes are joined horizontally with `lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)`
- Selected list items use an inverted style (`lipgloss.NewStyle().Background(...).Foreground(...)`)

Responsive behavior:
- If `m.width < 60`, the two-pane view collapses to a single full-width list; the right-pane metadata is shown on `→`/`enter` as a full-screen overlay (future enhancement). For the first version we keep the 35/65 split with `minLeftWidth` clamping.

### 4. Keybinding Conventions

We will use `github.com/charmbracelet/bubbles/key` bindings declared as package-level variables, matching the existing style in `prompt.go`.

Global bindings (active in all tabs after startup):

| Binding     | Key(s)         | Action                    |
|-------------|----------------|---------------------------|
| `tabKeys`   | `tab`          | Cycle to next tab         |
| `tab1Keys`  | `1`            | Jump to tabPlan           |
| `tab2Keys`  | `2`            | Jump to tabPlans          |

Tab-local bindings:

**tabPlan (prompt)**
| Binding     | Key(s)         | Action                    |
|-------------|----------------|---------------------------|
| `submitKeys`| `enter`        | Launch planner            |
| `newlineKeys`| `alt+enter`   | Insert newline in textarea|
| `cancelKeys`| `esc`, `ctrl+c`| Quit                      |

**tabPlans / viewPlanList**
| Binding     | Key(s)         | Action                    |
|-------------|----------------|---------------------------|
| `upKeys`    | `↑`            | Move cursor up            |
| `downKeys`  | `↓`            | Move cursor down          |
| `selectKeys`| `→`, `enter`   | Open plan detail          |

**tabPlans / viewPlanDetail**
| Binding     | Key(s)         | Action                    |
|-------------|----------------|---------------------------|
| `upKeys`    | `↑`            | Move cursor up (issues)   |
| `downKeys`  | `↓`            | Move cursor down (issues) |
| `backKeys`  | `←`, `esc`     | Return to plan list       |

Keybinding resolution order in `Update`:
1. Global tab-switching keys
2. Sub-view keys (`backKeys` in detail view)
3. Tab-local keys (`up`/`down`/`select`)
4. Pass through to underlying component (textarea in `tabPlan`)

This ordering prevents `esc` from quitting the app while the user is in a drill-down view.

## Consequences

Positive:
- Flat state machine keeps the existing single-model test strategy intact
- Async commands prevent UI blocking even though SQLite is local and fast
- Percentage-based pane sizing adapts to terminal resizes automatically via `tea.WindowSizeMsg`
- Explicit keybinding tables make it easy to add a help overlay later

Negative:
- Flat model will grow as tabs and sub-views accumulate; if we add a third tab or deeper navigation we should re-evaluate nested models
- No built-in scrollable components means we must implement list truncation and cursor clipping manually
- `tabPlans` data is fetched on first entry and held in memory; there is no refresh mechanism yet
