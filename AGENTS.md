# AGENTS.md

## Project Summary

Clear Dream is a lightweight Go toolkit for building LLM agents without a heavy
framework. It ships an OpenAI-compatible chat client, a `Model` that runs the
tool-calling loop, a tool registry that derives JSON Schema definitions from Go
structs, and a LangGraph-inspired generic `Graph[S]` for composing nodes,
static edges and conditional edges into bounded DAG-style workflows over a
caller-defined state type. The goal is a small,
embeddable toolkit (Model + Graph = Agent), not a full framework.

## Tech Stack

- **Go 1.26** — module `github.com/libsilverwolf/clear-dream`
- **Resty v3** — HTTP client for OpenAI-compatible `/chat/completions`
- **logrus** — structured logging (loggers carry a `module` field)
- **eris** — error wrapping with context
- **google/uuid** — per-request `x-opencode-session` header
- **OpenAI Chat Completions API** — any compatible endpoint (OpenAI, DeepSeek,
  Ollama, vLLM, ...)
- **Standard library testing** (`testing`, `httptest`) — no external test
  framework
- **Task** (`Taskfile.yml`) — optional task runner

No database, ORM, or web framework; everything outside the dependencies above
comes from the standard library.

## Project Structure

```text
.
├── agent/                  # Agent runtime components
│   ├── client.go           # OpenAI-compatible HTTP client
│   ├── model.go            # Model: completion + tool-calling loop
│   ├── graph.go            # Graph[S]: generic DAG builder, Compile, Invoke, START/END
│   ├── client_test.go
│   ├── model_test.go
│   └── graph_test.go
├── model/
│   ├── openai.go           # Chat Completions types, tools, JSON Schema, chunks
│   └── context.go          # LlmContext (any) passed to tool handlers
├── tools/
│   ├── tools.go            # Tool/ToolArgument, ToolRegistry, schema builder
│   ├── utils.go            # CreateTool / ExtractToolParams via reflection
│   ├── tools_test.go
│   └── utils_test.go
├── example/                # Standalone demos
│   ├── main.go             # REPL demo
│   ├── README.md
│   └── graph/              # Offline DAG pipeline demo
│       ├── main.go
│       └── README.md
├── Taskfile.yml            # `task repl`
├── README.md
├── go.mod
└── go.sum
```

Key entry points:

- `agent.NewModel` builds the LLM + tool loop from an
  `agent.OpenAiCompatableClient` and a `*tools.ToolRegistry`.
- `agent.NewDefaultOpenAiCompatableClient` builds the HTTP client.
- `agent.NewGraph[T]` → `AddNode` / `AddEdge` / `AddConditionalEdge` /
  `SetMaxSteps` → `Compile` → `CompiledGraph[T].Invoke`; nodes are
  `func(T) (T, error)` and routers are `func(T) (string, error)`.
- `tools.NewToolRegistry` + `tools.CreateTool` register tools; `Model` sends
  their definitions with every request.

## Project Constraints

### Common

- **Never guess.** When a situation is ambiguous, ask the user to confirm
  instead of acting on assumptions. Example: the user asks you to read
  `AGENTS.md`, but the folder only contains `AGENT.md` — verify with the
  user before proceeding.
- **English only in code.** To keep behavior consistent across different
  LLMs, all comments and descriptions in code MUST be written in English.
- **Read nested guides.** Attempt to read the `AGENTS.md` in every directory
  you work on.

### DAO Layer Design

- **Singular model names.** Name model files in singular form
  (e.g. `book.go`, not `books.go`).
- **Shared database singleton.** Design and implement a connection-reusable
  database singleton and share it across the entire app.
- **Free functions over structs/classes (Go / Rust / Node.js).** Do NOT
  implement data-access actions inside a struct or class. Write standalone
  functions that take the connection as an explicit argument, e.g.:

  ```go
  func get_book(db: DbConnection, book_id: int) { ... }
  ```

## Common Commands

| Command | Purpose |
| --- | --- |
| `go build ./...` | Build every package |
| `go vet ./...` | Run static checks |
| `gofmt -l .` | List files needing formatting (`gofmt -w .` fixes them) |
| `go test ./...` | Run all unit tests |
| `go test ./... -race` | Run tests with the race detector |
| `go test ./agent/ -v` | Run the agent package tests verbosely |
| `go test ./... -coverprofile=cover.out && go tool cover -func=cover.out` | Coverage report |
| `go run ./example` | Start the REPL demo |
| `go run ./example/graph` | Run the offline DAG pipeline demo |
| `task repl` | Start the REPL via Taskfile (loads `.env`) |

Environment variables used by the demo and HTTP client:

- `OPENAI_API_KEY` — bearer token; optional for local endpoints
- `OPENAI_BASE_URL` — defaults to `https://api.openai.com/v1`
- `OPENAI_MODEL` — defaults to `gpt-4o-mini` in the example and
  `deepseek-v4.1-flash` in `agent`

The REPL demo supports `/help`, `/tools`, `/exit` and `/quit`.
