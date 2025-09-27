package runtime

import (
	"context"

	"gorm.io/gorm"
)

type Runtime struct {
	Flags  *Flags
	Config *Config
	Mysql  *gorm.DB
	Slack  *Slack
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

	s, err := NewSlack(config)
	if err != nil {
		return nil, err
	}
	rt.Slack = s
	
	/*
		db, err := initMysql(&config.Mysql)
		if err != nil {
			return nil, err
		}
		rt.Mysql = db
	*/

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
