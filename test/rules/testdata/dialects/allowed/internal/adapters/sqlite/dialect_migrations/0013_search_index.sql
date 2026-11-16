-- SQLite's index of search_documents.

CREATE VIRTUAL TABLE search_fts USING fts5(body);
