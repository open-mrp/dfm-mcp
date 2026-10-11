# dfm-mcp

The [DFM check engine](https://github.com/open-mrp/dfm) as a local MCP server: an agent asks whether a sheet-metal drawing (DXF, SVG or STEP) can be made on a given machine from a given stock, and gets every problem back with where it is and how to fix it, in milliseconds, on the machine the drawing is on.

The drawing never leaves the machine. The server reaches the DFM service only when the user asks it to: to record a result, to file a feedback report, or to send the usage counts they opted into.

## Install

```sh
brew install open-mrp/tap/dfm-mcp                                               # Homebrew, macOS or Linux
curl -fsSL https://raw.githubusercontent.com/open-mrp/dfm-mcp/main/install.sh | sh   # macOS or Linux, into ~/.local/bin
go install github.com/open-mrp/dfm-mcp/cmd/dfm-mcp@latest                       # from source
```

The script downloads the release for your machine and checks it against the release's checksums; `DFM_MCP_VERSION=v1.2.3` pins a release and `DFM_MCP_INSTALL_DIR` picks the directory. On Windows, take a binary from the [releases](https://github.com/open-mrp/dfm-mcp/releases/latest). Then add it to your client:

```json
{ "mcpServers": { "dfm": { "command": "dfm-mcp" } } }
```

```sh
claude mcp add --scope user dfm -- dfm-mcp     # Claude Code
codex mcp add dfm -- dfm-mcp                   # Codex
gemini mcp add dfm dfm-mcp                     # Gemini CLI
```

## Tools

| Tool                                                       | Does                                                                                                                                                                                                            |
| ---------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `dfm_check_design`                                         | Checks a drawing (path or base64) against a preset or capability; concise summary by default, `response_format: detailed` for every occurrence; `record: true` (with consent) records the result on the service |
| `dfm_get_check`                                            | A check by id, local or recorded                                                                                                                                                                                |
| `dfm_list_capability_presets`, `dfm_get_capability_preset` | The reference machines and their limits                                                                                                                                                                         |
| `dfm_list_finding_codes`                                   | Explains finding codes, all or the ones named                                                                                                                                                                   |
| `dfm_report_feedback`                                      | Reports a wrong result; asks the user through elicitation whether to share the drawing when the client supports it                                                                                              |

The catalogs are also resources (`dfm://finding-codes`, `dfm://capability-presets`, `dfm://capability-presets/{code}`), a check is `dfm://checks/{id}`, and the `dfm_review_drawing` prompt walks through a review.

## What leaves the machine, and when

Results stay in `~/.local/state/dfm/checks`. Nothing reaches the service until the user records a check or reports a wrong result; a report records the check first and files the feedback against it. The API key for that is minted the first time it is needed and saved to `~/.config/dfm/credentials.json`, or taken from `DFM_API_KEY`. `DFM_API_URL` points the server at another deployment.

If the server crashes, on a request or in a background task, it fails only that request or task instead of stopping, and keeps a report in `~/.local/state/dfm/crashes` (the newest 20). A crash report carries:

- a random crash id, made for that crash;
- the operation that crashed (the tool, such as `dfm_check_design`, or a background task), the panic's message and the stack as function and file names, without directories;
- this program's version, the engine version, the operating system, the architecture and the MCP client, as for telemetry;
- when it happened.

Never the call's arguments, an installation id or a drawing; a panic's message is the program's own, though it can quote a value the program was handling. Crash reports are sent to the service only after `dfm-mcp crash-reports on`, as each crash happens; `dfm-mcp crash-reports` shows the choice, and `crash-reports off` or `DO_NOT_TRACK=1` keeps them on the machine. Otherwise a crashed tool call carries a link to a GitHub issue prefilled with the report, filed only if the user opens it and submits it. Either way the crash is counted under `crashes`.

When the server does call the service, the service logs the request's metadata (route, status, key id, client address and user agent) for usage reports, never a body and never a drawing.

Usage is counted locally and sent only after `dfm-mcp telemetry on`. Once on, the server sends what it counted about once a minute while it is in use, and once more when it stops. Each report carries:

- the counters, from a fixed list the service enforces: `checks`, `checks_recorded`, `feedback`, `errors`, `crashes`, and `verdict:`, `format:` and `process:` followed by one of the engine's values;
- a random installation id, made on this machine and tied to no account, API key, user or machine name (`dfm-mcp telemetry reset-id` replaces it);
- the MCP client the server is working for, as that client names itself (such as `claude-code`), and this program's version, the engine version, the operating system and the architecture;
- the period the counts cover.

Never a file name, path, size, hash, geometry, material, capability, finding, key or host name. `dfm-mcp telemetry` shows the mode, the id and this week's counts; `telemetry local` keeps counting on the machine without sending, and `telemetry off` or `DO_NOT_TRACK=1` counts nothing. Turning sending off drops anything still waiting to be sent, and counts from before it was turned on are never sent.

## Development

```sh
make test      # unit tests, including the server against a fake of the service
make lint      # staticcheck and gosec
make run       # the server over stdio against a service on localhost:8080
```

The engine is the published module `github.com/open-mrp/dfm`, pinned in `go.mod`.
