-- Postgres's index of search_documents.

CREATE INDEX search_documents_text ON search_documents USING gin (to_tsvector('simple', body));
