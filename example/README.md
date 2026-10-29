# clear-dream REPL demo

A minimal, self-contained REPL built on top of `agent`, `model` and `tools`.

## Run

```bash
OPENAI_API_KEY=sk-... \
OPENAI_MODEL=gpt-4o-mini \
go run ./example
```

Works with any OpenAI-compatible endpoint:

```bash
OPENAI_API_KEY=anything \
OPENAI_BASE_URL=http://localhost:11434/v1 \
OPENAI_MODEL=qwen2.5 \
go run ./example
```

| Variable | Default | Description |
| --- | --- | --- |
| `OPENAI_API_KEY` | empty | Sent as `Authorization: Bearer <key>` when set |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | Base URL; the client appends `/chat/completions` |
| `OPENAI_MODEL` | `gpt-4o-mini` | Model sent with every request |

## Usage

```
> /tools
add
current_time
echo
> what time is it?
2026-10-30T00:00:00+08:00
> add 17 and 25
42
> /exit
bye
```

| Input | Action |
| --- | --- |
| `/help` | Show available commands |
| `/tools` | List tools registered with the agent |
| `/exit`, `/quit` | Leave the REPL (Ctrl-D also works) |
| anything else | Sent to the model, which may call the demo tools |

## Demo tools

- `echo` — returns the `Text` argument, showing string parameter mapping
- `add` — sums two integers, showing `integer` parameter mapping
- `current_time` — no parameters, returns local time in RFC3339

Each turn is independent: the REPL sends a single user message, so previous
answers are not part of the conversation. Tool calls are resolved inside
`agent.Invoke` and are not shown in the transcript.
