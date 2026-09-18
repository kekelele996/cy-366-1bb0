package repository

import (
	"gorm.io/gorm"

	"github.com/esportsbar/backend/internal/model"
)

// WalletFlowRepository 会员资金/时长流水仓储。
type WalletFlowRepository struct {
	db *gorm.DB
}

// NewWalletFlowRepository 构造资金流水仓储。
func NewWalletFlowRepository(db *gorm.DB) *WalletFlowRepository {
	return &WalletFlowRepository{db: db}
}

// CreateTx 在指定事务内写入一条流水。
func (r *WalletFlowRepository) CreateTx(tx *gorm.DB, flow *model.WalletFlow) error {
	return tx.Create(flow).Error
}

// ListByUser 查询会员流水（看板/流水回读复用）。
func (r *WalletFlowRepository) ListByUser(userID uint, page, pageSize int) ([]model.WalletFlow, int64, error) {
	var list []model.WalletFlow
	var total int64
	query := r.db.Model(&model.WalletFlow{}).Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	return list, total, err
}

// ListBySession 查询某条上机记录相关流水（流水回读与看板一致）。
func (r *WalletFlowRepository) ListBySession(sessionID uint) ([]model.WalletFlow, error) {
	var list []model.WalletFlow
	err := r.db.Where("session_id = ?", sessionID).Order("id ASC").Find(&list).Error
	return list, err
}
