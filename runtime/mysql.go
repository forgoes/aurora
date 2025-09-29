package runtime

import (
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/forgoes/aurora/models"
)

func initMysql(config *MysqlConfig) (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=%t&loc=%s",
		config.User, config.Password, config.Host, config.Port, config.DB, true, "Local",
	)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if config.MaxOpen > 0 {
		sqlDB.SetMaxOpenConns(config.MaxOpen)
	}
	if config.MaxIdle > 0 {
		sqlDB.SetMaxIdleConns(config.MaxIdle)
	}
	if config.MaxLife > 0 {
		sqlDB.SetConnMaxLifetime(time.Second * time.Duration(config.MaxLife))
	}
	if config.Migrate {
		if err := db.AutoMigrate(
			&models.Message{},
		); err != nil {
			return nil, err
		}
	}
	return db, nil
}
