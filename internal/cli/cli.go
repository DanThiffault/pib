// Package cli is pib's command line: everything you can ask a running pib
// about plans and issues without going through its interface.
//
// Commands are clients. They find the socket the pib for this repository is
// listening on, send one request, and print the reply — the running pib
// stays the only writer, so parallel agents need no locking of their own.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"pib/internal/issueops"
	"pib/internal/protocol"
	"pib/internal/server"
	"pib/internal/workspace"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// App is one invocation of the command line.
type App struct {
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Socket overrides discovery. Empty means PIB_SOCKET, then the socket
	// recorded in the repository's workspace.
	Socket string
}

// Run carries out the command and reports the process exit code.
func (a App) Run() int {
	if len(a.Args) == 0 {
		a.usage()
		return exitUsage
	}

	group, rest := a.Args[0], a.Args[1:]
	switch group {
	case "help", "-h", "--help":
		a.usage()
		return exitOK
	case "plan":
		return a.dispatch(rest, map[string]command{
			"apply": a.planApply,
			"list":  a.planList,
			"show":  a.planShow,
		}, "plan")
	case "issue":
		return a.dispatch(rest, map[string]command{
			"create":  a.issueCreate,
			"list":    a.issueList,
			"ready":   a.issueReady,
			"view":    a.issueView,
			"edit":    a.issueEdit,
			"comment": a.issueComment,
			"link-pr": a.issueLinkPR,
			"close":   a.issueClose,
			"reindex": a.issueReindex,
		}, "issue")
	default:
		fmt.Fprintf(a.Stderr, "pib: unknown command %q\n\n", group)
		a.usage()
		return exitUsage
	}
}

// command carries out one subcommand.
type command func(args []string) error

// dispatch picks a subcommand and turns its error into an exit code.
func (a App) dispatch(args []string, commands map[string]command, group string) int {
	if len(args) == 0 {
		fmt.Fprintf(a.Stderr, "pib %s: expected a subcommand (%s)\n", group, names(commands))
		return exitUsage
	}

	run, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(a.Stderr, "pib %s: unknown subcommand %q (expected %s)\n", group, args[0], names(commands))
		return exitUsage
	}

	switch err := run(args[1:]); {
	case err == nil:
		return exitOK
	case errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.Is(err, errUsage):
		fmt.Fprintf(a.Stderr, "pib %s %s: %v\n", group, args[0], err)
		return exitUsage
	default:
		fmt.Fprintf(a.Stderr, "pib: %v\n", err)
		return exitError
	}
}

// errUsage marks a mistake in how the command was called rather than a
// failure carrying it out.
var errUsage = errors.New("usage")

func usagef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errUsage, fmt.Sprintf(format, args...))
}

// ── plan ──

func (a App) planApply(args []string) error {
	fs, asJSON := a.flags("plan apply")
	positional, err := parse(fs, args, 1, "<file.json>, or - for standard input")
	if err != nil {
		return err
	}

	document, err := a.read(positional[0])
	if err != nil {
		return err
	}
	if !json.Valid(document) {
		return fmt.Errorf("%s is not valid json", positional[0])
	}

	resp, err := a.send(protocol.Request{Op: protocol.OpPlanApply, Payload: document})
	if err != nil {
		return err
	}
	return a.renderApply(resp, *asJSON)
}

func (a App) planList(args []string) error {
	fs, asJSON := a.flags("plan list")
	if _, err := parse(fs, args, 0, ""); err != nil {
		return err
	}

	resp, err := a.send(protocol.Request{Op: protocol.OpPlanList})
	if err != nil {
		return err
	}
	return a.renderPlanList(resp, *asJSON)
}

func (a App) planShow(args []string) error {
	fs, asJSON := a.flags("plan show")
	positional, err := parse(fs, args, 1, "<slug>")
	if err != nil {
		return err
	}

	resp, err := a.send(request(protocol.OpPlanShow, issueops.PlanShowParams{Slug: positional[0]}))
	if err != nil {
		return err
	}
	return a.renderPlanDetail(resp, *asJSON)
}

// ── issue ──

