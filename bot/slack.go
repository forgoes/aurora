package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/sashabaranov/go-openai"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/forgoes/aurora/models"
	"github.com/forgoes/aurora/runtime"
)

var tools = []openai.Tool{
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
	Credentials Credentials `json:"credentials"`
	Runtime     Runtime     `json:"runtime"`
	Extra       any         `json:"extra"`
}

type TerraformRequirements struct {
	Cloud       string      `json:"cloud"` // aws/gcp/azure
	Region      string      `json:"region"`
	RepoURL     string      `json:"repo_url"`
	Credentials Credentials `json:"credentials"`
	Runtime     Runtime     `json:"runtime"`
	Readme      string      `json:"readme"`
}

type RepoAnalysis struct {
	Framework    string   `json:"framework"`
	StartCommand string   `json:"start_command"`
	Port         int      `json:"port"`
	EnvVars      []string `json:"env_vars"`

	TerraformRequirements TerraformRequirements `json:"terraform_requirements"`
	Summary               string                `json:"summary"`
}

type TerraformResponse struct {
	TerraformHCL string `json:"terraform_hcl"`
}

type TerraformStatus struct {
	Success bool
	Output  string
	Error   string
	JSON    interface{}
	URL     string
}

func save(db *gorm.DB, ev *slackevents.EventsAPICallbackEvent, ie *slackevents.MessageEvent) error {
	msg := &models.Message{
		Channel: ie.Channel,
		EventID: ev.EventID,
		Content: ie.Text,
		EventTS: ie.TimeStamp,
		IsBot:   ie.BotID != "",
	}

	if ie.User != "" {
		msg.UserID = &ie.User
	}
	if ie.BotID != "" {
		msg.BotID = &ie.BotID
	}

	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "event_id"}},
		DoNothing: true,
	}).Create(msg).Error
}

func shouldProcessMessage(ie *slackevents.MessageEvent) bool {
	// do not reply to all bots
	if ie.SubType == "bot_message" || ie.BotID != "" {
		return false
	}

	// do not reply to system messages
	if ie.SubType != "" {
		return false
	}

	return true
}

func saveMessage(r *runtime.Runtime, ev *slackevents.EventsAPICallbackEvent, ie *slackevents.MessageEvent, bot string) error {
	// from user
	if ie.SubType == "" && ie.User != "" && ie.BotID == "" {
		if err := save(r.Mysql, ev, ie); err != nil {
			return err
		}
	}

	// from the bot itself
	if ie.BotID == bot {
		if err := save(r.Mysql, ev, ie); err != nil {
			return err
		}
	}

	return nil
}

