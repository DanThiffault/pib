package issues

// EventKind is what kind of thing changed.
type EventKind string

const (
	// EventPlan means a plan was created or written to.
	EventPlan EventKind = "plan"
	// EventIssue means an issue's row or file changed.
	EventIssue EventKind = "issue"
	// EventRun means an agent run started or ended.
	EventRun EventKind = "run"
	// EventReview means a review cycle was opened or settled.
	EventReview EventKind = "review"
)

// Event says that something changed, and names what. It carries identity
// only, never content: a subscriber reloads what it displays rather than
// reading the change out of the event, which is what lets a subscriber treat
// any event — or a missed one — the same way.
type Event struct {
	Kind  EventKind
	Plan  string
	Issue int64
}

// eventBuffer is how far a subscriber may fall behind before its events are
// dropped. It is generous because every event means the same thing to a
// subscriber — reload — and a full buffer means the subscriber is already
// behind on work it has not done yet.
const eventBuffer = 64

// Subscribe returns a channel of change events and a function that stops the
// subscription and closes the channel. Calling the function more than once is
// harmless.
//
// Events are dropped rather than queued: a subscriber that stops reading
// cannot be allowed to block the agent writing to the store, and a slow one
// catches up by reloading anyway.
func (s *Store) Subscribe() (<-chan Event, func()) {
	events := make(chan Event, eventBuffer)

	s.eventsMu.Lock()
	if s.subscribers == nil {
		s.subscribers = map[chan Event]bool{}
	}
	s.subscribers[events] = true
	s.eventsMu.Unlock()

	return events, func() {
		s.eventsMu.Lock()
		defer s.eventsMu.Unlock()
		if s.subscribers[events] {
			delete(s.subscribers, events)
			close(events)
		}
	}
}

// publish offers an event to every subscriber, dropping it for any subscriber
// whose buffer is full. It never blocks and never fails: a write that has
// landed is a write, whether or not anybody was listening.
func (s *Store) publish(e Event) {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	for events := range s.subscribers {
		select {
		case events <- e:
		default:
		}
	}
}

// publishIssue names the plan an issue belongs to, so a subscriber can decide
// what to reload without a query of its own. An issue number pib cannot
// resolve — a run with no issue, say — still publishes, without a plan.
func (s *Store) publishIssue(kind EventKind, number int64) {
	e := Event{Kind: kind, Issue: number}
	if number > 0 {
		_ = s.db.QueryRow(
			`SELECT p.slug FROM issues i JOIN plans p ON p.id = i.plan_id WHERE i.number = ?`,
			number).Scan(&e.Plan)
	}
	s.publish(e)
}
