package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"pib/internal/issues"
)

// The prose a plan review leaves behind: a task, and the two comments an agent
// and a reviewer put under it.
const (
	proseBody = "Check every issue in this plan against the code it will change,\n" +
		"while changing an issue is still free."
	reviewComment = "## Plan review — ui-and-review-workflow\n" +
		"Fourteen issues checked against the code they will change."
	coderComment = "Fixed the truncation in `pad`; the action bar stays put."
)

func reviewedAt(hour, minute int) time.Time {
	return time.Date(2026, 9, 4, hour, minute, 0, 0, time.UTC)
}

// proseModel is a model parked on one issue whose markdown file is already
// held, which is the only place the interface gets prose from. There is no
// store attached: a render must never reach one.
func proseModel(t *testing.T) Model {
	t.Helper()
	m := proseModelWith(t, nil)
	m.issueProse = reviewedProse()
	return m
}

func reviewedProse() map[int64]issueProse {
	return map[int64]issueProse{7: {file: issues.File{
		Body: proseBody,
		Comments: []issues.Comment{
			{Author: "plan-reviewer", At: reviewedAt(20, 38), Body: reviewComment},
			{Author: "coder", At: reviewedAt(21, 5), Body: coderComment},
		},
	}}}
}

// proseModelWith is the same model with no prose held, so a test can fill the
// cache the way the interface does: by running the command and delivering its
// message.
func proseModelWith(t *testing.T, store *issues.Store) Model {
	t.Helper()
	m := plansModel(t, []issues.Plan{{Slug: "orders", Title: "Orders"}})
	m.screen = screenIssue
	m.store = store
	m.planIssues = []issues.Status{{
		Issue: issues.Issue{Number: 7, Title: "Show the body", State: issues.StateOpen, Type: "coder"},
	}}
	m.planIssuesLoadedFor = "orders"
	return m
}

// A body's only home is the issue file, and the full-screen view is the pane
// with the room to hold it. Without this the prose is readable only by opening
// the markdown by hand.
func TestFullScreenShowsTheIssueBody(t *testing.T) {
	view := proseModel(t).issueFullScreenView()

	for _, want := range []string{"Description", "Check every issue in this plan", "while changing an issue is still free"} {
		if !strings.Contains(view, want) {
			t.Errorf("full-screen view missing %q:\n%s", want, view)
		}
	}
}

// A comment is a finding somebody wrote down, and a finding with no author and
// no time on it cannot be acted on or reasoned about.
func TestFullScreenShowsEveryCommentWithItsAuthorAndTime(t *testing.T) {
	view := proseModel(t).issueFullScreenView()

	for _, want := range []string{
		"Comments (2)",
		"plan-reviewer · 2026-09-04 20:38", "Fourteen issues checked",
		"coder · 2026-09-04 21:05", "the action bar stays put",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("full-screen view missing %q:\n%s", want, view)
		}
	}
}

// A comment thread is a conversation. Sorting the comments, or the pane showing
// only the most recent, would put a reply above the finding it answers, which
// reads as a finding nobody made.
func TestCommentsAreShownInTheOrderTheFileStoresThem(t *testing.T) {
	m := proseModel(t)
	m.issueProse = map[int64]issueProse{7: {file: issues.File{Comments: []issues.Comment{
		{Author: "zeta", At: reviewedAt(9, 0), Body: "last comment's body"},
		{Author: "alpha", At: reviewedAt(8, 0), Body: "first comment's body"},
	}}}}

	view := m.issueFullScreenView()
	zeta, alpha := strings.Index(view, "zeta"), strings.Index(view, "alpha")
	if zeta < 0 || alpha < 0 {
		t.Fatalf("full-screen view is missing a comment:\n%s", view)
	}
	if zeta > alpha {
		t.Errorf("comments were reordered; the file stores zeta first:\n%s", view)
	}
}

