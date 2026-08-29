package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"pib/internal/issueops"
	"pib/internal/issues"
	"pib/internal/protocol"
)

// Rendering has two modes. With --json the payload goes to standard output
// untouched and nothing else is printed, so a caller can pipe it straight
// into a parser. Without it, the reply is written as text and warnings go to
// standard error, where they will not spoil the output being read.

func (a App) renderApply(resp protocol.Response, asJSON bool) error {
	var result issues.ApplyResult
	if done, err := a.decode(resp, asJSON, &result); done || err != nil {
		return err
	}

	fmt.Fprintf(a.Stdout, "Applied plan %s — %s.\n", result.Plan.Slug, changes(result))
	if len(result.Created) > 0 {
		fmt.Fprintf(a.Stdout, "  created %s\n", render(result.Created))
	}
	if len(result.Updated) > 0 {
		fmt.Fprintf(a.Stdout, "  updated %s\n", render(result.Updated))
	}
	a.warn(result.Warnings)
	return nil
}

func (a App) renderPlanList(resp protocol.Response, asJSON bool) error {
	var result issueops.PlanList
	if done, err := a.decode(resp, asJSON, &result); done || err != nil {
		return err
	}

	if len(result.Plans) == 0 {
		fmt.Fprintln(a.Stdout, "No plans yet. Run pib to make one.")
		return nil
	}

	w := table(a.Stdout)
	fmt.Fprintln(w, "PLAN\tTITLE\tCREATED")
	for _, plan := range result.Plans {
		fmt.Fprintf(w, "%s\t%s\t%s\n", plan.Slug, plan.Title, day(plan.CreatedAt))
	}
	return w.Flush()
}

func (a App) renderPlanDetail(resp protocol.Response, asJSON bool) error {
	var result issueops.PlanDetail
	if done, err := a.decode(resp, asJSON, &result); done || err != nil {
		return err
	}

	fmt.Fprintf(a.Stdout, "%s — %s\n\n", result.Plan.Slug, result.Plan.Title)
	a.issueTable(result.Issues)
	a.warn(result.Warnings)
	return nil
}

func (a App) renderList(resp protocol.Response, asJSON bool) error {
	var result issueops.StatusList
	if done, err := a.decode(resp, asJSON, &result); done || err != nil {
		return err
	}

	a.issueTable(result.Issues)
	a.warn(result.Warnings)
	return nil
}

