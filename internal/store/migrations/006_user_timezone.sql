-- Empty uses the browser timezone until the user explicitly selects a timezone.
ALTER TABLE users ADD COLUMN timezone TEXT NOT NULL DEFAULT '';
