-- Record review cycles rather than counting them.
--
-- The interface shows the history and not just the number: the issue detail
-- pane renders each cycle, its verdict and its finding count. A counter
-- column on issues could not answer "what did cycle one find".
--
-- Keying on (issue, pr_url, cycle) means a worker that opens a replacement
-- pull request starts its cycles at one again. The cap is per pull request,
-- not per issue.

CREATE TABLE reviews (
    id         TEXT PRIMARY KEY,
    issue      INTEGER NOT NULL REFERENCES issues(number) ON DELETE CASCADE,
    pr_url     TEXT    NOT NULL,
    cycle      INTEGER NOT NULL,
    run        TEXT REFERENCES runs(id),
    verdict    TEXT CHECK (verdict IS NULL OR verdict IN ('approved', 'changes', 'error')),
    findings   INTEGER NOT NULL DEFAULT 0,
    started_at TEXT NOT NULL,
    ended_at   TEXT,
    UNIQUE (issue, pr_url, cycle)
);
