-- Two things a retry needs, both derived rather than stored as state.
--
-- A reviewer that keeps finding the same changes exhausts its cycles, and the
-- issue stops on its own: retrying it would otherwise re-open a pull request
-- already at the cap, with a verdict of changes, and go straight back to
-- needing attention. review_base is the cycle a retry resets from, so
-- review_cycle - review_base counts the passes since the user last said go
-- again. The reviews rows are kept — the detail pane renders their history.

ALTER TABLE issues ADD COLUMN review_base INTEGER NOT NULL DEFAULT 0;

-- Remember what the file said when it was last indexed.
--
-- Reindex reads title, type and acceptance back into columns, so a change to
-- those is visible in the row. The prose body is not indexed, and a body-only
-- edit — the usual way someone clarifies a task after an agent failed on it —
-- would otherwise leave the issue looking untouched. The hash of the file's
-- bytes is what says whether the issue changed, so reindex can move
-- updated_at for a change the columns cannot see.
--
-- Existing rows have no hash: the first reindex after this migration counts
-- as a change once, which is the same thing as saying the file has been
-- re-read.

ALTER TABLE issues ADD COLUMN indexed_hash TEXT;
