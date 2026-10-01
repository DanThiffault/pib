-- Derived plan state needs two things the schema cannot say yet.
--
-- archived is the one plan state that is stored rather than derived: the
-- user puts a plan there by hand, and it must survive every re-derivation.
-- Everything else about a plan is read off its issues and runs.
--
-- Reviewer and planner runs have no issue, so until now nothing tied one to
-- the plan it works on: the closing-pass reviewer and `pib plan review` run
-- issue-less, and a planner is a placeholder until its plan is applied.
-- runs.plan is that tie. For runs on an issue it is the issue's plan, which
-- the store fills in, so every run of a plan can be found through one column.
-- runs.pass says whether a plan-reviewer run is the opening review, gating
-- the plan before work, or the closing review, settling it after the last
-- issue closes — the difference between "under review" and "complete".

ALTER TABLE plans ADD COLUMN archived_at TEXT;

ALTER TABLE runs ADD COLUMN plan TEXT;
ALTER TABLE runs ADD COLUMN pass TEXT CHECK (pass IS NULL OR pass IN ('opening', 'closing'));

CREATE INDEX runs_plan ON runs (plan) WHERE plan IS NOT NULL;
