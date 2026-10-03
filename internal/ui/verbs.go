package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/editor"
	"pib/internal/issueops"
	"pib/internal/issues"
	"pib/internal/protocol"
	"pib/internal/recheck"
	"pib/internal/session"
)

// This file carries out the lifecycle verbs of ADR-006 §1. The registry's
// handlers only name what was asked for; everything here runs in Update with
// the live model. Free text goes to $EDITOR (ADR-007); retry and answer go
// through issueops so a retry or an answer from the TUI settles exactly as
// one over the socket would.

// Editor results: what was written above the scissors, or why there is
// nothing. editor.ErrAborted is the user leaving the buffer empty.
type commentTextMsg struct {
	issue issues.Status
	text  string
	err   error
}
type followupTextMsg struct {
	issue issues.Status
	text  string
	err   error
}
type answerTextMsg struct {
	issue issues.Status
	text  string
	err   error
}

// editResultMsg reports the editor closed on an issue's file. changed is
// whether the file moved; a change has already been reindexed by the time
// this arrives.
type editResultMsg struct {
	issue   issues.Status
	changed bool
	err     error
}

// Operation results, one per verb that settles in the background.
type commentResultMsg struct {
	number int64
	err    error
}
type retryResultMsg struct {
	issue issues.Status
	err   error
}
type answerResultMsg struct {
	number int64
	err    error
}
type followupResultMsg struct {
	issue issues.Status
	err   error
}
type closeResultMsg struct {
	number   int64
	warnings []string
	err      error
}
type reopenResultMsg struct {
	number int64
	err    error
}
type prResultMsg struct{ err error }
type archiveResultMsg struct {
	slug     string
	archived bool
	err      error
}
type reviewResultMsg struct {
	plan   string
	status string
	err    error
}

// compose suspends pib for $EDITOR on the template, git-commit style, and
// delivers whatever was written — or why nothing was — through reply.
func compose(t editor.Template, reply func(text string, err error) tea.Msg) tea.Cmd {
	cmd, done := editor.Compose(t)
	return execProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return reply("", err)
		}
		return reply(done())
	})
}

// recentComments is the context comment and followup offer below the
// scissors: the issue's last three comments, oldest of the three first.
func (m Model) recentComments(number int64) []editor.Section {
	if m.store == nil {
		return nil
	}
	file, err := m.store.Content(number)
	if err != nil || len(file.Comments) == 0 {
		return nil
	}
	comments := file.Comments
	if len(comments) > 3 {
		comments = comments[len(comments)-3:]
	}
	parts := make([]string, 0, len(comments))
	for _, c := range comments {
		parts = append(parts, c.Author+" · "+ago(c.At)+"\n"+strings.TrimRight(c.Body, "\n"))
	}
	return []editor.Section{{Title: "recent comments", Body: strings.Join(parts, "\n\n")}}
}

// editorFailed reports an editor result that produced no text — an empty
// buffer aborts, the way an emptied commit message aborts a commit — and
// leaves the notice for it. It answers whether the verb is done.
func (m *Model) editorFailed(verb string, err error) bool {
	switch {
	case errors.Is(err, editor.ErrAborted):
		m.notice = verb + " aborted — nothing written"
		return true
	case err != nil:
		m.notice = "could not " + verb + ": " + err.Error()
		return true
	}
	return false
}

// handleComment opens $EDITOR for a comment on the issue.
func (m Model) handleComment(issue issues.Status) (tea.Model, tea.Cmd) {
	t := editor.Template{
		Header:  fmt.Sprintf("comment on #%d · %s", issue.Number, issue.Title),
		Context: m.recentComments(issue.Number),
	}
	return m, compose(t, func(text string, err error) tea.Msg {
		return commentTextMsg{issue: issue, text: text, err: err}
	})
}

// commentAuthor names who is at the keyboard, as `pib issue comment` does.
func commentAuthor() string {
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	return "human"
}

func commentCmd(store *issues.Store, number int64, text string) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return commentResultMsg{number: number, err: errors.New("no store")}
		}
		return commentResultMsg{number: number, err: store.Comment(number, commentAuthor(), text)}
	}
}

