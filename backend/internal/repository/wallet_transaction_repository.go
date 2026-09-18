package repository

import (
	"errors"

	"gorm.io/gorm"

	"github.com/esportsbar/backend/internal/constants"
	"github.com/esportsbar/backend/internal/model"
)

// WalletTransactionRepository 会员资金流水仓储（余额与时长包小时统一流水）。
type WalletTransactionRepository struct {
	db *gorm.DB
}

// NewWalletTransactionRepository 构造资金流水仓储。
func NewWalletTransactionRepository(db *gorm.DB) *WalletTransactionRepository {
	return &WalletTransactionRepository{db: db}
}

// CreateTx 事务内写入一条流水。
func (r *WalletTransactionRepository) CreateTx(tx *gorm.DB, wt *model.WalletTransaction) error {
	return tx.Create(wt).Error
}

// Create 写入一条流水（独立事务，仅供非事务场景兜底）。
func (r *WalletTransactionRepository) Create(wt *model.WalletTransaction) error {
	return r.db.Create(wt).Error
}

// ListConsumeBySessionTx 事务内回读某次上机的全部消费扣费流水（余额腿+时长包腿），按原扣费顺序（id 升序）返回。
func (r *WalletTransactionRepository) ListConsumeBySessionTx(tx *gorm.DB, sessionID uint) ([]model.WalletTransaction, error) {
	var list []model.WalletTransaction
	err := tx.Where("session_id = ? AND change_type = ?", sessionID, constants.WalletChangeConsume).
		Order("id ASC").Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// ExistsRefundBySessionTx 判断该上机是否已生成过退费流水（重复中断结算的二次防护）。
func (r *WalletTransactionRepository) ExistsRefundBySessionTx(tx *gorm.DB, sessionID uint) (bool, error) {
	var count int64
	err := tx.Model(&model.WalletTransaction{}).
		Where("session_id = ? AND change_type IN ?", sessionID, []string{
			constants.WalletChangeRefundBalance,
			constants.WalletChangeRefundPackage,
			constants.WalletChangeExpiredToBalance,
		}).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListByUser 分页查询会员资金流水（看板/明细回读）。
func (r *WalletTransactionRepository) ListByUser(userID uint, page, pageSize int) ([]model.WalletTransaction, int64, error) {
	var list []model.WalletTransaction
	var total int64
	query := r.db.Model(&model.WalletTransaction{}).Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, 0, err
	}
	return list, total, nil
}