// The file stores a comment's time as RFC 3339, which is precise and unreadable
// at a glance. The other timestamps in the pane set the form this one follows.
func TestCommentTimesUseTheFormTheRestOfThePaneUses(t *testing.T) {
	head := commentHead(issues.Comment{Author: "plan-reviewer", At: reviewedAt(20, 38)})

	if head != "plan-reviewer · 2026-09-04 20:38" {
		t.Errorf("commentHead = %q, want the pane's timestamp form", head)
	}
}

// A read that failed is not the same as an issue with nothing written in it.
// Silently showing metadata alone would tell the reader the issue has no
// description, which is a claim about the work rather than about pib.
func TestAFailedReadSaysSoRatherThanShowingAnIssueWithNoDescription(t *testing.T) {
	m := proseModel(t)
	m.issueProse = map[int64]issueProse{7: {err: errors.New("no such file")}}

	view := m.issueFullScreenView()
	if !strings.Contains(view, "Could not read the issue file") {
		t.Errorf("a failed read renders as an issue with no prose:\n%s", view)
	}
	if strings.Contains(view, "Comments: 2") {
		t.Errorf("a failed read still counts the comments it never got:\n%s", view)
	}
}

// A comment written by an agent can be a fenced diff or a table, and a pane
// that cuts it at the last row shows the reader a comment that ends mid-sentence
// with nothing saying it did. The pane scrolls instead.
func TestALongBodyScrollsInsteadOfBeingCut(t *testing.T) {
	m := proseModel(t)
	var b strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "line %02d of a body that runs well past the bottom of the pane\n", i)
	}
	m.issueProse = map[int64]issueProse{7: {file: issues.File{Body: b.String()}}}
	m.width, m.height = 80, 20

	if top := m.issueFullScreenView(); strings.Contains(top, "line 59") {
		t.Errorf("a body this long was not cut at the pane:\n%s", top)
	}
	m.issueScroll = 60
	if bottom := m.issueFullScreenView(); !strings.Contains(bottom, "line 59") {
		t.Errorf("scrolling to the end did not reach the last line:\n%s", bottom)
	}
}

// The action bar is the only place the keys are, and it sits on the bottom row
// of the screen. A pane that grew past its height would push it off, at every
// terminal size the pane is used at, and at any offset the user can reach.
func TestTheActionBarStaysOnTheBottomRowWhileTheContentScrolls(t *testing.T) {
	m := proseModel(t)
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("a line of prose long enough to wrap when the pane is narrow and the terminal is small\n")
	}
	m.issueProse = map[int64]issueProse{7: {file: issues.File{Body: b.String()}}}

	for _, size := range []struct{ w, h int }{{120, 40}, {100, 30}, {40, 30}, {30, 20}, {20, 12}} {
		for _, offset := range []int{0, 1, 7, 200, 5000} {
			m.width, m.height, m.issueScroll = size.w, size.h, offset

			view := m.View()
			lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
			if len(lines) > m.height {
				t.Errorf("%dx%d at offset %d: rendered %d lines into %d", size.w, size.h, offset, len(lines), m.height)
			}
			last := lines[len(lines)-1]
			if !strings.Contains(last, "[") {
				t.Errorf("%dx%d at offset %d: last line is %q, want the action bar", size.w, size.h, offset, last)
			}
		}
	}
}

// Scrolling is only a way of reaching the rest of the content if the pane says
// there is some. Without the indicator the reader is left to assume, wrongly,
// that the pane holds all there is.
func TestThereIsAnIndicatorWhenThereIsMoreBelow(t *testing.T) {
	m := proseModel(t)
	var b strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "line %02d of a body that runs well past the bottom of the pane\n", i)
	}
	m.issueProse = map[int64]issueProse{7: {file: issues.File{Body: b.String()}}}
	m.width, m.height = 80, 20

	if view := m.issueFullScreenView(); !strings.Contains(view, "▼") {
		t.Errorf("no indicator on a pane with more content below it:\n%s", view)
	}

	// At the end there is nothing below, and a ▼ that outlives its content is
	// as wrong as a missing one.
	m.issueScroll = 5000
	view := m.issueFullScreenView()
	if strings.Contains(view, "▼") {
		t.Errorf("indicator on the last page of the content:\n%s", view)
	}
	if !strings.Contains(view, "line 59") {
		t.Errorf("the last page does not hold the last line:\n%s", view)
	}
}

