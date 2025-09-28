package runtime

import (
	"github.com/slack-go/slack"
)

type Slack struct {
	*slack.Client
}

func initSlack(config *Config) (*Slack, error) {
	cli := slack.New(config.Slack.Token)

	return &Slack{
		Client: cli,
	}, nil
}
