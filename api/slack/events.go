package slack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-git/go-git/v5"
	"github.com/sashabaranov/go-openai"
	"github.com/slack-go/slack"

	"github.com/forgoes/aurora/api"
	"github.com/forgoes/aurora/models"
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
		ClientMsgID string `json:"client_msg_id,omitempty"`
		BotID       string `json:"bot_id,omitempty"`
		AppID       string `json:"app_id,omitempty"`
		Text        string `json:"text"`
		Team        string `json:"team"`

		BotProfile struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
			Name    string `json:"name"`
			Updated int64  `json:"updated"`
			AppID   string `json:"app_id"`
			UserID  string `json:"user_id"`
			Icons   struct {
				Image36 string `json:"image_36"`
				Image48 string `json:"image_48"`
				Image72 string `json:"image_72"`
			} `json:"icons"`
			TeamID string `json:"team_id"`
		} `json:"bot_profile,omitempty"`

		Metadata struct {
			EventType    string `json:"event_type"`
			EventPayload struct {
				ReplyToEvent string `json:"reply_to_event"`
				ReplyToUser  string `json:"reply_to_user"`
			} `json:"event_payload"`
		} `json:"metadata,omitempty"`

		Blocks []struct {
			Type     string `json:"type"`
			BlockID  string `json:"block_id"`
			Elements []struct {
				Type     string `json:"type"`
				Elements []struct {
					Type    string `json:"type"`
					Text    string `json:"text,omitempty"`
					Name    string `json:"name,omitempty"`
					Unicode string `json:"unicode,omitempty"`
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

type Credentials struct {
	AWSAccessKey    string `json:"aws_access_key"`
	AWSSecretKey    string `json:"aws_secret_key"`
	AWSSessionToken string `json:"aws_session_token"`
}

type Runtime struct {
	Port         int      `json:"port"`
	StartCommand string   `json:"start_command"`
	EnvVars      []string `json:"env_vars"`
}

type DeployRequest struct {
	Action      string      `json:"action"`
	Cloud       string      `json:"cloud"`
	Framework   string      `json:"framework"`
	RepoURL     string      `json:"repo_url"`
	Region      string      `json:"region"`
	InstanceID  string      `json:"instance_id"`
	Credentials Credentials `json:"credentials"`
	Runtime     Runtime     `json:"runtime"`
	Extra       any         `json:"extra"`
}

func checkMissingDeploy(req DeployRequest) []string {
	var missing []string

	if req.Action != "deploy" {
		return missing
	}

	if req.RepoURL == "" {
		missing = append(missing, "GitHub repository URL")
	}
	if req.Region == "" {
		missing = append(missing, "AWS region")
	}
	if req.InstanceID == "" {
		missing = append(missing, "EC2 Instance ID")
	}
	if req.Credentials.AWSAccessKey == "" ||
		req.Credentials.AWSSecretKey == "" ||
		req.Credentials.AWSSessionToken == "" {
		missing = append(missing, "AWS temporary credentials (AccessKey, SecretKey, SessionToken)")
	}

	if req.Runtime.Port == 0 {
		missing = append(missing, "App Port")
	}

	return missing
}

func generateMissingPrompt(missing []string) string {
	if len(missing) == 0 {
		return "✅ All required information is present. Ready to deploy!"
	}

	msg := "⚠️ Missing information:\n"
	for _, m := range missing {
		msg += "- " + m + "\n"
	}
	msg += "\nPlease provide the above information to continue deployment."
	return msg
}

func cloneRepo(repoURL, tmpDir string) (string, error) {
	repoPath := filepath.Join(tmpDir, fmt.Sprintf("repo-%d", time.Now().UnixNano()))

	if _, err := os.Stat(repoPath); err == nil {
		if err := os.RemoveAll(repoPath); err != nil {
			return "", fmt.Errorf("failed to remove existing repo dir: %w", err)
		}
	}

	_, err := git.PlainClone(repoPath, false, &git.CloneOptions{
		URL:      repoURL,
		Progress: os.Stdout,
	})
	if err != nil {
		return "", fmt.Errorf("failed to clone repo: %w", err)
	}
	return repoPath, nil
}

func readFileIfExists(repoPath, filename string) (string, error) {
	path := filepath.Join(repoPath, filename)
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return "", nil
	}
	return string(data), nil
}

type TerraformResponse struct {
	TerraformHCL string `json:"terraform_hcl"`
}

func analyzeRepoWithOpenAI(
	client *openai.Client,
	requirements, pkgJSON, dockerfile, readme string,
) (*RepoAnalysis, error) {
	ctx := context.Background()

	prompt := fmt.Sprintf(`
I have parts of a GitHub repository:

requirements.txt:
%s

package.json:
%s

Dockerfile:
%s

README:
%s

Task:
1) Identify deployment-relevant information from these files.
   **IMPORTANT: Carefully read README.md. Most deploy instructions (start commands, ports, env vars, build/migrate steps, docker/docker-compose usage) are usually in README.**
   Look for sections/headings like: "Deploy", "Run", "Start", "Usage", "Environment", "Configuration", ".env", "Docker", "docker-compose", "Procfile".
   Extract only what is explicitly supported by the files; do NOT invent.

2) Extract exactly these fields:
   - Framework (flask, django, nodejs, spring, etc.)
   - Start command (e.g. "gunicorn app.wsgi:application --bind 0.0.0.0:8000")
   - Port number (integer)
   - Important environment variables (list of strings; names only)

3) Conflict resolution / precedence rules:
   - Port: prefer Dockerfile EXPOSE; if absent, use README; if still unknown, return null (do NOT guess).
   - Start command: prefer explicit command in README; else Dockerfile CMD/ENTRYPOINT; else package.json "scripts.start"; if still unknown, return null.
   - Env vars: collect from README (Environment/Config/.env examples), Dockerfile ENV lines, and any clearly marked examples; deduplicate; if none, return [].
   - Framework: infer from README wording and dependency files (requirements.txt / package.json); if unclear, return "null".

4) Also prepare a field "terraform_requirements" that summarizes all information Terraform needs to generate a deployment script. If any item is unknown, set it to null or a clear placeholder (like "<AWS_ACCESS_KEY>") without inventing real values:
   - cloud provider (aws, gcp, azure)
   - region
   - repo_url
   - instance_id (if deploying to an existing EC2; otherwise null)
   - AWS temporary credentials (keys/tokens) as placeholders if missing
   - runtime details (port, start_command, env_vars)

5) put content of README in "readme" field of "terraform_requirements"

6) Output strictly as JSON (no prose, no markdown fences):
{
  "framework": "...|null",
  "start_command": "...|null",
  "port": 8000|null,
  "env_vars": ["ENV_A","ENV_B"],
  "terraform_requirements": {
    "readme": "",
    "cloud": "aws|gcp|azure|null",
    "region": "us-east-1|null",
    "repo_url": "...",
    "instance_id": null,
    "credentials": {
      "aws_access_key": "<AWS_ACCESS_KEY>",
      "aws_secret_key": "<AWS_SECRET_KEY>",
      "aws_session_token": "<AWS_SESSION_TOKEN>"
    },
    "runtime": {
      "port": 8000|null,
      "start_command": "...|null",
      "env_vars": ["ENV_A","ENV_B"]
    }
  }
}
`, requirements, pkgJSON, dockerfile, readme)

	msgs := []openai.ChatCompletionMessage{
		{
			Role:    "user",
			Content: prompt,
		},
	}

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:          "gpt-4o-mini",
		Messages:       msgs,
		ResponseFormat: &openai.ChatCompletionResponseFormat{Type: "json_object"},
		Temperature:    0,
	})
	if err != nil {
		return nil, err
	}

	var result RepoAnalysis
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return nil, err
	}

	return &result, nil
}

