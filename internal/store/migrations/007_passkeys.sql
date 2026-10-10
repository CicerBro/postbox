CREATE TABLE passkey_users (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  handle BLOB NOT NULL UNIQUE CHECK(length(handle)=32)
) STRICT;
CREATE TABLE passkeys (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  rp_id TEXT NOT NULL,
  credential_id BLOB NOT NULL,
  credential TEXT NOT NULL CHECK(json_valid(credential)),
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  UNIQUE(rp_id,credential_id)
) STRICT;
CREATE INDEX passkeys_user ON passkeys(user_id,rp_id);
CREATE TABLE passkey_ceremonies (
  binding_hash BLOB PRIMARY KEY,
  kind TEXT NOT NULL CHECK(kind IN ('register','login')),
  user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
  session_hash BLOB,
  auth_hash BLOB,
  name TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL CHECK(json_valid(state)),
  expires_at INTEGER NOT NULL
) STRICT;
CREATE INDEX passkey_ceremonies_expiry ON passkey_ceremonies(expires_at);
CREATE INDEX passkey_ceremonies_user ON passkey_ceremonies(user_id);
