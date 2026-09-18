package repository

import (
	"gorm.io/gorm"

	"github.com/esportsbar/backend/internal/model"
)

// SessionChargeRepository 上机扣费明细仓储。
type SessionChargeRepository struct {
	db *gorm.DB
}

// NewSessionChargeRepository 构造上机扣费明细仓储。
func NewSessionChargeRepository(db *gorm.DB) *SessionChargeRepository {
	return &SessionChargeRepository{db: db}
}

// CreateTx 在指定事务内创建扣费明细。
func (r *SessionChargeRepository) CreateTx(tx *gorm.DB, charge *model.SessionCharge) error {
	return tx.Create(charge).Error
}

// ListUnrefundedBySessionTx 事务内查询上机记录尚未退还的扣费明细，按原始扣减顺序（id 升序）返回。
func (r *SessionChargeRepository) ListUnrefundedBySessionTx(tx *gorm.DB, sessionID uint) ([]model.SessionCharge, error) {
	var list []model.SessionCharge
	err := tx.Where("session_id = ? AND refunded = ?", sessionID, false).
		Order("id ASC").Find(&list).Error
	return list, err
}

// MarkRefundedTx 事务内标记一条扣费明细已退还。
func (r *SessionChargeRepository) MarkRefundedTx(tx *gorm.DB, id uint, refundKind string, amount, hours float64) error {
	return tx.Model(&model.SessionCharge{}).Where("id = ?", id).
		Updates(map[string]any{
			"refunded":       true,
			"refund_kind":    refundKind,
			"refunded_amount": amount,
			"refunded_hours": hours,
		}).Error
}

// ListBySession 查询某条上机记录的全部扣费/退还明细。
func (r *SessionChargeRepository) ListBySession(sessionID uint) ([]model.SessionCharge, error) {
	var list []model.SessionCharge
	if err := r.db.Where("session_id = ?", sessionID).Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ExistsUnrefundedBySession 查询上机记录是否存在未退还明细（用于中断幂等判断）。
func (r *SessionChargeRepository) ExistsUnrefundedBySession(sessionID uint) (bool, error) {
	var count int64
	if err := r.db.Model(&model.SessionCharge{}).
		Where("session_id = ? AND refunded = ?", sessionID, false).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
