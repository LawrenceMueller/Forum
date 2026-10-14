-- The starter tree gave C# the alias ".net". Matching ignores punctuation,
-- so that alias really meant "net", and anyone typing "net" as a topic
-- (meaning networking, say) was filed under C#. "dotnet" stays. The admin
-- tag page now refuses aliases like this (tags.AliasProblem), and a test
-- checks the starter tree for them (TestStarterTreeIsHealthy).

-- +goose Up
DELETE FROM tag_aliases WHERE alias = '.net';

-- +goose Down
INSERT INTO tag_aliases (tag_id, alias)
SELECT id, '.net' FROM tags WHERE name = 'csharp'
ON CONFLICT DO NOTHING;