func (a App) renderDetail(resp protocol.Response, asJSON bool) error {
	var result issueops.IssueDetail
	if done, err := a.decode(resp, asJSON, &result); done || err != nil {
		return err
	}

	issue := result.Issue
	fmt.Fprintf(a.Stdout, "#%d  %s\n", issue.Number, issue.Title)
	fmt.Fprintf(a.Stdout, "%s\n", strings.Join(nonEmpty(
		issue.Plan, issue.Type, word(issue), note(issue), issue.LocalID,
	), " · "))

	if len(issue.Acceptance) > 0 {
		fmt.Fprintln(a.Stdout, "\nAcceptance")
		for _, criterion := range issue.Acceptance {
			fmt.Fprintf(a.Stdout, "  - %s\n", criterion)
		}
	}
	if body := strings.TrimSpace(result.Body); body != "" {
		fmt.Fprintf(a.Stdout, "\n%s\n", body)
	}
	if len(result.Runs) > 0 {
		fmt.Fprintln(a.Stdout, "\nRuns")
		w := table(a.Stdout)
		for _, run := range result.Runs {
			outcome := run.Status
			if outcome == "" {
				outcome = "running"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", run.Agent, outcome, moment(run.StartedAt), run.Window)
		}
		w.Flush()
	}
	if len(result.Comments) > 0 {
		fmt.Fprintln(a.Stdout, "\nActivity")
		for _, comment := range result.Comments {
			fmt.Fprintf(a.Stdout, "\n  %s · %s\n", comment.Author, moment(comment.At))
			for _, line := range strings.Split(strings.TrimSpace(comment.Body), "\n") {
				fmt.Fprintf(a.Stdout, "    %s\n", line)
			}
		}
	}
	return nil
}

func (a App) renderClose(resp protocol.Response, asJSON bool) error {
	var result issueops.CloseResult
	if done, err := a.decode(resp, asJSON, &result); done || err != nil {
		return err
	}

	fmt.Fprintf(a.Stdout, "Closed #%d — %s.\n", result.Issue.Number, result.Issue.Title)
	a.warn(result.Warnings)
	return nil
}

func (a App) renderReindex(resp protocol.Response, asJSON bool) error {
	var result issueops.ReindexResult
	if done, err := a.decode(resp, asJSON, &result); done || err != nil {
		return err
	}

	fmt.Fprintf(a.Stdout, "Re-read %s.\n", plural(result.Refreshed, "issue file", "issue files"))
	return nil
}

// decode either prints the payload as json and reports that it is done, or
// unmarshals it for the caller to render as text.
func (a App) decode(resp protocol.Response, asJSON bool, into any) (bool, error) {
	if asJSON {
		var indented bytes.Buffer
		if err := json.Indent(&indented, resp.Payload, "", "  "); err != nil {
			return true, err
		}
		fmt.Fprintln(a.Stdout, indented.String())
		return true, nil
	}
	if len(resp.Payload) == 0 {
		return true, nil
	}
	return false, json.Unmarshal(resp.Payload, into)
}

// issueTable is the listing every multi-issue command shares.
func (a App) issueTable(list []issues.Status) {
	if len(list) == 0 {
		fmt.Fprintln(a.Stdout, "No issues match.")
		return
	}

	w := table(a.Stdout)
	fmt.Fprintln(w, "ISSUE\tSTATE\tTYPE\tTITLE\tNOTE")
	for _, issue := range list {
		fmt.Fprintf(w, "#%d\t%s\t%s\t%s\t%s\n",
			issue.Number, word(issue), issue.Type, issue.Title, note(issue))
	}
	w.Flush()
}

// word is the one state an issue is in. The order matters: an issue can be
// several things at once, and this reports the one that explains it.
func word(issue issues.Status) string {
	switch {
	case issue.State == issues.StateClosed:
		return "closed"
	case issue.InProgress:
		return "in progress"
	case issue.AwaitingReview:
		return "in review"
	case issue.Blocked:
		return "blocked"
	case issue.Ready:
		return "ready"
	default:
		return "open"
	}
}

// note says what would happen next, or what is in the way.
func note(issue issues.Status) string {
	switch {
	case issue.State == issues.StateClosed:
		return ""
	case issue.InProgress:
		return strings.TrimSpace(issue.Agent + " running")
	case issue.AwaitingReview:
		return issue.PRURL
	case issue.Blocked:
		return "waiting on " + render(issue.OpenBlockers)
	case issue.Ready && issue.Launchable:
		return issue.Agent
	case issue.Ready:
		return "no agent for this type"
	default:
		return ""
	}
}

// warn puts problems on standard error, where they cannot spoil output that
// is being read by something else.
func (a App) warn(warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintf(a.Stderr, "warning: %s\n", warning)
	}
}

func changes(result issues.ApplyResult) string {
	switch {
	case len(result.Created) > 0 && len(result.Updated) > 0:
		return fmt.Sprintf("%s, %s",
			plural(len(result.Created), "issue created", "issues created"),
			plural(len(result.Updated), "updated", "updated"))
	case len(result.Created) > 0:
		return plural(len(result.Created), "issue created", "issues created")
	case len(result.Updated) > 0:
		return plural(len(result.Updated), "issue updated", "issues updated")
	default:
		return "nothing changed"
	}
}

func render(numbers []int64) string {
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

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02")
}

func moment(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

func table(w interface{ Write([]byte) (int, error) }) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}
