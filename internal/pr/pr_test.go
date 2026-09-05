package pr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGH writes a stand-in for the gh executable and returns its path.
func fakeGH(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStateMapsGitHubsVocabulary(t *testing.T) {
	cases := map[string]string{
		"OPEN":   Open,
		"MERGED": Merged,
		"CLOSED": Closed,
	}

	for reply, want := range cases {
		cli := CLI{Path: fakeGH(t, `echo '{"state":"`+reply+`"}'`)}
		got, err := cli.State(context.Background(), "https://github.com/o/r/pull/1")
		if err != nil {
			t.Fatalf("%s: %v", reply, err)
		}
		if got != want {
			t.Errorf("%s mapped to %q, want %q", reply, got, want)
		}
	}
}

func TestStatePassesTheUrlToGH(t *testing.T) {
	cli := CLI{Path: fakeGH(t, `echo "{\"state\":\"OPEN\",\"args\":\"$*\"}" >&2; echo '{"state":"OPEN"}'`)}
	if _, err := cli.State(context.Background(), "https://github.com/o/r/pull/7"); err != nil {
		t.Fatalf("State: %v", err)
	}
}

func TestMissingGHIsUnavailableRatherThanAFailure(t *testing.T) {
	cli := CLI{Path: filepath.Join(t.TempDir(), "definitely-not-here")}

	_, err := cli.State(context.Background(), "https://github.com/o/r/pull/1")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestGHFailureReportsItsFirstLine(t *testing.T) {
	cli := CLI{Path: fakeGH(t, `echo "" >&2; echo "could not resolve to a PullRequest" >&2; exit 1`)}

	_, err := cli.State(context.Background(), "https://github.com/o/r/pull/1")
	if err == nil || !strings.Contains(err.Error(), "could not resolve to a PullRequest") {
		t.Errorf("err = %v, want gh's own message", err)
	}
}

func TestUnreadableOrUnknownReplies(t *testing.T) {
	if _, err := (CLI{Path: fakeGH(t, `echo 'not json'`)}).State(context.Background(), "u"); err == nil {
		t.Error("unreadable json was accepted")
	}
	if _, err := (CLI{Path: fakeGH(t, `echo '{"state":"DRAFTED"}'`)}).State(context.Background(), "u"); err == nil {
		t.Error("an unknown state was accepted")
	}
	if _, err := (CLI{}).State(context.Background(), "  "); err == nil {
		t.Error("an empty url was accepted")
	}
}

func TestContextCancellationStopsTheLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cli := CLI{Path: fakeGH(t, `echo '{"state":"OPEN"}'`)}
	if _, err := cli.State(ctx, "https://github.com/o/r/pull/1"); err == nil {
		t.Error("a cancelled lookup succeeded")
	}
}

func TestThreadsReturnsReviewThreadsWithReplies(t *testing.T) {
	json := `{
  "data": {
    "repository": {
      "pullRequest": {
        "reviewThreads": {
          "nodes": [
            {
              "id": "PRRT_abc",
              "isResolved": false,
              "path": "foo.go",
              "line": 3,
              "comments": {
                "nodes": [
                  {"databaseId": 1, "author": {"login": "alice"}, "body": "root comment"},
                  {"databaseId": 2, "author": {"login": "bob"}, "body": "reply one"}
                ]
              }
            },
            {
              "id": "PRRT_def",
              "isResolved": true,
              "path": "bar.go",
              "line": 7,
              "comments": {
                "nodes": [
                  {"databaseId": 3, "author": {"login": "carol"}, "body": "another root"}
                ]
              }
            }
          ]
        }
      }
    }
  }
}`
	cli := CLI{Path: fakeGH(t, "cat <<'EOF'\n"+json+"\nEOF")}
	threads, err := cli.Threads(context.Background(), "https://github.com/o/r/pull/1")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("got %d threads, want 2", len(threads))
	}
	if threads[0].ID != "PRRT_abc" || threads[0].IsResolved || threads[0].Path != "foo.go" || threads[0].Line != 3 {
		t.Errorf("first thread = %+v, unexpected", threads[0])
	}
	if len(threads[0].Comments) != 2 {
		t.Fatalf("first thread has %d comments, want 2", len(threads[0].Comments))
	}
	if threads[0].Comments[0].ID != 1 || threads[0].Comments[0].Author != "alice" {
		t.Errorf("first comment = %+v", threads[0].Comments[0])
	}
	if threads[0].Comments[1].Body != "reply one" {
		t.Errorf("second comment body = %q, want reply one", threads[0].Comments[1].Body)
	}
	if !threads[1].IsResolved || threads[1].ID != "PRRT_def" {
		t.Errorf("second thread = %+v, unexpected", threads[1])
	}
}

func TestThreadsReturnsEmptyWhenNoThreads(t *testing.T) {
	json := `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`
	cli := CLI{Path: fakeGH(t, "cat <<'EOF'\n"+json+"\nEOF")}
	threads, err := cli.Threads(context.Background(), "https://github.com/o/r/pull/1")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(threads) != 0 {
		t.Errorf("got %d threads, want 0", len(threads))
	}
}

func TestThreadsUnavailableWhenGHMissing(t *testing.T) {
	cli := CLI{Path: filepath.Join(t.TempDir(), "nowhere")}
	_, err := cli.Threads(context.Background(), "https://github.com/o/r/pull/1")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestThreadsReportsGHError(t *testing.T) {
	cli := CLI{Path: fakeGH(t, `echo "" >&2; echo "bad request" >&2; exit 1`)}
	_, err := cli.Threads(context.Background(), "https://github.com/o/r/pull/1")
	if err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Errorf("err = %v, want gh error message", err)
	}
}

