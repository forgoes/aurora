package slack

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"

	"github.com/forgoes/aurora/api"
)

type ChallengeRequest struct {
	Token     string `json:"token"`
	Challenge string `json:"challenge"`
	Type      string `json:"type"`
}

type Event struct {
	Token               string  `json:"token"`
	TeamID              string  `json:"team_id"`
	ContextTeamID       string  `json:"context_team_id"`
	ContextEnterpriseID *string `json:"context_enterprise_id"`
	APIAppID            string  `json:"api_app_id"`
	Event               struct {
		User        string `json:"user"`
		Type        string `json:"type"`
		TS          string `json:"ts"`
		ClientMsgID string `json:"client_msg_id"`
		Text        string `json:"text"`
		Team        string `json:"team"`
		Blocks      []struct {
			Type     string `json:"type"`
			BlockID  string `json:"block_id"`
			Elements []struct {
				Type     string `json:"type"`
				Elements []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"elements"`
			} `json:"elements"`
		} `json:"blocks"`
		Channel     string `json:"channel"`
		EventTS     string `json:"event_ts"`
		ChannelType string `json:"channel_type"`
	} `json:"event"`
	Type           string `json:"type"`
	EventID        string `json:"event_id"`
	EventTime      int64  `json:"event_time"`
	Authorizations []struct {
		EnterpriseID        *string `json:"enterprise_id"`
		TeamID              string  `json:"team_id"`
		UserID              string  `json:"user_id"`
		IsBot               bool    `json:"is_bot"`
		IsEnterpriseInstall bool    `json:"is_enterprise_install"`
	} `json:"authorizations"`
	IsExtSharedChannel bool   `json:"is_ext_shared_channel"`
	EventContext       string `json:"event_context"`
}

func verifySlackRequest(c *gin.Context, signingSecret string, body []byte) bool {
	timestamp := c.Request.Header.Get("X-Slack-Request-Timestamp")
	slackSig := c.Request.Header.Get("X-Slack-Signature")

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}

	if time.Now().Unix()-ts > 60*5 {
		return false
	}

	sigBase := "v0:" + timestamp + ":" + string(body)

	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(sigBase))
	expectedSig := "v0=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expectedSig), []byte(slackSig))
}

func Events(c *api.Context) (interface{}, *api.Error) {
	body, err := io.ReadAll(c.GinCtx.Request.Body)
	if err != nil {
		return nil, api.InternalServerError()
	}
	c.GinCtx.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	if !verifySlackRequest(c.GinCtx, c.Runtime.Config.Slack.Secret, body) {
		return nil, api.StatusUnauthorizedError("invalid signature")
	}

	var req ChallengeRequest
	if err := json.Unmarshal(body, &req); err == nil && req.Type == "url_verification" {
		return map[string]string{"challenge": req.Challenge}, nil
	}

	var ev Event
	if err := json.Unmarshal(body, &ev); err == nil {
		fmt.Println("Got message:", ev.Event.Text, "from user:", ev.Event.User, "in channel:", ev.Event.Channel,
			"Event ID:", ev.EventID)
	}

	go func() {
		botUserID := ev.Authorizations[0].UserID

		if ev.Event.User == botUserID {
			fmt.Println("Ignoring bot's own message")
			return
		}

		_, _, err = c.Runtime.Slack.PostMessage(
			ev.Event.Channel,
			slack.MsgOptionText(fmt.Sprintf("Hello from Go Slack Bot! 🚀, %s, %s", ev.Event.Text, ev.EventID), false),
		)
		if err != nil {
			fmt.Printf("error posting message: %s\n", err)
		}
	}()

	return nil, nil
}
