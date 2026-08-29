-- Plans, issues, dependencies and agent runs.
--
-- The markdown file beside this database owns what the planner authored —
-- title, type, acceptance criteria, body, comments. The columns mirroring
-- frontmatter here are an index of that file, refreshed when it changes on
-- disk; the file always wins. Everything else is lifecycle, which the
-- database owns so that churn never rewrites a file someone has open.

CREATE TABLE plans (
    id          INTEGER PRIMARY KEY,
    slug        TEXT    NOT NULL UNIQUE,
    title       TEXT    NOT NULL,
    created_at  TEXT    NOT NULL,
    planner_run TEXT
);

CREATE TABLE issues (
    number        INTEGER PRIMARY KEY,
    plan_id       INTEGER NOT NULL REFERENCES plans(id),
    local_id      TEXT,
    parent        INTEGER REFERENCES issues(number),
    path          TEXT    NOT NULL,

    -- indexed copy of the file's frontmatter
    title         TEXT    NOT NULL,
    type          TEXT    NOT NULL,
    acceptance    TEXT,
    indexed_mtime INTEGER NOT NULL,
    indexed_size  INTEGER NOT NULL,

    -- lifecycle
    state         TEXT    NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    closed_at     TEXT,
    pr_url        TEXT,
    pr_state      TEXT CHECK (pr_state IS NULL OR pr_state IN ('open', 'merged', 'closed')),
    pr_checked_at TEXT,
    created_at    TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL,

    UNIQUE (plan_id, local_id)
);

CREATE INDEX issues_plan  ON issues (plan_id);
CREATE INDEX issues_state ON issues (state);

CREATE TABLE deps (
    blocked INTEGER NOT NULL REFERENCES issues(number) ON DELETE CASCADE,
    blocker INTEGER NOT NULL REFERENCES issues(number) ON DELETE CASCADE,
    PRIMARY KEY (blocked, blocker),
    CHECK (blocked <> blocker)
);

CREATE INDEX deps_blocker ON deps (blocker);

CREATE TABLE runs (
    id           TEXT PRIMARY KEY,
    issue        INTEGER REFERENCES issues(number),
    agent        TEXT NOT NULL,
    tmux_window  TEXT,
    started_at   TEXT NOT NULL,
    ended_at     TEXT,
    status       TEXT CHECK (status IS NULL OR status IN ('done', 'needs_input', 'error', 'unknown'))
);

CREATE INDEX runs_issue ON runs (issue);

-- An issue is in progress exactly while it has a run that has not ended, so
-- that lookup is the hot one.
CREATE INDEX runs_live ON runs (issue) WHERE ended_at IS NULL;
