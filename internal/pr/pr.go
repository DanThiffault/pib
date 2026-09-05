// Package pr reports the state of a pull request on GitHub.
//
// pib owns issues; GitHub still owns pull requests. This is the one place
// pib looks across at them, so that a merge can close the issue its coder
// opened the pull request for.
package pr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// The states pib records for a linked pull request.
const (
	Open   = "open"
	Merged = "merged"
	Closed = "closed"
)

// ErrUnavailable reports that gh could not be run at all — usually because
// it is not installed. Issue tracking carries on without it; only automatic
// closure pauses.
var ErrUnavailable = errors.New("gh is not available")

// Lookup reports the state of one pull request. The store depends on this
// shape, not on this package, so tests can supply their own.
type Lookup interface {
	State(ctx context.Context, url string) (string, error)
}

// Comment is one entry in a review thread.
type Comment struct {
	Author string
	Body   string
	ID     int64 // databaseId from GitHub
}

// Thread is one review thread on a pull request, with the root comment and
// every reply beneath it.
type Thread struct {
	ID         string
	IsResolved bool
	Path       string
	Line       int
	Comments   []Comment
}

// OutOfScope is a pib:out-of-scope marker parsed from a comment body.
type OutOfScope struct {
	Plan string
	ID   string
	Body string
}

// Settled reports whether any comment in the thread carries a pib:filed
// marker, which means the finding has already been filed as an issue.
func (t Thread) Settled() bool {
	for _, c := range t.Comments {
		if filedRe.MatchString(c.Body) {
			return true
		}
	}
	return false
}

// OutOfScope parses the first well-formed pib:out-of-scope marker in the
// thread. It returns nil if none is found or if the marker is malformed.
func (t Thread) OutOfScope() *OutOfScope {
	for _, c := range t.Comments {
		if oos := parseOutOfScope(c.Body); oos != nil {
			return oos
		}
	}
	return nil
}

var (
	outOfScopeRe = regexp.MustCompile(`(?s)<!--\s*pib:out-of-scope\s+plan=([a-zA-Z0-9_-]+)\s+id=([a-zA-Z0-9_-]+)\s*-->`)
	filedRe      = regexp.MustCompile(`(?s)<!--\s*pib:filed(?:\s+#?\d+)?\s*-->`)
	slugRe       = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

func validSlug(s string) bool {
	return slugRe.MatchString(s)
}

func parseOutOfScope(body string) *OutOfScope {
	loc := outOfScopeRe.FindStringSubmatchIndex(body)
	if loc == nil {
		return nil
	}
	plan := body[loc[2]:loc[3]]
	id := body[loc[4]:loc[5]]
	if !validSlug(plan) || !validSlug(id) {
		return nil
	}
	clean := body[:loc[0]] + body[loc[1]:]
	clean = strings.TrimSpace(clean)
	return &OutOfScope{Plan: plan, ID: id, Body: clean}
}

// CLI asks the gh command line tool.
type CLI struct {
	// Path is the executable to run. Empty means "gh" on PATH.
	Path string
}

// run executes gh with the given arguments, returning the stdout bytes.
// It translates a missing executable into ErrUnavailable the way every
// caller in this package must.
func (c CLI) run(ctx context.Context, args ...string) ([]byte, error) {
	path := c.Path
	if path == "" {
		path = "gh"
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// A bare name that is not on PATH gives ErrNotFound; an explicit
		// path that is not there gives ENOENT. Both mean the same thing.
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrUnavailable, path)
		}
		if message := firstLine(stderr.String()); message != "" {
			return nil, fmt.Errorf("gh %s: %s", strings.Join(args, " "), message)
		}
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}

	return stdout.Bytes(), nil
}

// State runs gh and maps its answer onto the states pib stores.
func (c CLI) State(ctx context.Context, url string) (string, error) {
	if strings.TrimSpace(url) == "" {
		return "", errors.New("no pull request url")
	}

	out, err := c.run(ctx, "pr", "view", url, "--json", "state")
	if err != nil {
		return "", err
	}

	var outStruct struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &outStruct); err != nil {
		return "", fmt.Errorf("gh pr view %s: unreadable reply: %w", url, err)
	}

	switch strings.ToUpper(strings.TrimSpace(outStruct.State)) {
	case "MERGED":
		return Merged, nil
	case "CLOSED":
		return Closed, nil
	case "OPEN":
		return Open, nil
	default:
		return "", fmt.Errorf("gh pr view %s: unknown state %q", url, outStruct.State)
	}
}

// Threads returns every review thread on a pull request, with the root
// comment and each reply nested beneath it. Markers are left in the bodies
// for the caller; Thread.Settled and Thread.OutOfScope parse them.
func (c CLI) Threads(ctx context.Context, url string) ([]Thread, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("no pull request url")
	}

	owner, repo, number, err := parsePRURL(url)
	if err != nil {
		return nil, err
	}

	const query = `query($owner:String!, $repo:String!, $number:Int!) {
  repository(owner:$owner, name:$repo) {
    pullRequest(number:$number) {
      reviewThreads(first: 100) {
        nodes {
          id
          isResolved
          path
          line
          comments(first: 100) {
            nodes {
              databaseId
              author { login }
              body
            }
          }
        }
      }
    }
  }
}`

	out, err := c.run(ctx, "api", "graphql",
		"-f", "owner="+owner,
		"-f", "repo="+repo,
		"-F", fmt.Sprintf("number=%d", number),
		"-f", "query="+query,
	)
	if err != nil {
		return nil, err
	}

	type response struct {
		Data struct {
			Repository *struct {
				PullRequest *struct {
					ReviewThreads struct {
						Nodes []struct {
							ID         string `json:"id"`
							IsResolved bool   `json:"isResolved"`
							Path       string `json:"path"`
							Line       int    `json:"line"`
							Comments   struct {
								Nodes []struct {
									DatabaseID int64 `json:"databaseId"`
									Author     struct {
										Login string `json:"login"`
									} `json:"author"`
									Body string `json:"body"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	var resp response
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("gh api graphql %s: unreadable reply: %w", url, err)
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("gh api graphql %s: %s", url, resp.Errors[0].Message)
	}
	if resp.Data.Repository == nil || resp.Data.Repository.PullRequest == nil {
		return nil, fmt.Errorf("gh api graphql %s: pull request not found", url)
	}

	var threads []Thread
	for _, node := range resp.Data.Repository.PullRequest.ReviewThreads.Nodes {
		var comments []Comment
		for _, c := range node.Comments.Nodes {
			comments = append(comments, Comment{
				Author: c.Author.Login,
				Body:   c.Body,
				ID:     c.DatabaseID,
			})
		}
		threads = append(threads, Thread{
			ID:         node.ID,
			IsResolved: node.IsResolved,
			Path:       node.Path,
			Line:       node.Line,
			Comments:   comments,
		})
	}
	return threads, nil
}

// parsePRURL extracts owner, repo and PR number from a GitHub pull request
// URL such as https://github.com/owner/repo/pull/123.
func parsePRURL(raw string) (owner, repo string, number int, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", 0, fmt.Errorf("not a pull request url: %s", raw)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[len(parts)-2] != "pull" {
		return "", "", 0, fmt.Errorf("not a pull request url: %s", raw)
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return "", "", 0, fmt.Errorf("not a pull request url: %s", raw)
	}
	return parts[len(parts)-4], parts[len(parts)-3], n, nil
}

// firstLine trims gh's error output to something worth putting in a warning.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
