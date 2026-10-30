// Command example is a small, self-contained REPL that demonstrates the
// clear-dream agent, client and tool registry packages.
//
// Run it with:
//
//	OPENAI_API_KEY=sk-... OPENAI_BASE_URL=https://api.openai.com/v1 \
//	OPENAI_MODEL=gpt-4o-mini go run ./example
//
// Any OpenAI-compatible endpoint works, including local servers that do not
// require an API key.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/libsilverwolf/clear-dream/agent"
	"github.com/libsilverwolf/clear-dream/tools"
	"github.com/rotisserie/eris"
	"github.com/sirupsen/logrus"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
	defaultModel   = "gpt-4o-mini"

	helpText = `commands:
  /help        show this help
  /tools       list the tools the model can call
  /exit, /quit leave the REPL
anything else is sent to the model.`
)

type config struct {
	apiKey  string
	baseURL string
	model   string
}

func main() {
	// Debug logging keeps tool-registry and tool-call activity visible while
	// trying the demo; raise this to WarnLevel for a quieter prompt.
	logrus.SetLevel(logrus.DebugLevel)

	cfg := loadConfig()

	registry := tools.NewToolRegistry()
	if err := registerDemoTools(registry); err != nil {
		fmt.Fprintf(os.Stderr, "repl: register tools: %v\n", err)
		os.Exit(1)
	}

	client := agent.NewDefaultOpenAiCompatableClient(agent.NewDefaultOpenAiCompatableClientParams{
		ApiKey: cfg.apiKey,
		ApiUrl: cfg.baseURL,
		Model:  cfg.model,
	})
	bot := agent.NewModel(&agent.NewModelParams{
		ToolsRegistry: registry,
		ApiClient:     client,
		DefaultModel:  cfg.model,
	})

	printBanner(os.Stdout, cfg, registry)

	if err := runREPL(os.Stdin, os.Stdout, bot, registry); err != nil {
		fmt.Fprintf(os.Stderr, "repl: %v\n", err)
		os.Exit(1)
	}
}

func loadConfig() config {
	cfg := config{
		apiKey:  strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		baseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")), "/"),
		model:   strings.TrimSpace(os.Getenv("OPENAI_MODEL")),
	}
	if cfg.baseURL == "" {
		cfg.baseURL = defaultBaseURL
	}
	if cfg.model == "" {
		cfg.model = defaultModel
	}

	return cfg
}

func printBanner(out io.Writer, cfg config, registry *tools.ToolRegistry) {
	writef(out, "clear-dream REPL demo\n")
	writef(out, "model:    %s\n", cfg.model)
	writef(out, "endpoint: %s/chat/completions\n", cfg.baseURL)
	writef(out, "tools:    %s\n", strings.Join(toolNames(registry), ", "))
	if cfg.apiKey == "" {
		writef(out, "note:     OPENAI_API_KEY is empty; requests are sent without Authorization\n")
	}
	writef(out, "type /help for commands, /exit to quit\n\n")
}

// writef writes formatted output and ignores the error: the REPL has no way to
// recover from a broken stdout or stderr.
func writef(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

func toolNames(registry *tools.ToolRegistry) []string {
	definitions := registry.GetToolDefinitions()
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Function.Name)
	}

	return names
}

// runREPL reads one request per line, forwards it to the agent and prints the
// answer. Slash commands are handled locally.
func runREPL(in io.Reader, out io.Writer, bot *agent.Model, registry *tools.ToolRegistry) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for {
		writef(out, "> ")
		if !scanner.Scan() {
			writef(out, "\n")
			return scanner.Err()
		}

		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "":
			continue
		case "/exit", "/quit", "exit", "quit":
			writef(out, "bye\n")
			return nil
		case "/help":
			writef(out, "%s\n", helpText)
		case "/tools":
			for _, name := range toolNames(registry) {
				writef(out, "%s\n", name)
			}
		default:
			response, err := bot.Invoke(line)
			if err != nil {
				writef(out, "error: %v\n", err)
				continue
			}
			writef(out, "%s\n", response)
		}
	}
}

// ==========================================================================
// Demo tools
// ==========================================================================

type echoParams struct {
	Text string `description:"text to echo back"`
}

type addParams struct {
	A int `description:"first addend"`
	B int `description:"second addend"`
}

func registerDemoTools(registry *tools.ToolRegistry) error {
	echo, err := tools.CreateTool(tools.CreateToolParams{
		Name:        "echo",
		Description: "Echo the given text back to the caller.",
		ParamStruct: echoParams{},
		Handler: func(input any, _ any) (any, error) {
			var params echoParams
			if err := decodeArguments(input, &params); err != nil {
				return nil, err
			}

			return params.Text, nil
		},
	})
	if err != nil {
		return eris.Wrap(err, "create echo tool")
	}

	add, err := tools.CreateTool(tools.CreateToolParams{
		Name:        "add",
		Description: "Add two integers and return the sum.",
		ParamStruct: addParams{},
		Handler: func(input any, _ any) (any, error) {
			var params addParams
			if err := decodeArguments(input, &params); err != nil {
				return nil, err
			}

			return params.A + params.B, nil
		},
	})
	if err != nil {
		return eris.Wrap(err, "create add tool")
	}

	now, err := tools.CreateTool(tools.CreateToolParams{
		Name:        "current_time",
		Description: "Return the current local time in RFC3339 format.",
		Handler: func(_ any, _ any) (any, error) {
			return time.Now().Format(time.RFC3339), nil
		},
	})
	if err != nil {
		return eris.Wrap(err, "create current_time tool")
	}

	registry.RegisterTool("echo", echo)
	registry.RegisterTool("add", add)
	registry.RegisterTool("current_time", now)

	return nil
}

// decodeArguments parses the JSON argument string the model sent. Tools without
// parameters receive an empty string and leave target untouched.
func decodeArguments(input any, target any) error {
	raw, ok := input.(string)
	if !ok {
		return eris.Errorf("expected JSON argument string, got %T", input)
	}
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return eris.Wrap(err, "decode tool arguments")
	}

	return nil
}
