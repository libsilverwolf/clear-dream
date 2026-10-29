package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/libsilverwolf/clear-dream/model"
)

// newTestClient builds a client against a test server.
func newTestClient(t *testing.T, apiURL, apiKey string) *DefaultOpenAiCompatableClient {
	t.Helper()

	return NewDefaultOpenAiCompatableClient(NewDefaultOpenAiCompatableClientParams{
		ApiKey: apiKey,
		ApiUrl: apiURL,
		Model:  "test-model",
	})
}

// capturedRequest is a snapshot of what the test server received.
type capturedRequest struct {
	method        string
	path          string
	authorization string
	session       string
	contentType   string
	body          model.ChatCompletionRequest
}

func TestNewDefaultOpenAiCompatableClientStoresConfig(t *testing.T) {
	c := NewDefaultOpenAiCompatableClient(NewDefaultOpenAiCompatableClientParams{
		ApiKey: "secret",
		ApiUrl: "https://api.example.test/v1",
		Model:  "some-model",
	})

	if c.apiKey != "secret" {
		t.Fatalf("apiKey = %q, want %q", c.apiKey, "secret")
	}
	if c.apiUrl != "https://api.example.test/v1" {
		t.Fatalf("apiUrl = %q, want %q", c.apiUrl, "https://api.example.test/v1")
	}
	if c.model != "some-model" {
		t.Fatalf("model = %q, want %q", c.model, "some-model")
	}
	if c.restyClient == nil {
		t.Fatal("restyClient = nil, want an initialized HTTP client")
	}
	if got := c.restyClient.Timeout(); got != requestTimeout {
		t.Fatalf("restyClient.Timeout() = %v, want %v", got, requestTimeout)
	}
}

func TestGetResponseMissingApiUrl(t *testing.T) {
	c := NewDefaultOpenAiCompatableClient(NewDefaultOpenAiCompatableClientParams{ApiKey: "secret"})

	_, err := c.GetResponse(model.ChatCompletionRequest{Model: "m"})
	if err == nil {
		t.Fatal("GetResponse() error = nil, want an error for a missing api url")
	}
	if !strings.Contains(err.Error(), "api url is not configured") {
		t.Fatalf("GetResponse() error = %v, want it to mention %q", err, "api url is not configured")
	}
}

func TestGetResponseSendsRequestAndDecodesResponse(t *testing.T) {
	requests := make(chan capturedRequest, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body model.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		requests <- capturedRequest{
			method:        r.Method,
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			session:       r.Header.Get("x-opencode-session"),
			contentType:   r.Header.Get("Content-Type"),
			body:          body,
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id": "chatcmpl-1",
			"object": "chat.completion",
			"created": 1700000000,
			"model": "test-model",
			"choices": [{
				"index": 0,
				"message": {"role": "assistant", "content": "pong"},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}
		}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "secret")
	request := model.ChatCompletionRequest{
		Model:    "test-model",
		Messages: []model.Message{model.NewTextMessage(model.RoleUser, "ping")},
	}

	response, err := client.GetResponse(request)
	if err != nil {
		t.Fatalf("GetResponse() error = %v, want nil", err)
	}

	got := <-requests
	if got.method != http.MethodPost {
		t.Fatalf("request method = %q, want %q", got.method, http.MethodPost)
	}
	if want := "/chat/completions"; got.path != want {
		t.Fatalf("request path = %q, want %q", got.path, want)
	}
	if want := "Bearer secret"; got.authorization != want {
		t.Fatalf("Authorization header = %q, want %q", got.authorization, want)
	}
	if got.session == "" {
		t.Fatal("x-opencode-session header is empty, want a generated session id")
	}
	if _, err := uuid.Parse(got.session); err != nil {
		t.Fatalf("x-opencode-session = %q is not a valid UUID: %v", got.session, err)
	}
	if !strings.Contains(got.contentType, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", got.contentType)
	}
	if got.body.Model != "test-model" {
		t.Fatalf("request model = %q, want %q", got.body.Model, "test-model")
	}
	if len(got.body.Messages) != 1 || got.body.Messages[0].Content != "ping" {
		t.Fatalf("request messages = %#v, want a single %q user message", got.body.Messages, "ping")
	}

	if response.ID != "chatcmpl-1" {
		t.Fatalf("response.ID = %q, want %q", response.ID, "chatcmpl-1")
	}
	if len(response.Choices) != 1 {
		t.Fatalf("len(response.Choices) = %d, want 1", len(response.Choices))
	}
	if response.Choices[0].Message.Content != "pong" {
		t.Fatalf("choice content = %q, want %q", response.Choices[0].Message.Content, "pong")
	}
	if response.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want %q", response.Choices[0].FinishReason, "stop")
	}
	if response.Usage.TotalTokens != 5 {
		t.Fatalf("usage.total_tokens = %d, want 5", response.Usage.TotalTokens)
	}
}