type RepoAnalysis struct {
	Framework    string   `json:"framework"`
	StartCommand string   `json:"start_command"`
	Port         int      `json:"port"`
	EnvVars      []string `json:"env_vars"`

	TerraformRequirements TerraformRequirements `json:"terraform_requirements"`
}

type TerraformRequirements struct {
	Cloud       string      `json:"cloud"` // aws/gcp/azure
	Region      string      `json:"region"`
	RepoURL     string      `json:"repo_url"`
	InstanceID  *string     `json:"instance_id"` // null if not provided
	Credentials Credentials `json:"credentials"`
	Runtime     Runtime     `json:"runtime"`
	Readme      string      `json:"readme"`
}

func AnalyzeRepo(client *openai.Client, repoURL string) (*RepoAnalysis, error) {
	tmpDir := os.TempDir()
	repoPath, err := cloneRepo(repoURL, tmpDir)
	if err != nil {
		return nil, err
	}

	reqs, _ := readFileIfExists(repoPath, "requirements.txt")
	pkg, _ := readFileIfExists(repoPath, "package.json")
	dock, _ := readFileIfExists(repoPath, "Dockerfile")
	rm, _ := readFileIfExists(repoPath, "README.md")

	if reqs == "" && pkg == "" && dock == "" && rm == "" {
		return nil, errors.New("no recognizable config files found")
	}

	return analyzeRepoWithOpenAI(client, reqs, pkg, dock, rm)
}

