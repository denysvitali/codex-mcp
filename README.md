# codex-mcp

`codex-mcp` is a Go-based MCP stdio server that wraps OpenAI's Codex CLI and exposes `codex exec` as a structured MCP tool named `codex_exec`.

It is intended for agentic workflows where an MCP client such as Claude Code needs to delegate a self-contained coding task to Codex, receive the final assistant message, and capture execution metadata in a predictable schema.

## Features

- Exposes a single structured MCP tool: `codex_exec`
- Runs over stdio, making it easy to plug into local MCP clients
- Wraps Codex non-interactive execution with JSONL event parsing
- Supports both synchronous calls and MCP task-based execution
- Supports resuming existing Codex threads with `thread_id`
- Enforces an allowed workspace root plus optional additional allowed directories
- Normalizes paths and resolves symlinks before execution
- Automatically enables `--skip-git-repo-check` when the target directory is not a Git repository
- Limits concurrent Codex runs with a configurable semaphore
- Supports YAML configuration plus CLI flag overrides
- Emits structured JSON logs to stderr

## Requirements

- Go 1.26 or newer to build/install from source
- The `codex` CLI installed and available in `PATH`, or configured explicitly with `--codex-bin`
- A working Codex authentication/session on the machine running the server

## Installation

Install the server directly with Go:

```bash
go install github.com/denysvitali/codex-mcp/cmd/codex-mcp@latest
```

Then verify it is available:

```bash
codex-mcp version
```

## Configuration

`codex-mcp` reads a YAML config file from `~/.config/codex-mcp/config.yaml` by default when that file exists. CLI flags override file values.

### YAML configuration

Example:

```yaml
codex_bin: codex
root: /path/to/workspace
allow_dirs:
  - /path/to/extra-worktree

default:
  yolo: true
  model: gpt-5.4
  sandbox: workspace-write

max_concurrent_runs: 4
log_level: info
```

### Configuration fields

| Field | Type | Description |
| --- | --- | --- |
| `codex_bin` | string | Path to the Codex CLI binary. Defaults to `codex`. |
| `root` | string | Primary allowed workspace root. If omitted, the server falls back to the current working directory at startup. |
| `allow_dirs` | list of strings | Additional allowed directories for `cwd` resolution. |
| `default.yolo` | bool | Whether to run Codex in unrestricted mode by default. Defaults to `true`. |
| `default.model` | string | Default model passed to Codex when a request does not specify one. Defaults to `gpt-5.4`. |
| `default.sandbox` | string | Sandbox used when `default.yolo` is `false`. Valid values: `read-only`, `workspace-write`, `danger-full-access`. |
| `max_concurrent_runs` | integer | Maximum number of Codex processes allowed at once. Defaults to `4`. |
| `log_level` | string | Logrus level. Valid values include `error`, `warn`, `info`, `debug`, `trace`. Defaults to `info`. |

### CLI flags

The server can be configured entirely from flags:

```bash
codex-mcp serve \
  --root /path/to/workspace \
  --allow-dir /path/to/extra-worktree \
  --codex-bin /usr/local/bin/codex \
  --default-yolo=false \
  --default-sandbox workspace-write \
  --default-model gpt-5.4 \
  --max-concurrent-runs 2 \
  --log-level info
```

Available flags:

| Flag | Description |
| --- | --- |
| `--codex-bin` | Path to the Codex binary |
| `--root` | Primary allowed workspace root |
| `--allow-dir` | Additional allowed directory; repeatable |
| `--config` | Path to a YAML config file |
| `--default-yolo` | Enable unrestricted Codex execution by default |
| `--default-model` | Default model when requests omit `model` |
| `--default-sandbox` | Default sandbox when yolo is disabled |
| `--max-concurrent-runs` | Maximum concurrent Codex runs |
| `--log-level` | Logging verbosity |

## Usage

### Start the server manually

```bash
codex-mcp serve --root /path/to/workspace
```

The server communicates over stdio, so it is normally launched by an MCP client rather than used interactively.

### Claude Code configuration

Claude Code supports local stdio MCP servers. One simple way to register this server is:

```bash
claude mcp add codex-mcp -- codex-mcp serve --root /path/to/workspace
```

