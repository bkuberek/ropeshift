-- SQLite's index of search_documents, with no Postgres counterpart.

CREATE VIRTUAL TABLE search_fts USING fts5(body);
