package runtime

import (
	"github.com/sashabaranov/go-openai"

	"github.com/forgoes/aurora/models"
)

type OpenAI struct {
	*openai.Client
	*Runtime
	Tools []openai.Tool
}

func initOpenAI(rt *Runtime) (*OpenAI, error) {
	client := openai.NewClient(rt.Config.OpenAI.ApiKey)

	return &OpenAI{
		client,
		rt,
		[]openai.Tool{
			{
				Type: "function",
				Function: &openai.FunctionDefinition{
					Name:        "parse_deploy_request",
					Description: "Parse user intent to deploy apps, extracting all required deployment parameters.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"action": map[string]interface{}{
								"type": "string",
								"enum": []string{"deploy", "stop", "status", "scale"},
							},
							"cloud": map[string]interface{}{
								"type": "string",
								"enum": []string{"aws", "gcp", "azure", "null"},
							},
							"framework": map[string]interface{}{
								"type": "string",
								"enum": []string{"django", "flask", "nodejs", "spring", "null"},
							},
							"repo_url":    map[string]interface{}{"type": "string"},
							"region":      map[string]interface{}{"type": "string"},
							"instance_id": map[string]interface{}{"type": "string"},
							"credentials": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"aws_access_key":    map[string]interface{}{"type": "string"},
									"aws_secret_key":    map[string]interface{}{"type": "string"},
									"aws_session_token": map[string]interface{}{"type": "string"},
								},
							},
							"runtime": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"port":          map[string]interface{}{"type": "integer"},
									"start_command": map[string]interface{}{"type": "string"},
									"env_vars":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								},
							},
							"extra": map[string]interface{}{"type": "object"},
						},
						"required": []string{"action"},
					},
				},
			},
		},
	}, nil
}

func (o *OpenAI) BuildEventsHistory(userID, channelID string, count int) []openai.ChatCompletionMessage {
	var msgs []models.Event
	o.Runtime.Mysql.Where("user_id = ? AND channel_id = ? AND content IS NOT NULL AND content != ''",
		userID, channelID).
		Order("event_ts desc").
		Limit(count).
		Find(&msgs)

	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	var history []openai.ChatCompletionMessage
	for _, m := range msgs {
		role := "user"
		if m.IsBot {
			role = "assistant"
		}
		history = append(history, openai.ChatCompletionMessage{
			Role:    role,
			Content: m.Content,
		})
	}
	return history
}
