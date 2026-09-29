package command

// fixture is a Row with every field settable, so a test can walk a row of any
// kind in any state with any of the derived flags set or clear.
type fixture struct {
	kind           Kind
	state          State
	launchable     bool
	needsAttention bool
	needsInput     bool
	reviewable     bool
	hasRun         bool
	hasPR          bool
}

func (f fixture) Kind() Kind           { return f.kind }
func (f fixture) State() State         { return f.state }
func (f fixture) Launchable() bool     { return f.launchable }
func (f fixture) NeedsAttention() bool { return f.needsAttention }
func (f fixture) NeedsInput() bool     { return f.needsInput }
func (f fixture) Reviewable() bool     { return f.reviewable }
func (f fixture) HasRun() bool         { return f.hasRun }
func (f fixture) HasPR() bool          { return f.hasPR }

// everyState is every lifecycle state the registry knows about.
var everyState = []State{
	StateOpen, StateClosed, StateInProgress, StateAwaitingReview,
	StateBlocked, StatePlanning, StateComplete, StateArchived,
}

// everyKind is every screen's row kind.
var everyKind = []Kind{KindPlan, KindIssue, KindSettings}

// everyRow is the cross product: every kind, in every state, with the four
// derived flags each on and off. It is the population the shared-key
// disjointness test runs over — a predicate pair that can both be true is
// found by trying every combination, not by reading them.
func everyRow() []fixture {
	var rows []fixture
	for _, kind := range everyKind {
		for _, state := range everyState {
			for bits := 0; bits < 64; bits++ {
				rows = append(rows, fixture{
					kind:           kind,
					state:          state,
					launchable:     bits&1 != 0,
					needsAttention: bits&2 != 0,
					needsInput:     bits&4 != 0,
					reviewable:     bits&8 != 0,
					hasRun:         bits&16 != 0,
					hasPR:          bits&32 != 0,
				})
			}
		}
	}
	return rows
}
