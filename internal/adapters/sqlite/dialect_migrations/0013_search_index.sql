-- SQLite's index of search_documents. FTS5 keeps no copy of the text, and the triggers keep
-- it in step, so a statement that writes the table cannot leave the index behind.

CREATE VIRTUAL TABLE search_fts USING fts5(
  body,
  content = 'search_documents',
  content_rowid = 'rowid',
  tokenize = 'unicode61'
);

CREATE TRIGGER search_documents_insert AFTER INSERT ON search_documents BEGIN
  INSERT INTO search_fts (rowid, body) VALUES (new.rowid, new.body);
END;

CREATE TRIGGER search_documents_delete AFTER DELETE ON search_documents BEGIN
  INSERT INTO search_fts (search_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
END;

CREATE TRIGGER search_documents_update AFTER UPDATE ON search_documents BEGIN
  INSERT INTO search_fts (search_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
  INSERT INTO search_fts (rowid, body) VALUES (new.rowid, new.body);
END;
