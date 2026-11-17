-- Postgres's index at the neutral file's version.

CREATE INDEX search_documents_text ON search_documents USING gin (to_tsvector('simple', body));
