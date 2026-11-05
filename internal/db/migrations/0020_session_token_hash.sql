-- sessions.token holds sha256(token) (lower-case hex) from here on; the
-- rows of signed-in browsers and devices are hashed in place, so they keep
-- working (lookup hashes the presented token) and keep their rowid.
UPDATE sessions SET token = vault_sha256_hex(token);
