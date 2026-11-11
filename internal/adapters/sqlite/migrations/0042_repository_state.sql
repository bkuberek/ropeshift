-- Whether a repository is in its project (ADR-0021). A member removes one that no open work targets: its
-- record stays as history, with its name, and its identity leaves the repository index, so another project
-- may register it. Registering it here again makes it active.
ALTER TABLE repositories ADD COLUMN state TEXT NOT NULL DEFAULT 'active'
  CONSTRAINT repository_state CHECK (state IN ('active', 'removed'));
