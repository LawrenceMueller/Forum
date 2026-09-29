-- +goose Up

-- Accounts. Deliberately minimal: no email, no IP address, no last-login
-- time. Anything not stored here can never leak.
CREATE TABLE users (
    -- Internal only, never shown in URLs (profiles use the username),
    -- so the order people signed up in isn't exposed.
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    -- The generated name, e.g. blue-humming-otter. UNIQUE means Postgres
    -- itself refuses duplicates, even if the Go code has a bug.
    username      text NOT NULL UNIQUE
                  CONSTRAINT username_format CHECK (username ~ '^[a-z]+-[a-z]+-[a-z]+$'),

    -- An argon2id hash, never the password itself.
    password_hash text NOT NULL,

    created_at    timestamptz NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE users;