// An offset belongs to the issue it was set for. Carried onto the next issue it
// opens the pane on rows that are not there, and a reader who scrolled to the
// end of a long review lands in the middle of whatever they open next.
func TestTheScrollOffsetResetsWhenTheSelectedIssueChanges(t *testing.T) {
	m := proseModel(t)
	m.planIssues = append(m.planIssues, issues.Status{
		Issue: issues.Issue{Number: 8, Title: "Another issue", State: issues.StateOpen, Type: "coder"},
	})
	m.issueProse[8] = issueProse{file: issues.File{Body: "A body of its own."}}
	m.screen = screenPlanDetail
	m.issueScroll = 12

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.issueScroll != 0 {
		t.Errorf("scroll offset = %d after moving to another issue, want 0", m.issueScroll)
	}
	if m.issueCursor != 1 {
		t.Fatalf("issueCursor = %d, want 1", m.issueCursor)
	}

	// And opening the full-screen view starts at the top of the issue it shows,
	// even when the cursor never moved to get there.
	m.issueScroll = 9
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.screen != screenIssue {
		t.Fatalf("screen = %v, want the full-screen view", m.screen)
	}
	if m.issueScroll != 0 {
		t.Errorf("scroll offset = %d on opening the full-screen view, want 0", m.issueScroll)
	}
}

