-- A Postgres migration with no SQLite counterpart.

CREATE INDEX extra ON search_documents (body);
