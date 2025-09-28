package runtime

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/forgoes/aurora/models"
)

type Runtime struct {
	Flags  *Flags
	Config *Config
	Mysql  *gorm.DB
	Slack  *Slack
	OpenAI *OpenAI
}

func New() (*Runtime, error) {
	rt := &Runtime{}

	flags, err := parseFlags()
	if err != nil {
		return nil, err
	}
	rt.Flags = flags

	config, err := loadConfig(flags.ConfigFile)
	if err != nil {
		return nil, err
	}
	rt.Config = config

	s, err := initSlack(config)
	if err != nil {
		return nil, err
	}
	rt.Slack = s

	db, err := initMysql(&config.Mysql)
	if err != nil {
		return nil, err
	}
	rt.Mysql = db

	oi, err := initOpenAI(rt)
	if err != nil {
		return nil, err
	}
	rt.OpenAI = oi

	return rt, nil
}

func (r *Runtime) Close(ctx context.Context) error {
	// TODO ctx
	defer ctx.Done()

	sqlDB, err := r.Mysql.DB()
	if err != nil {
		return err
	}
	if err := sqlDB.Close(); err != nil {
		return err
	}

	return nil
}

func (r *Runtime) CreateEvent(userID, channelID, appID, eventID, eventTS, content string, isBot bool, replyToEvent *string) error {
	msg := models.Event{
		UserID:       userID,
		ChannelID:    channelID,
		AppID:        appID,
		EventID:      eventID,
		EventTS:      eventTS,
		Content:      content,
		IsBot:        isBot,
		ReplyToEvent: replyToEvent,
	}

	return r.Mysql.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "event_id"}},
		DoNothing: true,
	}).Create(&msg).Error
}
