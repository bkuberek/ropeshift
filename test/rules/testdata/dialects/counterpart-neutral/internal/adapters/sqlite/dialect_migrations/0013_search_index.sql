-- SQLite's index at the neutral file's version.

CREATE VIRTUAL TABLE search_fts USING fts5(body);
