-- Reply notifications. Nothing about the replies themselves is stored: they
-- are comments already, and the inbox (/account/replies) is worked out from
-- them. Each account keeps only two values: how many replies arrived since
-- it last looked (for the header, so pages don't have to count), and when
-- it last looked (to mark which replies are new).

-- +goose Up
ALTER TABLE users
    ADD COLUMN unread_replies  integer     NOT NULL DEFAULT 0 CONSTRAINT unread_replies_not_negative CHECK (unread_replies >= 0),
    ADD COLUMN replies_seen_at timestamptz NOT NULL DEFAULT now();

-- +goose Down
ALTER TABLE users DROP COLUMN replies_seen_at, DROP COLUMN unread_replies;
