package model

import "time"

// WalletFlow 会员资金/时长流水：余额变动与时长包小时变动统一记账，
// 故障中断的费用回补与机位状态、上机结算在同一事务内落库，保证看板与流水回读一致。
type WalletFlow struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	UserID        uint      `gorm:"index;not null" json:"user_id"`
	SessionID     uint      `gorm:"index;default:0" json:"session_id"`
	UserPackageID uint      `gorm:"index;default:0" json:"user_package_id"`
	Direction     string    `gorm:"size:16;not null" json:"direction"` // deduct/refund
	Kind          string    `gorm:"size:16;not null" json:"kind"`      // balance/package_hours
	BizType       string    `gorm:"size:32;not null" json:"biz_type"`  // session_renew/session_end/session_interrupt_refund
	Amount        float64   `gorm:"type:decimal(12,2);default:0" json:"amount"`
	Hours         float64   `gorm:"type:decimal(10,2);default:0" json:"hours"`
	Remark        string    `gorm:"size:255;default:''" json:"remark"`
	CreatedAt     time.Time `json:"created_at"`
}

// TableName 指定表名。
func (WalletFlow) TableName() string { return "wallet_flows" }
