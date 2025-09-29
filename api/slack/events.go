package slack

import (
	"encoding/json"
	"io"
	"log"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/forgoes/aurora/api"
	"github.com/forgoes/aurora/bot"
)

func Events(c *api.Context) (interface{}, *api.Error) {
	// Signature Verification
	body, err := io.ReadAll(c.GinCtx.Request.Body)
	if err != nil {
		return nil, api.InternalServerError()
	}

	sVerifier, err := slack.NewSecretsVerifier(c.GinCtx.Request.Header, c.Runtime.Config.Slack.Secret)
	if err != nil {
		return nil, api.StatusUnauthorizedError("signature error")
	}
	if _, err := sVerifier.Write(body); err != nil {
		return nil, api.InternalServerError("verify error")
	}
	if err := sVerifier.Ensure(); err != nil {
		return nil, api.StatusUnauthorizedError("invalid signature")
	}

	eventsAPIEvent, err := slackevents.ParseEvent(
		body,
		slackevents.OptionNoVerifyToken(),
	)
	if err != nil {
		return nil, api.InvalidArgument(nil, err.Error())
	}

	// URL Verification
	if eventsAPIEvent.Type == slackevents.URLVerification {
		var r slackevents.ChallengeResponse
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, api.InvalidArgument(nil, err.Error())
		}
		return map[string]string{"challenge": r.Challenge}, nil
	}

	// Event Callback
	if eventsAPIEvent.Type == slackevents.CallbackEvent {
		var callbackEvent slackevents.EventsAPICallbackEvent
		if err := json.Unmarshal(body, &callbackEvent); err != nil {
			return nil, api.InvalidArgument(nil, err.Error())
		}

		var inner slackevents.EventsAPIInnerEvent
		if err := json.Unmarshal(*callbackEvent.InnerEvent, &inner); err != nil {
			return nil, api.InvalidArgument(nil, err.Error())
		}

		switch slackevents.EventsAPIType(inner.Type) {
		case slackevents.Message:
			var ie slackevents.MessageEvent
			if err := json.Unmarshal(*callbackEvent.InnerEvent, &ie); err != nil {
				return nil, api.InvalidArgument(nil, err.Error())
			}

			if strings.HasPrefix(ie.Channel, "D") {
				// Handle Direct Message
				err := bot.HandleDM(c.Runtime, &callbackEvent, &ie)
				if err != nil {
					log.Println("Failed to handle DM:", err)
				}
			} else {
				// Non-DM → Send a hint
				_, _, err := c.Runtime.Slack.PostMessage(ie.Channel, slack.MsgOptionText(
					"🚧 This type of message is under development. Please DM me instead.", false,
				))
				if err != nil {
					log.Println("Failed to send hint:", err)
				}
			}
		case slackevents.AppMention:
			var ie slackevents.AppMentionEvent
			if err := json.Unmarshal(*callbackEvent.InnerEvent, &ie); err != nil {
				return nil, api.InvalidArgument(nil, err.Error())
			}

			_, _, err := c.Runtime.Slack.PostMessage(ie.Channel, slack.MsgOptionText(
				"🚧 This type of message is under development. Please DM me instead.", false,
			))
			if err != nil {
				log.Println("Failed to send hint:", err)
			}
		default:
			return map[string]string{"status": "ignored"}, nil
		}
	}

	return nil, nil
}
