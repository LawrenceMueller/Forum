-- Moderation, slice 2: suspensions (timed or permanent) and appeals.
-- A suspended account is read-only: it can log in, read, and delete its own
-- posts and comments, but not post, comment, vote, report or edit. It gets
-- one appeal per suspension. Design: docs/PROJECT_CONTEXT.md, section 11.2.

-- +goose Up
CREATE TABLE suspensions (
    id                bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id           bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason            text        NOT NULL CONSTRAINT suspension_reason
                                  CHECK (reason IN ('spam', 'harassment', 'personal', 'illegal', 'rules')),
    note              text        NOT NULL DEFAULT '' CONSTRAINT suspension_note_length CHECK (char_length(note) <= 500),
    created_at        timestamptz NOT NULL DEFAULT now(),
    ends_at           timestamptz,           -- NULL: permanent
    lifted_at         timestamptz,           -- ended early by the admin (or replaced by a new suspension)
    appeal            text        CONSTRAINT suspension_appeal_length CHECK (char_length(appeal) <= 500),
    appealed_at       timestamptz,
    appeal_handled_at timestamptz
);
-- "Is this account suspended right now?" runs with every logged-in request.
CREATE INDEX suspensions_user_idx ON suspensions (user_id, id DESC) WHERE lifted_at IS NULL;
CREATE INDEX suspensions_open_appeals_idx ON suspensions (appealed_at)
    WHERE appeal IS NOT NULL AND appeal_handled_at IS NULL AND lifted_at IS NULL;

-- The log now covers accounts too: suspensions name no post, and carry how
-- long they last ("7 days", "permanent") in detail.
ALTER TABLE mod_log ALTER COLUMN post_public_id DROP NOT NULL;
ALTER TABLE mod_log ADD COLUMN detail text;
ALTER TABLE mod_log DROP CONSTRAINT mod_log_action;
ALTER TABLE mod_log ADD CONSTRAINT mod_log_action
    CHECK (action IN ('remove', 'hold', 'restore', 'dismiss', 'suspend', 'lift', 'keep'));

-- +goose Down
DELETE FROM mod_log WHERE action IN ('suspend', 'lift', 'keep');
ALTER TABLE mod_log DROP CONSTRAINT mod_log_action;
ALTER TABLE mod_log ADD CONSTRAINT mod_log_action CHECK (action IN ('remove', 'hold', 'restore', 'dismiss'));
ALTER TABLE mod_log DROP COLUMN detail;
ALTER TABLE mod_log ALTER COLUMN post_public_id SET NOT NULL;
DROP TABLE suspensions;