func (a App) issueCreate(args []string) error {
	fs, asJSON := a.flags("issue create")
	var (
		plan       = fs.String("plan", "", "plan the issue belongs to")
		localID    = fs.String("id", "", "id this issue is known by inside its plan")
		issueType  = fs.String("type", "", "issue type, e.g. task")
		title      = fs.String("title", "", "issue title")
		body       = fs.String("body", "", "issue body")
		bodyFile   = fs.String("body-file", "", "read the body from a file, or - for standard input")
		parent     = fs.Int64("parent", 0, "issue this one belongs to")
		acceptance stringList
		blockedBy  int64List
	)
	fs.Var(&acceptance, "acceptance", "an acceptance criterion; repeat for more")
	fs.Var(&blockedBy, "blocked-by", "issues this one waits on, comma separated")

	if _, err := parse(fs, args, 0, ""); err != nil {
		return err
	}
	if *plan == "" || *title == "" || *issueType == "" {
		return usagef("--plan, --type and --title are required")
	}

	text, err := a.body(*body, *bodyFile)
	if err != nil {
		return err
	}

	resp, err := a.send(request(protocol.OpIssueCreate, issueops.CreateParams{
		Plan: *plan, LocalID: *localID, Type: *issueType, Title: *title,
		Body: text, Acceptance: acceptance, Parent: *parent, BlockedBy: blockedBy,
	}))
	if err != nil {
		return err
	}
	return a.renderDetail(resp, *asJSON)
}

func (a App) issueList(args []string) error {
	fs, asJSON := a.flags("issue list")
	var (
		plan      = fs.String("plan", "", "only this plan")
		state     = fs.String("state", "", "open or closed")
		issueType = fs.String("type", "", "only this type")
		readyOnly = fs.Bool("ready", false, "only issues that could start now")
	)
	if _, err := parse(fs, args, 0, ""); err != nil {
		return err
	}

	op := protocol.OpIssueList
	if *readyOnly {
		op = protocol.OpIssueReady
	}

	resp, err := a.send(request(op, issueops.ListParams{Plan: *plan, State: *state, Type: *issueType}))
	if err != nil {
		return err
	}
	return a.renderList(resp, *asJSON)
}

func (a App) issueReady(args []string) error {
	fs, asJSON := a.flags("issue ready")
	plan := fs.String("plan", "", "only this plan")
	if _, err := parse(fs, args, 0, ""); err != nil {
		return err
	}

	resp, err := a.send(request(protocol.OpIssueReady, issueops.ListParams{Plan: *plan}))
	if err != nil {
		return err
	}
	return a.renderList(resp, *asJSON)
}

func (a App) issueView(args []string) error {
	fs, asJSON := a.flags("issue view")
	positional, err := parse(fs, args, 1, "<number>")
	if err != nil {
		return err
	}
	number, err := numberOf(positional[0])
	if err != nil {
		return err
	}

	resp, err := a.send(request(protocol.OpIssueView, issueops.ViewParams{Number: number}))
	if err != nil {
		return err
	}
	return a.renderDetail(resp, *asJSON)
}

func (a App) issueEdit(args []string) error {
	fs, asJSON := a.flags("issue edit")
	var (
		title     = fs.String("title", "", "new title")
		issueType = fs.String("type", "", "new type")
		body      = fs.String("body", "", "new body")
		bodyFile  = fs.String("body-file", "", "read the new body from a file, or - for standard input")
		parent    = fs.Int64("parent", 0, "new parent, or 0 for none")

		acceptance stringList
		add        int64List
		remove     int64List
	)
	fs.Var(&acceptance, "acceptance", "replace the acceptance criteria; repeat for more")
	fs.Var(&add, "add-blocked-by", "issues to wait on, comma separated")
	fs.Var(&remove, "remove-blocked-by", "issues to stop waiting on, comma separated")

	positional, err := parse(fs, args, 1, "<number>")
	if err != nil {
		return err
	}
	number, err := numberOf(positional[0])
	if err != nil {
		return err
	}

	params := issueops.EditParams{Number: number, AddBlockedBy: add, RemoveBlockedBy: remove}
	set := given(fs)
	if set["title"] {
		params.Title = title
	}
	if set["type"] {
		params.Type = issueType
	}
	if set["parent"] {
		params.Parent = parent
	}
	if set["acceptance"] {
		criteria := []string(acceptance)
		params.Acceptance = &criteria
	}
	if set["body"] || set["body-file"] {
		text, err := a.body(*body, *bodyFile)
		if err != nil {
			return err
		}
		params.Body = &text
	}

	if params.Title == nil && params.Type == nil && params.Parent == nil &&
		params.Acceptance == nil && params.Body == nil &&
		len(add) == 0 && len(remove) == 0 {
		return usagef("nothing to change")
	}

	resp, err := a.send(request(protocol.OpIssueEdit, params))
	if err != nil {
		return err
	}
	return a.renderDetail(resp, *asJSON)
}