func buildHistory(r *runtime.Runtime, channel string, count int) []openai.ChatCompletionMessage {
	var msgs []models.Message

	subQuery := r.Mysql.Model(&models.Message{}).
		Where("channel = ? AND content IS NOT NULL AND content != ''", channel).
		Order("event_ts desc").
		Limit(count)

	r.Mysql.Table("(?) as m", subQuery).
		Order("event_ts asc").
		Find(&msgs)

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

func replySlack(r *runtime.Runtime, ie *slackevents.MessageEvent, answer string) {
	_, _, err := r.Slack.PostMessage(
		ie.Channel,
		slack.MsgOptionText(answer, false),
	)

	if err != nil {
		fmt.Printf("error posting message: %s\n", err)
	}
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
	if req.Credentials.AWSAccessKey == "" ||
		req.Credentials.AWSSecretKey == "" {
		missing = append(missing, "AWS temporary credentials (AccessKey, SecretKey)")
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

func analyzeRepo(client *openai.Client, repoURL string) (*RepoAnalysis, error) {
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

6) Also prepare a concise natural language summary of the repo analysis in a new field "summary".

7) Output strictly as JSON (no prose, no markdown fences):
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
  },
  "summary": "This repo is a Node.js web app with a Dockerfile, starts via npm start, exposes port 3000, and uses ENV_DB_URL as config."
}
`, reqs, pkg, dock, rm)

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

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func runTerraform(dir string, noColor bool, asJSON bool) TerraformStatus {
	var stdout, stderr bytes.Buffer

	// terraform init
	initArgs := []string{"init", "-input=false"}
	if noColor {
		initArgs = append(initArgs, "-no-color")
	}
	initCmd := exec.Command("terraform", initArgs...)
	initCmd.Dir = dir
	initCmd.Stdout = &stdout
	initCmd.Stderr = &stderr
	if err := initCmd.Run(); err != nil {
		return TerraformStatus{
			Success: false,
			Output:  clean(stdout.String(), noColor),
			Error:   clean(stderr.String(), noColor),
		}
	}

	// terraform apply
	stdout.Reset()
	stderr.Reset()
	applyArgs := []string{"apply", "-auto-approve", "-input=false"}
	if noColor {
		applyArgs = append(applyArgs, "-no-color")
	}
	if asJSON {
		applyArgs = append(applyArgs, "-json")
	}
	applyCmd := exec.Command("terraform", applyArgs...)
	applyCmd.Dir = dir
	applyCmd.Stdout = &stdout
	applyCmd.Stderr = &stderr
	if err := applyCmd.Run(); err != nil {
		return TerraformStatus{
			Success: false,
			Output:  clean(stdout.String(), noColor),
			Error:   clean(stderr.String(), noColor),
		}
	}

	status := TerraformStatus{
		Success: true,
		Output:  clean(stdout.String(), noColor),
		Error:   clean(stderr.String(), noColor),
	}

	// terraform output -json
	var out bytes.Buffer
	outCmd := exec.Command("terraform", "output", "-json")
	outCmd.Dir = dir
	outCmd.Stdout = &out
	if err := outCmd.Run(); err == nil {
		var tfOut map[string]struct {
			Value interface{} `json:"value"`
		}
		if err := json.Unmarshal(out.Bytes(), &tfOut); err == nil {
			ip, ok1 := tfOut["public_ip"].Value.(string)
			port, ok2 := tfOut["port"].Value.(float64)
			if ok1 && ok2 {
				status.URL = fmt.Sprintf("http://%s:%d", ip, int(port))
			}
			status.JSON = tfOut
		}
	}

	return status
}
func clean(s string, noColor bool) string {
	if noColor {
		return ansi.ReplaceAllString(s, "")
	}
	return s
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

	return runTerraform(dir, true, false)
}

func generateTerraformWithContext(
	client *openai.Client,
	analysis *RepoAnalysis,
	evs []openai.ChatCompletionMessage,
) (*TerraformResponse, error) {
	ctx := context.Background()

	analysisJSON, _ := json.MarshalIndent(analysis.TerraformRequirements, "", "  ")

	prompt := fmt.Sprintf(`I have a repository analysis and deployment requirements:

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
   - If readme is provided, use it as a reference to generate user_data.
   - Open the app port in the security group.
   - Also open port 22 for SSH access in the security group.
   - Security group must use "name_prefix" instead of "name" to avoid duplicate name conflicts in VPC.
   - Security group egress MUST allow all outbound traffic. **If protocol = "-1" (all protocols), then from_port=0 and to_port=0 must be used.**
   - In user_data:
     * installation and run commands as root.
     * Always run "apt-get -o Acquire::ForceIPv4=true update -y" before any install to avoid IPv6 repo issues.
     * Ensure "apt-get install -y python3 python3-pip python3-venv git" is included if python/pip/git are missing.
     * Default to python3 and pip3 for virtual environments, installs, and app execution.
     * Run the app as "nohup python3 app.py > app.log 2>&1 &" so it persists and logs output.
   - Ensure Terraform outputs:
     * “public_ip” = instance public IP
     * “app_port” = application port (use a Terraform variable, e.g. “var.app_port”, not an aws_instance attribute)
     * “app_url”  = "http://${aws_instance.<name>.public_ip}:${var.app_port}" (use HCL interpolation)

	2. Use placeholders like <AMI_ID>, <INSTANCE_TYPE>, <AWS_ACCESS_KEY> where values are missing.

	3. Provide a short natural language message telling the user what important information is still missing.

	4. The ONLY valid output is JSON, without any explanation, prose, or Markdown fences:
	{
		"terraform_hcl": "string containing only valid HCL"
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

func deploy(r *runtime.Runtime, ev *slackevents.EventsAPICallbackEvent, ie *slackevents.MessageEvent, req *DeployRequest, evs []openai.ChatCompletionMessage) {
	replySlack(r, ie, fmt.Sprintf("Analyzing repository..."))
	analysis, err := analyzeRepo(r.OpenAI.Client, req.RepoURL)
	if err != nil {
		replySlack(r, ie, fmt.Sprintf("deploy failed: %s\n", err.Error()))
		return
	}
	replySlack(r, ie, fmt.Sprintf("%s\n", analysis.Summary))

	tfResp, err := generateTerraformWithContext(r.OpenAI.Client, analysis, evs)
	if err != nil {
		replySlack(r, ie, fmt.Sprintf("deploy failed: %s\n", err.Error()))
		return
	}
	replySlack(r, ie, fmt.Sprintf("HCL generated:\n```%s```\n🚀 Deploying", tfResp.TerraformHCL))

	status := ExecuteTerraform(tfResp)
	if status.Success {
		replySlack(r, ie, fmt.Sprintf("✅ Terraform apply succeeded!\n%s\n website url: %s\n", status.Output, status.URL))
	} else {
		replySlack(r, ie, "⚠️ Terraform apply failed!\n"+status.Output+"Error:"+status.Error)
	}
}

func process(r *runtime.Runtime, ev *slackevents.EventsAPICallbackEvent, ie *slackevents.MessageEvent, count int) {
	evs := buildHistory(r, ie.Channel, count)

	resp, err := r.OpenAI.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:       "gpt-4o-mini",
			Messages:    evs,
			Tools:       tools,
			ToolChoice:  "auto",
			Temperature: 0,
		},
	)
	if err != nil {
		fmt.Printf("error processing message: %s\n", err)
		return
	}

	if len(resp.Choices) <= 0 {
		replySlack(r, ie, "sorry, I got nothing from the AI.")
		fmt.Printf("error processing message: %s\n", err)
		return
	}

	if len(resp.Choices[0].Message.ToolCalls) <= 0 {
		replySlack(r, ie, resp.Choices[0].Message.Content)
		fmt.Printf("error processing message: %s\n", err)
		return
	}

	// tool call
	toolCall := resp.Choices[0].Message.ToolCalls[0]

	var req DeployRequest
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &req); err != nil {
		fmt.Printf("error processing message: %s\n", err)
		return
	}

	missing := checkMissingDeploy(req)
	if len(missing) > 0 {
		replySlack(r, ie, generateMissingPrompt(missing))
		return
	}

	replySlack(r, ie, fmt.Sprintf("%s\n", displayDeployRequest(req)))

	deploy(r, ev, ie, &req, evs)
}

func HandleDM(r *runtime.Runtime, ev *slackevents.EventsAPICallbackEvent, ie *slackevents.MessageEvent) error {
	err := saveMessage(r, ev, ie, r.Config.Slack.Bot)
	if err != nil {
		return err
	}

	// TODO handle duplicated message
	if !shouldProcessMessage(ie) {
		return nil
	}

	go process(r, ev, ie, 200)

	return nil
}
