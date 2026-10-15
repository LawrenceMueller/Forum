-- Moderation, slice 3: cleaning up an account (removing everything it
-- posted and withdrawing its votes) and shadowbans for spam bots.
--
-- A shadowbanned account's new posts and comments are saved already
-- removed, with the reason 'shadow': the author still sees them as if
-- they were up; nobody else does. Its votes and reports are dropped.
-- Only for obvious spam bots: on people it would be deception
-- (docs/PROJECT_CONTEXT.md, section 8).

-- +goose Up
ALTER TABLE users ADD COLUMN shadowbanned_at timestamptz;

ALTER TABLE posts DROP CONSTRAINT post_removal_reason;
ALTER TABLE posts ADD CONSTRAINT post_removal_reason
    CHECK (removal_reason IN ('spam', 'harassment', 'personal', 'illegal', 'rules', 'held', 'shadow'));
ALTER TABLE comments DROP CONSTRAINT comment_removal_reason;
ALTER TABLE comments ADD CONSTRAINT comment_removal_reason
    CHECK (removal_reason IN ('spam', 'harassment', 'personal', 'illegal', 'rules', 'held', 'shadow'));

ALTER TABLE mod_log DROP CONSTRAINT mod_log_action;
ALTER TABLE mod_log ADD CONSTRAINT mod_log_action
    CHECK (action IN ('remove', 'hold', 'restore', 'dismiss', 'suspend', 'lift', 'keep',
                      'cleanup', 'shadowban', 'unshadowban'));

-- +goose Down
DELETE FROM mod_log WHERE action IN ('cleanup', 'shadowban', 'unshadowban');
ALTER TABLE mod_log DROP CONSTRAINT mod_log_action;
ALTER TABLE mod_log ADD CONSTRAINT mod_log_action
    CHECK (action IN ('remove', 'hold', 'restore', 'dismiss', 'suspend', 'lift', 'keep'));
UPDATE posts SET removal_reason = 'spam' WHERE removal_reason = 'shadow';
UPDATE comments SET removal_reason = 'spam' WHERE removal_reason = 'shadow';
ALTER TABLE comments DROP CONSTRAINT comment_removal_reason;
ALTER TABLE comments ADD CONSTRAINT comment_removal_reason
    CHECK (removal_reason IN ('spam', 'harassment', 'personal', 'illegal', 'rules', 'held'));
ALTER TABLE posts DROP CONSTRAINT post_removal_reason;
ALTER TABLE posts ADD CONSTRAINT post_removal_reason
    CHECK (removal_reason IN ('spam', 'harassment', 'personal', 'illegal', 'rules', 'held'));
ALTER TABLE users DROP COLUMN shadowbanned_at;
