-- Let a retry start the review cap again.
--
-- A reviewer that keeps finding the same changes exhausts its cycles, and the
-- issue stops on its own: retrying it would otherwise re-open a pull request
-- already at the cap, with a verdict of changes, and go straight back to
-- needing attention. review_base is the cycle a retry resets from, so
-- review_cycle - review_base counts the passes since the user last said go
-- again. The reviews rows are kept — the detail pane renders their history.

ALTER TABLE issues ADD COLUMN review_base INTEGER NOT NULL DEFAULT 0;
