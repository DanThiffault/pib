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