// latestEndedRun finds the run a followup continues. The newest run still
// going is a refusal, as `pib issue followup` refuses: a live agent cannot
// be resumed.
func latestEndedRun(store *issues.Store, number int64) (issues.Run, error) {
	if store == nil {
		return issues.Run{}, errors.New("no store")
	}
	run, ok, err := store.LatestRun(number)
	if err != nil {
		return issues.Run{}, err
	}
	if !ok {
		return issues.Run{}, fmt.Errorf("#%d has never been worked on — start it first", number)
	}
	if run.EndedAt.IsZero() {
		return issues.Run{}, fmt.Errorf("an agent is already working on #%d", number)
	}
	return run, nil
}

// handleFollowup opens $EDITOR for a note to the agent that last worked the
// issue. The guard runs before the editor opens, so a live run does not cost
// the user a written note.
func (m Model) handleFollowup(issue issues.Status) (tea.Model, tea.Cmd) {
	run, err := latestEndedRun(m.store, issue.Number)
	if err != nil {
		m.notice = err.Error()
		return m, nil
	}
	t := editor.Template{
		Header:  fmt.Sprintf("followup to #%d · %s · %s", issue.Number, issue.Title, run.Agent),
		Context: m.recentComments(issue.Number),
	}
	return m, compose(t, func(text string, err error) tea.Msg {
		return followupTextMsg{issue: issue, text: text, err: err}
	})
}

// followupCmd resumes the newest ended run with the note, as `pib issue
// followup` does: the agent keeps everything it worked out, and the resume
// blocks until it stops again. A run that went live while the editor was
// open is refused here rather than given a second window on one session.
func followupCmd(store *issues.Store, agents spawner, issue issues.Status, text string) tea.Cmd {
	return func() tea.Msg {
		run, err := latestEndedRun(store, issue.Number)
		if err != nil {
			return followupResultMsg{issue: issue, err: err}
		}
		if agents == nil {
			return followupResultMsg{issue: issue, err: errors.New("agent runner is not available")}
		}
		resp, err := agents.Run(context.Background(), protocol.Request{
			Op:      protocol.OpResume,
			Session: run.ID,
			Answer:  text,
			Name:    fmt.Sprintf("%s #%d", run.Agent, issue.Number),
			Issue:   issue.Number,
		})
		if err != nil {
			return agentFinishedMsg{issue: issue, err: err}
		}
		return agentFinishedMsg{issue: issue, status: resp.Status}
	}
}

// handleAnswer opens $EDITOR for the reply to what the agent asked, with the
// question below the scissors. The question lives in the run's exit sidecar,
// not the store.
func (m Model) handleAnswer(issue issues.Status) (tea.Model, tea.Cmd) {
	agentName := issue.Agent
	var context []editor.Section
	if m.store != nil {
		if run, ok, err := m.store.LatestRun(issue.Number); err == nil && ok {
			agentName = run.Agent
			if exit, found, err := session.ReadExit(filepath.Join(m.workspace.Dir, "runs", run.ID)); err == nil && found && exit.Message != "" {
				context = append(context, editor.Section{Title: "the agent asked", Body: exit.Message})
			}
		}
	}
	t := editor.Template{
		Header:  fmt.Sprintf("answer to #%d · %s · %s", issue.Number, issue.Title, agentName),
		Context: context,
	}
	return m, compose(t, func(text string, err error) tea.Msg {
		return answerTextMsg{issue: issue, text: text, err: err}
	})
}

// issueOp sends a lifecycle operation through the same issueops handler the
// socket serves, so the TUI does not assemble the steps itself: the handler
// unlinks a closed pull request, resets an exhausted review count, and starts
// or resumes the run through its own spawner.
func (m Model) issueOp(op protocol.Op, params any, reply func(error) tea.Msg) tea.Cmd {
	store, cfg, agents := m.store, m.cfg, m.agents
	return func() tea.Msg {
		if store == nil {
			return reply(errors.New("no store"))
		}
		payload, err := json.Marshal(params)
		if err != nil {
			return reply(err)
		}
		h := issueops.Handler{Store: store, Config: cfg, Spawn: agents}
		_, err = h.Run(context.Background(), protocol.Request{Op: op, Payload: payload})
		return reply(err)
	}
}

