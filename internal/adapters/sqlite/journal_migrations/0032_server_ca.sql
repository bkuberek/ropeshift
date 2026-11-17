-- The certificate authority a group worker pinned when it joined: the SHA-256 of its certificate in lowercase
-- hexadecimal, which the worker's link verifies the server's certificate against. A worker that joined over
-- loopback, or verifies the server through the system's roots, holds the empty fingerprint.

ALTER TABLE server_link ADD COLUMN ca_sha256 TEXT NOT NULL DEFAULT '';