func displayDeployRequest(req DeployRequest) string {
	msg := "🚀 Deployment Plan\n"

	if req.RepoURL != "" {
		msg += fmt.Sprintf("- Repo: %s\n", req.RepoURL)
	}
	if req.Cloud != "" {
		msg += fmt.Sprintf("- Cloud: %s\n", req.Cloud)
	}
	if req.Region != "" {
		msg += fmt.Sprintf("- Region: %s\n", req.Region)
	}
	if req.InstanceID != "" {
		msg += fmt.Sprintf("- Instance: %s\n", req.InstanceID)
	}
	if req.Framework != "" {
		msg += fmt.Sprintf("- Framework: %s\n", req.Framework)
	}

	if req.Runtime.Port != 0 {
		msg += fmt.Sprintf("- Port: %d\n", req.Runtime.Port)
	}

	if len(req.Runtime.EnvVars) > 0 {
		msg += fmt.Sprintf("- Env Vars: %v\n", req.Runtime.EnvVars)
	}

	return msg
}

func GenerateTerraformWithContext(
	client *openai.Client,
	analysis *RepoAnalysis,
	evs []openai.ChatCompletionMessage,
) (*TerraformResponse, error) {
	ctx := context.Background()

	analysisJSON, _ := json.MarshalIndent(analysis.TerraformRequirements, "", "  ")

	prompt := fmt.Sprintf(`
I have a repository analysis and deployment requirements:

Repository Analysis:
- Framework: %s
- Start Command: %s
- Port: %d
- Env Vars: %v
- Readme: %v

Terraform Requirements (current context):
%s

Task:
1. Generate a Terraform configuration draft in HCL for deploying this app on AWS.
   - Use provided repo_url, region, instance_id, port, start_command, env_vars.
   - If instance_id is provided, assume deploying to that existing EC2 via SSM.
   - If not provided, create a new EC2 instance with user_data.
   - If readme is provided, use it as a reference of generate user_data.
   - Open the app port in the security group.
2. Use placeholders like <AMI_ID>, <INSTANCE_TYPE>, <AWS_ACCESS_KEY> where values are missing.
3. Provide a short natural language message telling the user what important information is still missing.
4. Output strictly as JSON:
{
  "terraform_hcl": "string containing only valid HCL",
}
`, analysis.Framework, analysis.StartCommand, analysis.Port, analysis.EnvVars, analysis.TerraformRequirements.Readme, string(analysisJSON))

	var msgs []openai.ChatCompletionMessage
	msgs = append(msgs, openai.ChatCompletionMessage{
		Role:    "user",
		Content: prompt,
	})
	msgs = append(msgs, evs...)

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:          "gpt-4o-mini",
		Messages:       msgs,
		ResponseFormat: &openai.ChatCompletionResponseFormat{Type: "json_object"},
		Temperature:    0,
	})
	if err != nil {
		return nil, err
	}

	raw := resp.Choices[0].Message.Content
	var result TerraformResponse
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, err
	}

	return &result, nil
}

type TerraformStatus struct {
	Success bool
	Output  string
	Error   string
}

// Save HCL to a temp dir
func saveTerraformHCL(hcl string, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	filePath := dir + "/main.tf"
	err := os.WriteFile(filePath, []byte(hcl), 0644)
	if err != nil {
		return "", err
	}
	return dir, nil
}

// Run terraform init + apply
func runTerraform(dir string) TerraformStatus {
	var stdout, stderr bytes.Buffer

	// terraform init
	initCmd := exec.Command("terraform", "init", "-input=false")
	initCmd.Dir = dir
	initCmd.Stdout = &stdout
	initCmd.Stderr = &stderr
	if err := initCmd.Run(); err != nil {
		return TerraformStatus{false, stdout.String(), stderr.String()}
	}

	// terraform apply
	applyCmd := exec.Command("terraform", "apply", "-auto-approve", "-input=false")
	applyCmd.Dir = dir
	applyCmd.Stdout = &stdout
	applyCmd.Stderr = &stderr
	if err := applyCmd.Run(); err != nil {
		return TerraformStatus{false, stdout.String(), stderr.String()}
	}

	return TerraformStatus{true, stdout.String(), ""}
}

func ExecuteTerraform(tfResp *TerraformResponse) TerraformStatus {
	timestamp := time.Now().Format("20060102-150405")
	dir := fmt.Sprintf("./tf-deployment-%s", timestamp)

	_, err := saveTerraformHCL(tfResp.TerraformHCL, dir)
	if err != nil {
		return TerraformStatus{
			Success: false,
			Output:  "",
			Error:   err.Error(),
		}
	}

	return runTerraform(dir)
}