func (a App) issueComment(args []string) error {
	fs, asJSON := a.flags("issue comment")
	var (
		body     = fs.String("body", "", "the comment")
		bodyFile = fs.String("body-file", "", "read the comment from a file, or - for standard input")
		author   = fs.String("author", "", "who is commenting; defaults to the agent, or you")
	)

	positional, err := parse(fs, args, 1, "<number>")
	if err != nil {
		return err
	}
	number, err := numberOf(positional[0])
	if err != nil {
		return err
	}

	text, err := a.body(*body, *bodyFile)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return usagef("--body or --body-file is required")
	}

	resp, err := a.send(request(protocol.OpIssueComment, issueops.CommentParams{
		Number: number, Author: whoAmI(*author), Body: text,
	}))
	if err != nil {
		return err
	}
	return a.renderDetail(resp, *asJSON)
}

func (a App) issueLinkPR(args []string) error {
	fs, asJSON := a.flags("issue link-pr")
	positional, err := parse(fs, args, 2, "<number> <pull request url>")
	if err != nil {
		return err
	}
	number, err := numberOf(positional[0])
	if err != nil {
		return err
	}

	resp, err := a.send(request(protocol.OpIssueLinkPR, issueops.LinkPRParams{
		Number: number, URL: positional[1],
	}))
	if err != nil {
		return err
	}
	return a.renderDetail(resp, *asJSON)
}

func (a App) issueClose(args []string) error {
	fs, asJSON := a.flags("issue close")
	reason := fs.String("reason", "", "recorded as a comment")

	positional, err := parse(fs, args, 1, "<number>")
	if err != nil {
		return err
	}
	number, err := numberOf(positional[0])
	if err != nil {
		return err
	}

	resp, err := a.send(request(protocol.OpIssueClose, issueops.CloseParams{
		Number: number, Reason: *reason,
	}))
	if err != nil {
		return err
	}
	return a.renderClose(resp, *asJSON)
}

func (a App) issueReindex(args []string) error {
	fs, asJSON := a.flags("issue reindex")
	plan := fs.String("plan", "", "only this plan")
	if _, err := parse(fs, args, 0, ""); err != nil {
		return err
	}

	resp, err := a.send(request(protocol.OpIssueReindex, issueops.ReindexParams{Plan: *plan}))
	if err != nil {
		return err
	}
	return a.renderReindex(resp, *asJSON)
}

// ── plumbing ──

// flags builds a flag set that every command shares the shape of.
func (a App) flags(name string) (*flag.FlagSet, *bool) {
	fs := flag.NewFlagSet("pib "+name, flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	return fs, fs.Bool("json", false, "print the reply as json")
}

// parse reads flags and positional arguments in either order, so both
// "view 7 --json" and "view --json 7" work.
func parse(fs *flag.FlagSet, args []string, want int, shape string) ([]string, error) {
	var positional []string
	for len(args) > 0 && len(positional) < want && isPositional(args[0]) {
		positional = append(positional, args[0])
		args = args[1:]
	}

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	positional = append(positional, fs.Args()...)

	if len(positional) < want {
		return nil, usagef("expected %s", shape)
	}
	if len(positional) > want {
		return nil, usagef("unexpected argument %q", positional[want])
	}
	return positional, nil
}

// isPositional reports an argument that is not a flag. A lone dash is the
// conventional name for standard input, not a flag.
func isPositional(arg string) bool {
	return arg == "-" || !strings.HasPrefix(arg, "-")
}

// given reports which flags were actually passed, so an edit can tell "set
// this to empty" from "leave it alone".
func given(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// request builds a request with a marshalled payload.
func request(op protocol.Op, params any) protocol.Request {
	body, err := json.Marshal(params)
	if err != nil {
		// The parameter types are all plain structs; this cannot fail.
		panic(err)
	}
	return protocol.Request{Op: op, Payload: body}
}

// send asks the running pib and returns its reply.
func (a App) send(req protocol.Request) (protocol.Response, error) {
	socket, err := a.socket()
	if err != nil {
		return protocol.Response{}, err
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		return protocol.Response{}, fmt.Errorf(
			"pib is not running (no listener at %s). Start pib in this repository and try again", socket)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return protocol.Response{}, err
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Minute))

	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return protocol.Response{}, fmt.Errorf("pib closed the connection: %w", err)
	}
	if resp.Error != "" {
		return protocol.Response{}, errors.New(resp.Error)
	}
	return resp, nil
}