// handleEdit opens the issue's own markdown file — frontmatter and body, not
// a temp file — and reindexes when it changed. A content-changing reindex
// bumps updated_at, which is what takes an edited issue out of needs
// attention (ADR-005 §2).
func (m Model) handleEdit(issue issues.Status) (tea.Model, tea.Cmd) {
	if m.store == nil {
		m.notice = "no store"
		return m, nil
	}
	store := m.store
	cmd, changed := editor.Open(filepath.Join(store.Dir(), issue.Path))
	return m, execProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return editResultMsg{issue: issue, err: err}
		}
		moved, err := changed()
		if err != nil {
			return editResultMsg{issue: issue, err: err}
		}
		if moved {
			if _, err := store.Reindex(issue.Plan); err != nil {
				return editResultMsg{issue: issue, err: err}
			}
		}
		return editResultMsg{issue: issue, changed: moved}
	})
}

func closeIssueCmd(store *issues.Store, number int64, reason string) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return closeResultMsg{number: number, err: errors.New("no store")}
		}
		_, warnings, err := store.CloseIssue(number, reason)
		return closeResultMsg{number: number, warnings: warnings, err: err}
	}
}

func reopenIssueCmd(store *issues.Store, number int64) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return reopenResultMsg{number: number, err: errors.New("no store")}
		}
		_, err := store.ReopenIssue(number)
		return reopenResultMsg{number: number, err: err}
	}
}

// openInBrowser is open on macOS, xdg-open elsewhere; named so a test can
// stand in for the desktop.
var openInBrowser = func(url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	return exec.Command(name, url).Run()
}

func openPRCmd(url string) tea.Cmd {
	return func() tea.Msg {
		return prResultMsg{err: openInBrowser(url)}
	}
}

// handleBlockers moves the cursor to the first blocker still holding the
// issue up.
func (m Model) handleBlockers(issue issues.Status) (tea.Model, tea.Cmd) {
	if len(issue.OpenBlockers) == 0 {
		m.notice = fmt.Sprintf("#%d has no open blockers", issue.Number)
		return m, nil
	}
	target := issue.OpenBlockers[0]
	for i, candidate := range m.visibleIssues() {
		if candidate.Number == target {
			m.issueCursor = i
			m.issueScroll = 0
			m.notice = ""
			return m, m.selectIssueContent()
		}
	}
	m.notice = fmt.Sprintf("#%d is hidden by the filters", target)
	return m, nil
}

func archivePlanCmd(store *issues.Store, slug string, archive bool) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return archiveResultMsg{slug: slug, archived: archive, err: errors.New("no store")}
		}
		var err error
		if archive {
			err = store.ArchivePlan(slug)
		} else {
			err = store.UnarchivePlan(slug)
		}
		return archiveResultMsg{slug: slug, archived: archive, err: err}
	}
}

// handleReviewPlan starts the plan-reviewer on a plan row. There is no
// reviewer issue to start: the run is traced to its plan by runs.plan and
// runs.pass, exactly as `pib plan review` and the recheck hook's closing
// pass trace theirs.
func (m Model) handleReviewPlan(plan issues.PlanStatus) (tea.Model, tea.Cmd) {
	if m.agents == nil {
		m.notice = "Agent runner is not available"
		return m, nil
	}
	pass := issues.PassOpening
	task := recheck.OpeningBriefing(plan.Slug)
	if plan.State == issues.PlanAwaitingClosingReview {
		pass = issues.PassClosing
		task = recheck.ClosingBriefing(plan.Slug)
	}
	agents := m.agents
	m.notice = "reviewing plan " + plan.Slug
	return m, func() tea.Msg {
		resp, err := agents.Run(context.Background(), protocol.Request{
			Op:    protocol.OpSpawn,
			Agent: recheck.ReviewerName,
			Name:  "review " + plan.Slug,
			Task:  task,
			Plan:  plan.Slug,
			Pass:  pass,
		})
		if err != nil {
			return reviewResultMsg{plan: plan.Slug, err: err}
		}
		return reviewResultMsg{plan: plan.Slug, status: resp.Status}
	}
}
