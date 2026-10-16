-- Notes from the admin: short updates in Markdown ("comment threading is
-- live", "down for maintenance Sunday"). The newest one shows in a box on
-- the front page; all of them are listed at /notes. Hidden notes stay in
-- the table (and the admin's list) but show nowhere public.

-- +goose Up
CREATE TABLE admin_notes (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    body       text        NOT NULL CONSTRAINT admin_note_length CHECK (char_length(body) BETWEEN 1 AND 2000),
    created_at timestamptz NOT NULL DEFAULT now(),
    edited_at  timestamptz,
    hidden_at  timestamptz
);

-- +goose Down
DROP TABLE admin_notes;
