package model

import "time"

// SessionCharge 上机扣费明细：续费/下机时每一笔余额或时长包扣减都记录一行，
// 故障中断时按这些明细的原始扣减顺序逐项回退。
type SessionCharge struct {
	ID                uint    `gorm:"primaryKey" json:"id"`
	SessionID         uint    `gorm:"index;not null" json:"session_id"`
	UserID            uint    `gorm:"index;not null" json:"user_id"`
	UserPackageID     uint    `gorm:"index;default:0" json:"user_package_id"` // 0 表示余额扣费
	PackageID         uint    `gorm:"index;default:0" json:"package_id"`
	PackageName       string  `gorm:"size:64" json:"package_name"`
	Kind              string  `gorm:"size:16;default:balance" json:"kind"` // balance/package
	Hours             float64 `gorm:"type:decimal(10,2);default:0" json:"hours"`
	Amount            float64 `gorm:"type:decimal(12,2);default:0" json:"amount"` // 该行对应的货币价值（余额扣费金额或小时折算金额）
	PricePerHour      float64 `gorm:"type:decimal(12,2);default:0" json:"price_per_hour"`
	BizType           string  `gorm:"size:32;default:session_end" json:"biz_type"` // session_renew/session_end
	PackageExpireAt   *time.Time `json:"package_expire_at"`                        // 扣费时该时长包的过期时间快照
	Refunded          bool    `gorm:"default:false" json:"refunded"`
	RefundKind        string  `gorm:"size:16;default:''" json:"refund_kind"` // balance/hours/expired_cash
	RefundedAmount    float64 `gorm:"type:decimal(12,2);default:0" json:"refunded_amount"`
	RefundedHours     float64 `gorm:"type:decimal(10,2);default:0" json:"refunded_hours"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (SessionCharge) TableName() string { return "session_charges" }
