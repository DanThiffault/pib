package issues

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Verdicts a settled review cycle can carry.
const (
	VerdictApproved = "approved"
	VerdictChanges  = "changes"
	VerdictError    = "error"
)

// verdicts are the outcomes the schema allows. Unlike a run status, an
// unrecognised verdict is rejected rather than recorded as unknown: the loop
// in internal/review branches on it, and a value it cannot read would be a
// silent decision to keep going.
var verdicts = map[string]bool{VerdictApproved: true, VerdictChanges: true, VerdictError: true}

// Review is one pass a code reviewer made over a pull request. Cycles are
// numbered per pull request, so a replacement pull request on the same issue
// starts again at one — the cap is per diff, not per issue.
type Review struct {
	ID        string    `json:"id"`
	Issue     int64     `json:"issue"`
	PRURL     string    `json:"prUrl"`
	Cycle     int       `json:"cycle"`
	Run       string    `json:"run,omitempty"`
	Verdict   string    `json:"verdict,omitempty"`
	Findings  int       `json:"findings"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitzero"`
}

// Running reports a cycle that was opened and has not been settled.
func (r Review) Running() bool { return r.EndedAt.IsZero() }

// OpenReview starts a cycle on a pull request and returns it, numbered one
// past the newest cycle already recorded for that pull request.
//
// The number is the store's to work out rather than the caller's: it is what
// makes a replacement pull request start at one, and a caller that counted
// its own passes would decide that property somewhere pib cannot test it.
func (s *Store) OpenReview(issue int64, prURL, run string) (Review, error) {
	if strings.TrimSpace(prURL) == "" {
		return Review{}, errors.New("a review needs a pull request url")
	}

	id, err := newReviewID()
	if err != nil {
		return Review{}, err
	}

	started := format(now())
	res, err := s.db.Exec(`
		INSERT INTO reviews (id, issue, pr_url, cycle, run, started_at)
		SELECT ?, ?, ?, COALESCE(MAX(cycle), 0) + 1, ?, ?
		FROM reviews WHERE issue = ? AND pr_url = ?`,
		id, issue, prURL, nullable(run), started, issue, prURL)
	if err != nil {
		return Review{}, wrapRef(err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return Review{}, fmt.Errorf("opening a review on issue #%d: no row written", issue)
	}

	return s.review(id)
}

// CloseReview settles a cycle with the verdict its reviewer reached and how
// many findings it filed.
func (s *Store) CloseReview(id, verdict string, findings int) (Review, error) {
	if !verdicts[verdict] {
		return Review{}, fmt.Errorf("%q is not a review verdict", verdict)
	}
	if findings < 0 {
		findings = 0
	}

	res, err := s.db.Exec(
		`UPDATE reviews SET verdict = ?, findings = ?, ended_at = ? WHERE id = ?`,
		verdict, findings, format(now()), id)
	if err != nil {
		return Review{}, err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return Review{}, fmt.Errorf("review %s: %w", id, ErrNotFound)
	}
	return s.review(id)
}

// Reviews lists an issue's cycles, oldest first. Every pull request the issue
// has had is included, in the order the cycles were opened, so a replacement
// pull request reads as a second run of cycles rather than a continuation.
//
// The order is insertion order rather than started_at: two cycles opened in
// the same second would otherwise sort by cycle number, interleaving a
// replacement pull request's cycle one with the previous request's later
// cycles.
func (s *Store) Reviews(issue int64) ([]Review, error) {
	rows, err := s.db.Query(`
		SELECT id, issue, pr_url, cycle, run, verdict, findings, started_at, ended_at
		FROM reviews WHERE issue = ? ORDER BY rowid`, issue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Review
	for rows.Next() {
		review, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, review)
	}
	return list, rows.Err()
}

// review reads one cycle back, so callers see the row as it landed.
func (s *Store) review(id string) (Review, error) {
	row := s.db.QueryRow(`
		SELECT id, issue, pr_url, cycle, run, verdict, findings, started_at, ended_at
		FROM reviews WHERE id = ?`, id)
	review, err := scanReview(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Review{}, fmt.Errorf("review %s: %w", id, ErrNotFound)
	}
	return review, err
}

func scanReview(row scanner) (Review, error) {
	var (
		review  Review
		run     sql.NullString
		verdict sql.NullString
		started string
		ended   sql.NullString
	)
	if err := row.Scan(&review.ID, &review.Issue, &review.PRURL, &review.Cycle,
		&run, &verdict, &review.Findings, &started, &ended); err != nil {
		return Review{}, err
	}

	review.Run = run.String
	review.Verdict = verdict.String
	review.StartedAt = parseTime(started)
	review.EndedAt = parseTime(ended.String)
	return review, nil
}

func newReviewID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