// socket finds the pib to talk to: an explicit override, the environment the
// extension already uses, or the repository's own workspace.
func (a App) socket() (string, error) {
	if a.Socket != "" {
		return a.Socket, nil
	}
	if fromEnv := os.Getenv("PIB_SOCKET"); fromEnv != "" {
		return fromEnv, nil
	}

	ws, err := workspace.Detect()
	if err != nil {
		return "", err
	}
	if socket, err := server.Discover(ws.Dir); err == nil && socket != "" {
		return socket, nil
	}
	return server.Path(ws.Dir), nil
}

// read loads a document from a file, or standard input for "-".
func (a App) read(path string) ([]byte, error) {
	if path == "-" {
		if a.Stdin == nil {
			return nil, errors.New("nothing on standard input")
		}
		return io.ReadAll(a.Stdin)
	}
	return os.ReadFile(path)
}

// body resolves the two ways of giving prose.
func (a App) body(inline, path string) (string, error) {
	if inline != "" && path != "" {
		return "", usagef("give --body or --body-file, not both")
	}
	if path == "" {
		return inline, nil
	}
	text, err := a.read(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(text), "\n"), nil
}

// whoAmI names the author of a comment. Inside an agent pib started, that is
// the agent; otherwise it is whoever is at the keyboard.
func whoAmI(explicit string) string {
	for _, candidate := range []string{explicit, os.Getenv("PIB_AGENT"), os.Getenv("USER")} {
		if candidate != "" {
			return candidate
		}
	}
	return "human"
}

func numberOf(arg string) (int64, error) {
	number, err := strconv.ParseInt(strings.TrimPrefix(arg, "#"), 10, 64)
	if err != nil || number < 1 {
		return 0, usagef("%q is not an issue number", arg)
	}
	return number, nil
}

// stringList is a repeatable string flag.
type stringList []string

func (l stringList) String() string      { return strings.Join(l, ", ") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// int64List is a repeatable, comma separated list of issue numbers.
type int64List []int64

func (l int64List) String() string {
	parts := make([]string, 0, len(l))
	for _, n := range l {
		parts = append(parts, strconv.FormatInt(n, 10))
	}
	return strings.Join(parts, ",")
}

func (l *int64List) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		number, err := numberOf(part)
		if err != nil {
			return err
		}
		*l = append(*l, number)
	}
	return nil
}

func names(commands map[string]command) string {
	list := make([]string, 0, len(commands))
	for name := range commands {
		list = append(list, name)
	}
	sortStrings(list)
	return strings.Join(list, ", ")
}

func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func (a App) usage() {
	fmt.Fprint(a.Stderr, `pib — a planning front-end for pi

Run pib with no arguments to plan something. The commands below talk to a pib
already running in this repository.

  pib plan apply <file.json>     apply a plan document, creating its issues
  pib plan list                  every plan pib knows
  pib plan show <slug>           a plan and the issues in it

  pib issue create --plan <slug> --type <type> --title <title>
  pib issue list [--plan <slug>] [--state open|closed] [--type <type>] [--ready]
  pib issue ready [--plan <slug>]
  pib issue view <number>
  pib issue edit <number> [--title …] [--add-blocked-by 2,3] […]
  pib issue comment <number> --body <text>
  pib issue link-pr <number> <url>
  pib issue close <number> [--reason <text>]
  pib issue reindex [--plan <slug>]

Every command takes --json to print the reply as json instead of text.
`)
}