// A render happens on every frame. Reaching the store for the prose would put a
// file read behind every frame of every pane, and behind every row of the lists
// that have no prose to show — so a render that has a store and has not run the
// read shows none of it.
func TestARenderNeverReadsTheProseItHasNotBeenGiven(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	issue, err := store.Create(issues.NewIssue{
		Plan: "orders", Type: "coder", Title: "Show the body",
		Body: "A sentence that only a reader of the file would know.",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	m := proseModelWith(t, store)
	m.planIssues = []issues.Status{{
		Issue: issues.Issue{Number: issue.Number, Title: "Show the body", State: issues.StateOpen, Type: "coder"},
	}}
	if view := m.issueFullScreenView(); strings.Contains(view, "only a reader of the file") {
		t.Errorf("the render read the file rather than being handed the prose:\n%s", view)
	}

	// The read is what puts it there.
	msg := loadIssueContent(store, issue.Number)()
	next, _ := m.Update(msg)
	m = next.(Model)
	if view := m.issueFullScreenView(); !strings.Contains(view, "only a reader of the file") {
		t.Errorf("the prose did not arrive after the read:\n%s", view)
	}
}

// The whole journey on a real store: the cursor lands on an issue, the read
// fills the cache, and the pane renders the body and the comments without
// touching the store again.
func TestTheDetailPaneFillsFromWhatTheInterfaceItselfRead(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	issue, err := store.Create(issues.NewIssue{Plan: "orders", Type: "coder", Title: "Show the body", Body: proseBody})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, c := range []struct{ author, body string }{
		{"plan-reviewer", reviewComment},
		{"coder", coderComment},
	} {
		if err := store.Comment(issue.Number, c.author, c.body); err != nil {
			t.Fatalf("Comment: %v", err)
		}
	}

	m := proseModelWith(t, store)
	m.planIssues = []issues.Status{{
		Issue: issues.Issue{Number: issue.Number, Title: "Show the body", State: issues.StateOpen, Type: "coder"},
	}}

	msg := loadIssueContent(store, issue.Number)()
	loaded, ok := msg.(issueContentLoadedMsg)
	if !ok {
		t.Fatalf("loadIssueContent returned %T", msg)
	}
	if loaded.err != nil {
		t.Fatalf("loadIssueContent: %v", loaded.err)
	}
	if !strings.Contains(loaded.file.Body, "while changing an issue is still free") {
		t.Errorf("the read did not carry the body: %q", loaded.file.Body)
	}
	if len(loaded.file.Comments) != 2 {
		t.Fatalf("the read carried %d comments, want 2", len(loaded.file.Comments))
	}
	if loaded.file.Comments[0].Author != "plan-reviewer" || loaded.file.Comments[1].Author != "coder" {
		t.Errorf("the read did not keep the file's order: %+v", loaded.file.Comments)
	}

	next, _ := m.Update(loaded)
	m = next.(Model)
	view := m.issueFullScreenView()
	for _, want := range []string{
		"while changing an issue is still free",
		"plan-reviewer", "Fourteen issues checked",
		"coder", "the action bar stays put",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("full-screen view missing %q:\n%s", want, view)
		}
	}
	// The times the store writes are the current time, so the pane's form is
	// what can be asserted, not the value.
	if !strings.Contains(view, "plan-reviewer · 20") {
		t.Errorf("comment byline is not in the pane's timestamp form:\n%s", view)
	}
}

// One read per issue the cursor lands on. Holding the down arrow through a
// plan would otherwise re-read every issue on the way past, and re-entering the
// full-screen view would re-read the issue already on screen.
func TestContentIsReadOncePerSelectedIssue(t *testing.T) {
	store := testStore(t)
	if _, err := store.CreatePlan(issues.NewPlan{Slug: "orders", Title: "Orders"}); err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	first, err := store.Create(issues.NewIssue{Plan: "orders", Type: "coder", Title: "First", Body: "first body"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := store.Create(issues.NewIssue{Plan: "orders", Type: "coder", Title: "Second", Body: "second body"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	m := plansModel(t, []issues.Plan{{Slug: "orders", Title: "Orders"}})
	m.screen, m.store = screenPlanDetail, store
	m.planIssuesLoadedFor = "orders"
	m.planIssues = []issues.Status{
		{Issue: issues.Issue{Number: first.Number, Title: "First", State: issues.StateOpen, Type: "coder"}},
		{Issue: issues.Issue{Number: second.Number, Title: "Second", State: issues.StateOpen, Type: "coder"}},
	}

	// The cursor lands on each issue in turn, and each landing reads the file
	// behind it exactly once.
	if m.selectIssueContent() == nil {
		t.Fatal("landing on an issue with no prose held armed no read")
	}
	m = navigate(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.issueCursor != 1 {
		t.Fatalf("issueCursor = %d, want 1", m.issueCursor)
	}
	if _, held := m.issueProse[second.Number]; !held {
		t.Fatalf("landing on #%d did not read it: %+v", second.Number, m.issueProse)
	}
	m = navigate(t, m, tea.KeyMsg{Type: tea.KeyDown}) // already at the end
	m = navigate(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if _, held := m.issueProse[first.Number]; !held {
		t.Fatalf("landing on #%d did not read it: %+v", first.Number, m.issueProse)
	}
	if len(m.issueProse) != 2 {
		t.Errorf("the cache holds %d issues, want the 2 the cursor landed on", len(m.issueProse))
	}

	// Both files are held now, so moving back onto either and opening the
	// full-screen view arm nothing further.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Error("moving onto an issue whose prose is held armed another read")
	}
	next, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("opening the full-screen view of an issue whose prose is held armed another read")
	}
	m = next.(Model)
	if m.screen != screenIssue {
		t.Fatalf("screen = %v, want the full-screen view", m.screen)
	}
	if view := m.issueFullScreenView(); !strings.Contains(view, "second body") {
		t.Errorf("the full-screen view is missing the prose it already held:\n%s", view)
	}
}

// navigate sends a key and, if it armed a read, delivers the message that read
// produced — the two halves of what the bubbletea runtime does on its own.
func navigate(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, cmd := m.Update(msg)
	m = next.(Model)
	if cmd == nil {
		return m
	}
	next, _ = m.Update(cmd())
	return next.(Model)
}

// The preview pane shares its rows with the issue list, and half a width cannot
// hold a fenced diff. It says how much there is to read and leaves the reading
// to the full-screen view.
func TestThePreviewPaneCountsTheCommentsWithoutTheirText(t *testing.T) {
	m := proseModel(t)
	m.screen = screenPlanDetail

	preview := m.issuePreviewPane(45, 20)
	if !strings.Contains(preview, "Comments: 2") {
		t.Errorf("preview pane does not say there are comments to read:\n%s", preview)
	}
	for _, unwanted := range []string{"plan-reviewer", "Fourteen issues checked", "the action bar stays put"} {
		if strings.Contains(preview, unwanted) {
			t.Errorf("preview pane spent its rows on %q", unwanted)
		}
	}
}

// The preview's row count is the thing the list beside it lines up with, so a
// body that wraps must not push it past the height it was given — at any size
// the plan detail view is used at.
func TestThePreviewPaneStillFitsTheSizesItsTestsCover(t *testing.T) {
	m := proseModel(t)
	m.screen = screenPlanDetail

	for _, size := range []struct{ w, h int }{{120, 40}, {100, 30}, {45, 20}, {40, 30}, {30, 20}, {20, 12}} {
		preview := m.issuePreviewPane(size.w, size.h)
		if got := len(strings.Split(preview, "\n")); got != size.h {
			t.Errorf("preview rendered %d lines into %d at %dx%d", got, size.h, size.w, size.h)
		}
	}
}

// Scrolling takes the cursor keys in the full-screen view only. Down there they
// still move between issues, and the action keys still start things: a scroll
// that swallowed them would make the view read-only.
func TestScrollingTakesTheCursorKeysOnlyInTheFullScreenView(t *testing.T) {
	m := proseModel(t)
	m.planIssues = append(m.planIssues, issues.Status{
		Issue: issues.Issue{Number: 8, Title: "Another issue", State: issues.StateOpen, Type: "coder"},
	})
	m.issueProse[8] = issueProse{file: issues.File{Body: "A body of its own."}}
	m.issueScroll = 5

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.issueCursor != 0 {
		t.Errorf("the down arrow moved the issue cursor in the full-screen view, to %d", m.issueCursor)
	}
	if m.issueScroll != 6 {
		t.Errorf("issueScroll = %d after the down arrow, want 6", m.issueScroll)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("5")})
	m = next.(Model)
	if m.issueScroll != 6 {
		t.Errorf("an action key moved the scroll offset to %d", m.issueScroll)
	}
	for i := 0; i < 100; i++ {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
		m = next.(Model)
	}
	if m.issueScroll != 0 {
		t.Errorf("issueScroll = %d after paging to the top, want 0", m.issueScroll)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(Model)
	if m.issueScroll != m.contentHeight() {
		t.Errorf("issueScroll = %d after a page down, want a page (%d)", m.issueScroll, m.contentHeight())
	}
}

// A pane one row short of a header and a row is the degenerate case the plan
// views already have to survive. Scrolling must not make it overflow.
func TestAScrollingPaneFitsEvenWhenTheTerminalIsAlmostTooShort(t *testing.T) {
	m := proseModel(t)
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("a line of prose\n")
	}
	m.issueProse = map[int64]issueProse{7: {file: issues.File{Body: b.String()}}}

	for h := 1; h <= 8; h++ {
		m.width, m.height, m.issueScroll = 20, h+3, 12
		rows := strings.Split(m.issueFullScreenView(), "\n")
		if len(rows) != h {
			t.Errorf("content height %d: rendered %d lines into %d", h, len(rows), h)
		}
		for i, row := range rows {
			if got := utf8.RuneCountInString(row); got > 20 {
				t.Errorf("content height %d: row %d is %d wide, want 20", h, i, got)
			}
		}
	}
}