func getAnswer(evs []openai.ChatCompletionMessage, c *api.Context, ev *Event) string {
	resp, err := c.Runtime.OpenAI.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:       "gpt-4o-mini",
			Messages:    evs,
			Tools:       c.Runtime.OpenAI.Tools,
			ToolChoice:  "auto",
			Temperature: 0,
		},
	)
	if err != nil {
		return "Ops, OpenAI invoking error: " + err.Error()
	}

	if len(resp.Choices) > 0 && len(resp.Choices[0].Message.ToolCalls) > 0 {
		toolCall := resp.Choices[0].Message.ToolCalls[0]

		var req DeployRequest
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &req); err != nil {
			return "JSON parse error:" + err.Error()
		}

		missing := checkMissingDeploy(req)
		if len(missing) > 0 {
			return generateMissingPrompt(missing)
		}

		analysis, err := AnalyzeRepo(c.Runtime.OpenAI.Client, req.RepoURL)
		if err != nil {
			return err.Error()
		}

		tfResp, err := GenerateTerraformWithContext(c.Runtime.OpenAI.Client, analysis, evs)
		if err != nil {
			return err.Error()
		}

		defer func() {
			go func(tr *TerraformResponse) {
				status := ExecuteTerraform(tfResp)

				if status.Success {
					_, _, _ = c.Runtime.Slack.PostMessage(
						ev.Event.Channel,
						slack.MsgOptionText("✅ Terraform apply succeeded!\n"+status.Output, false),
						slack.MsgOptionMetadata(slack.SlackMetadata{
							EventType: "bot_reply",
							EventPayload: map[string]interface{}{
								"reply_to_user":  ev.Event.User,
								"reply_to_event": ev.EventID,
							},
						}),
					)
				} else {
					_, _, _ = c.Runtime.Slack.PostMessage(
						ev.Event.Channel,
						slack.MsgOptionText("⚠️ Terraform apply failed!\n"+status.Output+"Error:"+status.Error, false),
						slack.MsgOptionMetadata(slack.SlackMetadata{
							EventType: "bot_reply",
							EventPayload: map[string]interface{}{
								"reply_to_user":  ev.Event.User,
								"reply_to_event": ev.EventID,
							},
						}),
					)
				}
			}(tfResp)

		}()

		return fmt.Sprintf(
			"%s\n\nTerraform HCL:\n```\n%s\n```",
			displayDeployRequest(req),
			tfResp.TerraformHCL,
		)

		/*
			ms := []openai.ChatCompletionMessage{
				{Role: "system", Content: "You generate short, friendly questions."},
				{Role: "user", Content: "Ask user if anything is missing."},
				{Role: "user", Content: "todo"},
			}
			ms = append(ms, evs...)

			if req.RepoURL != "" {

			} else {
				r, as, _ := AnalyzeRepo(c.Runtime.OpenAI.Client, req.RepoURL, evs)
				fmt.Println(r)
				return as
			}

			_, err = c.Runtime.OpenAI.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
				Model:    "gpt-4o-mini",
				Messages: ms,
			})

		*/
	}

	if len(resp.Choices) > 0 {
		return resp.Choices[0].Message.Content
	}

	return ""
}

func botReply(ev *Event, c *api.Context) {
	// TODO slack may receive more than one reply, need duplicate event handling
	var count int64
	c.Runtime.Mysql.Model(&models.Event{}).
		Where("reply_to_event = ? AND is_bot = ?", ev.EventID, true).
		Count(&count)
	if count > 0 {
		fmt.Println("already replied")
		return
	}

	evs := c.Runtime.OpenAI.BuildEventsHistory(ev.Event.User, ev.Event.Channel, 300)

	answer := getAnswer(evs, c, ev)

	_, _, err := c.Runtime.Slack.PostMessage(
		ev.Event.Channel,
		slack.MsgOptionText(answer, false),
		slack.MsgOptionMetadata(slack.SlackMetadata{
			EventType: "bot_reply",
			EventPayload: map[string]interface{}{
				"reply_to_user":  ev.Event.User,
				"reply_to_event": ev.EventID,
			},
		}),
	)

	if err != nil {
		fmt.Printf("error posting message: %s\n", err)
	}
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
		if ev.Event.User == "USLACKBOT" {
			return nil, nil
		}

		if ev.Event.BotID == "" {
			if ev.Event.User == "" || ev.Event.Text == "" {
				return nil, nil
			}

			// from user
			err = c.Runtime.CreateEvent(
				ev.Event.User,
				ev.Event.Channel,
				ev.APIAppID,
				ev.EventID,
				ev.Event.TS,
				ev.Event.Text,
				ev.Event.BotID != "",
				nil,
			)
			if err != nil {
				fmt.Printf("error creating event: %s\n", err)
			}

			go botReply(&ev, c)
		} else {
			// from bot
			err = c.Runtime.CreateEvent(
				ev.Event.Metadata.EventPayload.ReplyToUser,
				ev.Event.Channel,
				ev.APIAppID,
				ev.EventID,
				ev.Event.TS,
				ev.Event.Text,
				ev.Event.BotID != "",
				&ev.Event.Metadata.EventPayload.ReplyToEvent,
			)
			if err != nil {
				fmt.Printf("error creating event: %s\n", err)
			}
		}
	}

	return nil, nil
}
