package models

type Event struct {
	Model

	UserID       string  `gorm:"size:50;index;not null"`
	ChannelID    string  `gorm:"size:50;index;not null"`
	AppID        string  `gorm:"size:50;index;not null"`
	EventID      string  `gorm:"size:50;uniqueIndex;not null"`
	EventTS      string  `gorm:"size:30;index;not null"`
	Content      string  `gorm:"type:text;not null"`
	IsBot        bool    `gorm:"default:false;not null;uniqueIndex:idx_reply_bot"`
	ReplyToEvent *string `gorm:"size:50;uniqueIndex:idx_reply_bot"`
}
