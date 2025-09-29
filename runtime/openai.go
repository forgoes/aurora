package runtime

import (
	"github.com/sashabaranov/go-openai"
)

type OpenAI struct {
	*openai.Client
	*Runtime
}

func initOpenAI(rt *Runtime) (*OpenAI, error) {
	client := openai.NewClient(rt.Config.OpenAI.ApiKey)

	return &OpenAI{
		client,
		rt,
	}, nil
}