You can also add an equivalent JSON configuration. Example `.mcp.json` entry:

```json
{
  "mcpServers": {
    "codex-mcp": {
      "type": "stdio",
      "command": "codex-mcp",
      "args": ["serve", "--root", "/path/to/workspace"],
      "env": {}
    }
  }
}
```

If `codex-mcp` or `codex` are not in `PATH`, use absolute paths in the command and configure `--codex-bin` explicitly.

### MCP tool contract

The server exposes one tool:

- `codex_exec`

Input fields:

| Field | Required | Description |
| --- | --- | --- |
| `prompt` | Yes | Instructions sent to Codex |
| `cwd` | No | Working directory for the run; relative paths resolve from the server root |
| `thread_id` | No | Existing Codex thread ID to resume |
| `model` | No | Per-request model override |
| `profile` | No | Per-request Codex profile override |
| `sandbox` | No | Per-request sandbox override, used only when yolo is disabled in server config |
| `timeout_ms` | No | Per-request timeout in milliseconds |
| `skip_git_repo_check` | No | Overrides automatic Git repository detection |

Output fields:

| Field | Description |
| --- | --- |
| `thread_id` | Codex thread ID observed in the JSONL stream |
| `final_message` | Final assistant message returned by Codex |
| `usage` | Token usage summary |
| `elapsed_ms` | End-to-end execution time |
| `exit_code` | Process exit code |
| `mode` | `sync` or `task` |
| `raw_event_count` | Number of JSONL events parsed from Codex output |
| `stderr_tail` | Present on some error paths to help diagnose failures |

## Architecture Overview

The codebase is intentionally small and split into a few focused packages:

- [`cmd/codex-mcp/main.go`](/home/workspace/git/codex-mcp/cmd/codex-mcp/main.go) defines the Cobra CLI, loads config, validates runtime settings, and starts the stdio MCP server.
- [`internal/config/config.go`](/home/workspace/git/codex-mcp/internal/config/config.go) handles YAML loading, defaults, path normalization, and config validation.
- [`internal/mcpserver/server.go`](/home/workspace/git/codex-mcp/internal/mcpserver/server.go) defines the MCP server, registers `codex_exec`, validates tool input, and maps MCP calls to runner requests.
- [`internal/codexcli/runner.go`](/home/workspace/git/codex-mcp/internal/codexcli/runner.go) builds the `codex exec` command, enforces directory restrictions, manages timeouts and concurrency, parses JSONL output, and returns structured results.
- [`internal/codexcli/git.go`](/home/workspace/git/codex-mcp/internal/codexcli/git.go) performs Git repository detection used for `--skip-git-repo-check`.

At runtime the flow is:

1. The MCP client starts `codex-mcp` over stdio.
2. `codex-mcp` loads config, resolves the allowed roots, and checks that the Codex binary exists.
3. An MCP client invokes `codex_exec`.
4. The server validates input and forwards the request to the runner.
5. The runner resolves `cwd`, ensures it stays within the allowed directories, constructs the `codex exec` command, and executes it.
6. Codex JSONL output is parsed into a structured MCP tool result.

## Security Considerations

This project executes a local coding agent and should be deployed with deliberate constraints.

- Directory access is restricted to `root` and `allow_dirs`, and symlinks are resolved before execution to prevent simple path-escape tricks.
- Relative `cwd` values are resolved from the configured root, not from the caller's arbitrary current directory.
- Concurrent Codex runs are bounded by `max_concurrent_runs` to reduce resource contention.
- The default configuration enables yolo mode (`default.yolo: true`), which maps to Codex's unrestricted bypass flag. This is convenient but materially less restrictive than sandboxed execution.
- If you want stronger isolation, set `default.yolo: false` and use an explicit sandbox such as `workspace-write` or `read-only`.
- The server emits logs to stderr, including task metadata and failure context. Avoid sending logs to destinations that should not receive workspace paths or error output.
- The server runs whatever `codex` binary you point it to. Treat `codex_bin` as part of your trusted computing base.

## License

This repository does not currently include a `LICENSE` file. As a result, the license terms are not stated in-tree.

## References

- Anthropic Claude Code MCP documentation: https://docs.anthropic.com/en/docs/claude-code/mcp
