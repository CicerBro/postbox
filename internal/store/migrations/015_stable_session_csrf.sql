-- Existing CSRF tokens were random and only their hashes were stored. They
-- cannot be recovered for stable session reads, so require a fresh sign-in.
DELETE FROM sessions;
