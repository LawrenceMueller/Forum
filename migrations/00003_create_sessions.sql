-- +goose Up

-- Logged-in sessions. The browser holds a random token in a cookie; only a
-- hash of it is stored here, so a leaked database can't be used to log in.
-- No IP address or browser details, on purpose.
CREATE TABLE sessions (
    token_hash  text PRIMARY KEY,
    user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);

-- +goose Down

DROP TABLE sessions;
