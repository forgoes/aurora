package models

type Message struct {
	Model

	Channel string  `gorm:"size:50;index;not null"`
	UserID  *string `gorm:"size:50;index" json:",omitempty"`
	BotID   *string `gorm:"size:50;index" json:",omitempty"`
	EventID string  `gorm:"size:50;uniqueIndex;not null"`
	Content string  `gorm:"type:text"`
	EventTS string  `gorm:"size:30;index;not null"`
	IsBot   bool
}
