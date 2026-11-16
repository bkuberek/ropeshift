-- A neutral migration runs on both databases.

CREATE TABLE search_documents (
  id   TEXT NOT NULL PRIMARY KEY,
  body TEXT NOT NULL
);
