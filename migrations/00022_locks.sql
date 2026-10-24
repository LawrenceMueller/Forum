-- Admin controls for a thread that's getting out of hand (any post, mega
-- polls included): locking a post stops new comments (and edits) while
-- leaving everything readable, and a poll can be closed early. Both go in
-- the public moderation log.

-- +goose Up
ALTER TABLE posts ADD COLUMN locked_at timestamptz;

ALTER TABLE mod_log DROP CONSTRAINT mod_log_action;
ALTER TABLE mod_log ADD CONSTRAINT mod_log_action
    CHECK (action IN ('remove', 'hold', 'restore', 'dismiss', 'suspend', 'lift', 'keep',
                      'cleanup', 'shadowban', 'unshadowban', 'lock', 'unlock', 'close'));

-- +goose Down
DELETE FROM mod_log WHERE action IN ('lock', 'unlock', 'close');
ALTER TABLE mod_log DROP CONSTRAINT mod_log_action;
ALTER TABLE mod_log ADD CONSTRAINT mod_log_action
    CHECK (action IN ('remove', 'hold', 'restore', 'dismiss', 'suspend', 'lift', 'keep',
                      'cleanup', 'shadowban', 'unshadowban'));
ALTER TABLE posts DROP COLUMN locked_at;
