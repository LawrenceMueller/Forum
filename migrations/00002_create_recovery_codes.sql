-- +goose Up

-- One-time codes for getting back into an account without email.
-- Only hashes are stored, like passwords.
CREATE TABLE recovery_codes (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- ON DELETE CASCADE: deleting a user deletes their codes too.
    user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  text NOT NULL,
    used_at    timestamptz          -- NULL until the code is spent
);

-- Postgres doesn't index foreign keys automatically. Recovery looks codes
-- up by user, so without this every lookup would scan the whole table.
CREATE INDEX recovery_codes_user_id_idx ON recovery_codes (user_id);

-- +goose Down

DROP TABLE recovery_codes;