func TestGetResponseWithoutApiKeyOmitsAuthorization(t *testing.T) {
	requests := make(chan capturedRequest, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body model.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		requests <- capturedRequest{
			authorization: r.Header.Get("Authorization"),
			session:       r.Header.Get("x-opencode-session"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices": []}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")

	if _, err := client.GetResponse(model.ChatCompletionRequest{Model: "m"}); err != nil {
		t.Fatalf("GetResponse() error = %v, want nil", err)
	}

	got := <-requests
	if got.authorization != "" {
		t.Fatalf("Authorization header = %q, want it to be omitted", got.authorization)
	}
	if got.session == "" {
		t.Fatal("x-opencode-session header is empty, want a generated session id")
	}
}

func TestGetResponseGeneratesUniqueSessionPerCall(t *testing.T) {
	sessions := make(chan string, 2)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessions <- r.Header.Get("x-opencode-session")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices": []}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "secret")

	for i := 0; i < 2; i++ {
		if _, err := client.GetResponse(model.ChatCompletionRequest{Model: "m"}); err != nil {
			t.Fatalf("GetResponse() call %d error = %v, want nil", i, err)
		}
	}

	first, second := <-sessions, <-sessions
	if first == second {
		t.Fatalf("both calls used session id %q, want a fresh id per call", first)
	}
	for _, session := range []string{first, second} {
		if _, err := uuid.Parse(session); err != nil {
			t.Fatalf("session id %q is not a valid UUID: %v", session, err)
		}
	}
}

func TestGetResponseInvalidJSONBodyReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices": [`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "secret")

	if _, err := client.GetResponse(model.ChatCompletionRequest{Model: "m"}); err == nil {
		t.Fatal("GetResponse() error = nil, want a decode error for a malformed body")
	}
}

func TestGetResponseErrorStatusWithApiError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid api key","type":"authentication_error","code":"invalid_api_key"}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "secret")

	_, err := client.GetResponse(model.ChatCompletionRequest{Model: "m"})
	if err == nil {
		t.Fatal("GetResponse() error = nil, want an HTTP status error")
	}
	for _, want := range []string{"completion request failed", "401", "invalid api key"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("GetResponse() error = %v, want it to contain %q", err, want)
		}
	}
}

func TestGetResponseErrorStatusWithoutApiErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "secret")

	_, err := client.GetResponse(model.ChatCompletionRequest{Model: "m"})
	if err == nil {
		t.Fatal("GetResponse() error = nil, want an HTTP status error")
	}
	for _, want := range []string{"completion request failed", "500"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("GetResponse() error = %v, want it to contain %q", err, want)
		}
	}
}

func TestGetResponseUnreachableServerReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	apiUrl := server.URL
	server.Close()

	client := newTestClient(t, apiUrl, "secret")

	_, err := client.GetResponse(model.ChatCompletionRequest{Model: "m"})
	if err == nil {
		t.Fatal("GetResponse() error = nil, want a transport error")
	}
	if !strings.Contains(err.Error(), "failed to send completion request") {
		t.Fatalf("GetResponse() error = %v, want it to mention %q", err, "failed to send completion request")
	}
}
