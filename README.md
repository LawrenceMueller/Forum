# Snowball

A small, anonymous, Reddit-style forum, hand-built by one person.

No email, no real names, no tracking, no ads. Snowball picks your username, you pick the password, and the site stores as little about you as it possibly can.

Built with Go (standard library `net/http` and `html/template`), htmx, and PostgreSQL.

## Running it locally

You'll need Go 1.25 or newer, Docker, and the [goose](https://github.com/pressly/goose) migration tool.

```
cp .env.example .env
docker compose up -d
goose up
go run ./cmd/snowball
```

Then open http://localhost:8080. Run the tests with `go test ./...`.

## Learn more

[docs/PROJECT_CONTEXT.md](docs/PROJECT_CONTEXT.md) covers everything: the values behind the project, the architecture, the database schema, every feature and where it lives, the decisions made along the way, and the roadmap.


docker compose exec db psql -U snowball

goose -dir ./migrations postgres "postgres://snowball:snowball-dev-only@localhost:5432/snowball?sslmode=disable" up