package runner

import (
	"fmt"
	"strings"

	"pib/internal/issues"
)

// Briefing is what the agent is started with. It is deliberately thin: the
// issue is the specification, and every agent's first step is to read it.
func Briefing(number int64, title string) string {
	return fmt.Sprintf(
		"Work pib issue #%d: %s\n\n"+
			"Read it first with `pib issue view %d` — that is your specification, "+
			"including its acceptance criteria. Your issue number is also in PIB_ISSUE.",
		number, title, number)
}

// Blocking says in one phrase why an issue cannot start.
func Blocking(issue issues.Status) string {
	switch {
	case issue.State == issues.StateClosed:
		return "it is closed"
	case issue.InProgress:
		return "an agent is already working on it"
	case issue.AwaitingReview:
		return "it is waiting on " + issue.PRURL
	case issue.Blocked:
		return "it is waiting on " + renderBlockers(issue.OpenBlockers)
	default:
		return "unknown"
	}
}

func renderBlockers(numbers []int64) string {
	parts := make([]string, 0, len(numbers))
	for _, number := range numbers {
		parts = append(parts, fmt.Sprintf("#%d", number))
	}
	switch len(parts) {
	case 0:
		return "nothing"
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}
