-- +goose Up

-- Deleting an account keeps the row, so its username stays taken forever
-- (the UNIQUE constraint does the work), but wipes the password.
ALTER TABLE users ADD COLUMN deleted_at timestamptz;
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

-- An account has a password hash exactly when it isn't deleted. Postgres
-- enforces this itself, so no bug can leave a live account without a
-- password, or a deleted one with a password still in it.
ALTER TABLE users ADD CONSTRAINT password_only_while_active
    CHECK ((deleted_at IS NULL) = (password_hash IS NOT NULL));

-- +goose Down

-- Note: this fails if any accounts have been deleted, since they have no
-- password to satisfy NOT NULL. Remove those rows first if you really
-- need to roll back.
ALTER TABLE users DROP CONSTRAINT password_only_while_active;
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
ALTER TABLE users DROP COLUMN deleted_at;
