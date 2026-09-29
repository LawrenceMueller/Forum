-- +goose Up

-- Used codes are deleted rather than timestamped: a code is valid exactly
-- when its row exists, and there's no record of when anyone recovered.
ALTER TABLE recovery_codes DROP COLUMN used_at;

-- +goose Down

ALTER TABLE recovery_codes ADD COLUMN used_at timestamptz;
