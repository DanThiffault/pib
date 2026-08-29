-- Give a plan somewhere to say what it is for.
--
-- The plan used to be five columns of identity, while a container issue of
-- type "feature" held the goal, the scope and the feature-level acceptance
-- criteria. That put the content in the wrong record: a plan could only ever
-- print its own title, and the issue holding the real material never closed,
-- never launched an agent, and sat in the ready set forever.
--
-- A plan now has a markdown file of its own, indexed the same way an issue's
-- is, so it can be hand-edited and the file still wins.

ALTER TABLE plans ADD COLUMN path          TEXT    NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN acceptance    TEXT;
ALTER TABLE plans ADD COLUMN indexed_mtime INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plans ADD COLUMN indexed_size  INTEGER NOT NULL DEFAULT 0;
