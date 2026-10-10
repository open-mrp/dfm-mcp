# AGENTS.md

`dfm-mcp` (`github.com/open-mrp/dfm-mcp`): the DFM check engine (`github.com/open-mrp/dfm`) as a local MCP server over stdio. It is public and released as a binary; the DFM service it reports to (`dfm-api`) is private. Read `README.md` for what it does, its tools, and what leaves the machine.

## Commands

```sh
make test   # unit tests; the server is exercised against a fake of the service, never a real one
make lint   # staticcheck and gosec
make run    # serve over stdio against a service on localhost:8080
```

## Layout

- `cmd/dfm-mcp` is the binary: `Run` serves MCP, and the `telemetry` subcommand shows or changes the upload mode.
- `internal/mcpserver` is the server: tools, resources and prompt (`server.go`), the engine, store and counters behind them (`local.go`), and the little of the service it calls (`api.go`: record, feedback, fetch a check, mint a key, upload telemetry).
- `internal/localstore` keeps results on the machine. `telemetry` (public, imported by the service for its allowlist) counts and uploads. `internal/client` is the HTTP client and the API version it speaks.
- `internal/resource` and `internal/constants` are the service's wire shapes, copied from it: the service's OpenAPI spec is the contract, and these must match it. `internal/engine` assembles the engine's check-request document and runs it; `internal/apierrors` maps engine errors onto the service's error codes so the agent sees one shape either way.

## Rules

- Hand-written, never generated, never hosted: tools are shaped for tasks, not endpoints; every tool returns a text summary and structured content; errors come back as `isError` results in the service's error shape.
- Nothing leaves the machine without the user's consent: `record`, `share_design` (asked through elicitation when the client supports it), `telemetry on`. A drawing is sent only with a feedback report the user chose to share it with.
- A new telemetry counter goes into `telemetry.Allowed` and the README's "What leaves the machine, and when" list together, and never carries a value from a drawing. The service refuses any other name. The same section is the user's only description of telemetry, so a change to what a report carries (its fields, its cadence) changes it too.
- The service's routes this module calls are a published contract: a change there is a new API version, and this client pins the version it speaks in `internal/client`.
- Comments: one paragraph per line, no hard wrapping; explain intent and side effects, not mechanics. Conventional Commits; release-please versions from them and goreleaser attaches the binaries.
- The engine is the published module `github.com/open-mrp/dfm`, pinned in `go.mod`; bump it with `go get github.com/open-mrp/dfm@<version>` and keep the copies in `internal/resource` and `internal/constants` in step with the API that ships against the same engine.
