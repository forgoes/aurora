package runtime

import (
	"os"

	"github.com/pelletier/go-toml/v2"
)

type Mode struct {
	Debug bool
}

type HTTP struct {
	Host string
	Port int
	TLS  bool
	Crt  string
	Key  string
}

type MysqlConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DB       string
	MaxOpen  int
	MaxIdle  int
	MaxLife  int
	Migrate  bool
}

type RedisConfig struct {
	Address    string
	User       string
	Password   string
	DB         int
	MaxRetries int
	PoolSize   int
	MinIdle    int
}

type EmailConfig struct {
	Host     string
	Port     int
	From     string
	Password string
	Template string
}

type JWT struct {
	Expire int
	Key    string
}

type SlackConfig struct {
	Secret string
	Token  string
}

type OpenAIConfig struct {
	ApiKey string
}

type Config struct {
	Mode   Mode
	HTTP   HTTP
	Mysql  MysqlConfig
	Redis  RedisConfig
	Email  EmailConfig
	Jwt    JWT
	Slack  SlackConfig
	OpenAI OpenAIConfig
}

func loadConfig(configFile string) (*Config, error) {
	config := &Config{}

	content, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	err = toml.Unmarshal(content, config)
	if err != nil {
		return nil, err
	}

	return config, nil
}
