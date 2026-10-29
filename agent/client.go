package agent

import (
	"github.com/google/uuid"
	"github.com/libsilverwolf/clear-dream/model"
	"github.com/rotisserie/eris"
	"resty.dev/v3"
)

type OpenAiCompatableClient interface {
	GetResponse(input model.ChatCompletionRequest) (model.ChatCompletionResponse, error)
}

type DefaultOpenAiCompatableClient struct {
	apiKey      string
	apiUrl      string
	model       string
	restyClient *resty.Client
}

type NewDefaultOpenAiCompatableClientParams struct {
	ApiKey string
	ApiUrl string
	Model  string
}

func NewDefaultOpenAiCompatableClient(params NewDefaultOpenAiCompatableClientParams) *DefaultOpenAiCompatableClient {
	return &DefaultOpenAiCompatableClient{
		apiKey:      params.ApiKey,
		apiUrl:      params.ApiUrl,
		model:       params.Model,
		restyClient: resty.New().SetTimeout(requestTimeout),
	}
}

func (c *DefaultOpenAiCompatableClient) GetResponse(request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	if c.apiUrl == "" {
		return model.ChatCompletionResponse{}, eris.New("agent: api url is not configured")
	}

	var response model.ChatCompletionResponse
	var apiError model.APIErrorResponse
	httpRequest := c.restyClient.R().
		SetResult(&response).
		SetResultError(&apiError).
		SetBody(request)
	httpRequest = httpRequest.SetHeader("x-opencode-session", uuid.NewString())
	if c.apiKey != "" {
		httpRequest = httpRequest.SetHeader("Authorization", "Bearer "+c.apiKey)
	}

	httpResponse, err := httpRequest.Post(c.apiUrl + "/chat/completions")
	if err != nil {
		return model.ChatCompletionResponse{}, eris.Wrap(err, "failed to send completion request")
	}
	if httpResponse.IsStatusFailure() {
		if apiError.Error.Message != "" {
			return model.ChatCompletionResponse{}, eris.Errorf("completion request failed: %s: %s", httpResponse.Status(), apiError.Error.Message)
		}

		return model.ChatCompletionResponse{}, eris.Errorf("completion request failed: %s", httpResponse.Status())
	}

	return response, nil
}
