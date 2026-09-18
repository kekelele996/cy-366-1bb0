package model

import "time"

// WalletTransaction 会员钱包流水：余额变动与时长包小时变动统一记账。
// 上机扣费按 session_id 串联，故障中断结算依赖流水回读重算。
type WalletTransaction struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	UserID        uint      `gorm:"index;not null" json:"user_id"`
	ChangeType    string    `gorm:"size:32;not null;index" json:"change_type"`
	AccountType   string    `gorm:"size:16;not null" json:"account_type"` // balance / package
	Direction     string    `gorm:"size:8;not null" json:"direction"`     // debit 出账 / credit 入账
	Amount        float64   `gorm:"type:decimal(12,2);not null;default:0" json:"amount"`
	Hours         float64   `gorm:"type:decimal(10,2);not null;default:0" json:"hours"`
	UserPackageID uint      `gorm:"index;default:0" json:"user_package_id"`
	SessionID     uint      `gorm:"index;default:0" json:"session_id"`
	RelatedID     uint      `gorm:"index;default:0" json:"related_id"`
	BalanceAfter  float64   `gorm:"type:decimal(12,2);default:0" json:"balance_after"`
	Remark        string    `gorm:"size:255;default:''" json:"remark"`
	CreatedAt     time.Time `gorm:"index" json:"created_at"`
}

// TableName 指定表名。
func (WalletTransaction) TableName() string { return "wallet_transactions" }