func TestThreadsRejectsBadJSON(t *testing.T) {
	cli := CLI{Path: fakeGH(t, `echo 'not json'`)}
	_, err := cli.Threads(context.Background(), "https://github.com/o/r/pull/1")
	if err == nil {
		t.Error("bad json was accepted")
	}
}

func TestThreadsRejectsGraphQLError(t *testing.T) {
	json := `{"errors":[{"message":"Could not resolve to a PullRequest"}]}`
	cli := CLI{Path: fakeGH(t, "cat <<'EOF'\n"+json+"\nEOF")}
	_, err := cli.Threads(context.Background(), "https://github.com/o/r/pull/1")
	if err == nil || !strings.Contains(err.Error(), "Could not resolve") {
		t.Errorf("err = %v, want GraphQL error", err)
	}
}

func TestThreadsRejectsMissingPullRequest(t *testing.T) {
	json := `{"data":{"repository":null}}`
	cli := CLI{Path: fakeGH(t, "cat <<'EOF'\n"+json+"\nEOF")}
	_, err := cli.Threads(context.Background(), "https://github.com/o/r/pull/1")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not found", err)
	}
}

func TestThreadsRejectsEmptyURL(t *testing.T) {
	cli := CLI{Path: fakeGH(t, `echo '{}'`)} // should not be called
	_, err := cli.Threads(context.Background(), "  ")
	if err == nil {
		t.Error("empty url was accepted")
	}
}

func TestThreadsRejectsMalformedURL(t *testing.T) {
	cli := CLI{Path: fakeGH(t, `echo '{}'`)} // should not be called
	_, err := cli.Threads(context.Background(), "https://example.com/not-a-pr")
	if err == nil {
		t.Error("malformed url was accepted")
	}
}

func TestContextCancellationStopsThreads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cli := CLI{Path: fakeGH(t, `cat <<'EOF'
{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}
EOF`)}
	if _, err := cli.Threads(ctx, "https://github.com/o/r/pull/1"); err == nil {
		t.Error("a cancelled lookup succeeded")
	}
}

func TestThreadSettledDetectsFiledMarker(t *testing.T) {
	settled := Thread{Comments: []Comment{{Body: "<!-- pib:filed #42 -->\nFiled."}}}
	if !settled.Settled() {
		t.Error("expected settled")
	}
	unsettled := Thread{Comments: []Comment{{Body: "no marker here"}}}
	if unsettled.Settled() {
		t.Error("expected not settled")
	}
}

func TestThreadSettledDetectsBareFiledMarker(t *testing.T) {
	thread := Thread{Comments: []Comment{{Body: "<!-- pib:filed -->"}}}
	if !thread.Settled() {
		t.Error("expected settled for bare marker")
	}
}

func TestOutOfScopeParsesValidMarker(t *testing.T) {
	body := "<!-- pib:out-of-scope plan=orders id=money-type-is-float -->\nThe money type is a float."
	thread := Thread{Comments: []Comment{{Body: body}}}
	oos := thread.OutOfScope()
	if oos == nil {
		t.Fatal("expected OutOfScope, got nil")
	}
	if oos.Plan != "orders" || oos.ID != "money-type-is-float" {
		t.Errorf("OutOfScope = %+v, want plan=orders id=money-type-is-float", oos)
	}
	if !strings.Contains(oos.Body, "money type is a float") {
		t.Errorf("Body = %q, want finding text", oos.Body)
	}
}

func TestOutOfScopeStripsMarkerFromBody(t *testing.T) {
	body := "<!-- pib:out-of-scope plan=x id=y -->\n\nSome finding.\n"
	thread := Thread{Comments: []Comment{{Body: body}}}
	oos := thread.OutOfScope()
	if oos == nil {
		t.Fatal("expected OutOfScope, got nil")
	}
	if strings.Contains(oos.Body, "pib:out-of-scope") {
		t.Error("marker still present in body")
	}
	if oos.Body != "Some finding." {
		t.Errorf("Body = %q, want \"Some finding.\"", oos.Body)
	}
}

func TestOutOfScopeIgnoresMalformedMarker(t *testing.T) {
	cases := []string{
		"<!-- pib:out-of-scope plan=orders -->",         // missing id
		"<!-- pib:out-of-scope id=foo -->",              // missing plan
		"<!-- pib:out-of-scope plan= orders id=foo -->", // spaces around =
		"no marker at all",
	}
	for _, body := range cases {
		thread := Thread{Comments: []Comment{{Body: body}}}
		if thread.OutOfScope() != nil {
			t.Errorf("body %q should not parse", body)
		}
	}
}

func TestOutOfScopeRejectsBadSlugShape(t *testing.T) {
	badSlugs := []string{
		"<!-- pib:out-of-scope plan=bad plan id=foo -->", // space in plan
		"<!-- pib:out-of-scope plan=foo id=bad id -->",   // space in id
		"<!-- pib:out-of-scope plan=foo id=bar.baz -->",  // dot in id
	}
	for _, body := range badSlugs {
		thread := Thread{Comments: []Comment{{Body: body}}}
		if thread.OutOfScope() != nil {
			t.Errorf("body %q should be rejected", body)
		}
	}
}

func TestOutOfScopeUsesFirstMarkerInThread(t *testing.T) {
	thread := Thread{Comments: []Comment{
		{Body: "no marker"},
		{Body: "<!-- pib:out-of-scope plan=orders id=first -->\nFinding one."},
		{Body: "<!-- pib:out-of-scope plan=orders id=second -->\nFinding two."},
	}}
	oos := thread.OutOfScope()
	if oos == nil || oos.ID != "first" {
		t.Errorf("got %+v, want first marker", oos)
	}
}
