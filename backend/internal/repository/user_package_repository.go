package repository

import (
	"errors"
	"math"
	"time"

	"gorm.io/gorm"

	"github.com/esportsbar/backend/internal/model"
)

// PackageDeduction 单次时长包扣费明细（一腿对应一个会员时长包）。
type PackageDeduction struct {
	UserPackageID uint
	Hours         float64
}

// UserPackageRepository 会员时长包仓储。
type UserPackageRepository struct {
	db *gorm.DB
}

// NewUserPackageRepository 构造会员时长包仓储。
func NewUserPackageRepository(db *gorm.DB) *UserPackageRepository {
	return &UserPackageRepository{db: db}
}

// Create 创建会员时长包。
func (r *UserPackageRepository) Create(up *model.UserPackage) error {
	return r.db.Create(up).Error
}

// FindByID 查询会员时长包。
func (r *UserPackageRepository) FindByID(id uint) (*model.UserPackage, error) {
	var up model.UserPackage
	err := r.db.First(&up, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &up, err
}

// LockByIDTx 事务内行锁查询会员时长包。
func (r *UserPackageRepository) LockByIDTx(tx *gorm.DB, id uint) (*model.UserPackage, error) {
	var up model.UserPackage
	err := tx.Clauses(clauseLocking()).First(&up, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &up, err
}

// FindActiveByUser 查询会员有效时长包列表。
func (r *UserPackageRepository) FindActiveByUser(userID uint) ([]model.UserPackage, error) {
	var list []model.UserPackage
	err := r.db.Where("user_id = ? AND status = ?", userID, "active").
		Order("expire_at ASC").Find(&list).Error
	return list, err
}

// ConsumeHours 扣减时长包小时数（先扣最接近过期的）。
func (r *UserPackageRepository) ConsumeHours(userID uint, hours float64) (float64, error) {
	var consumed float64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		legs, err := r.ConsumeHoursTx(tx, userID, hours)
		if err != nil {
			return err
		}
		for _, leg := range legs {
			consumed += leg.Hours
		}
		return nil
	})
	return consumed, err
}

// ConsumeHoursTx 事务内扣减时长包小时数（先扣最接近过期的），返回每个时长包的扣费明细。
// 扣费腿顺序即原扣费顺序，退费与流水记账均依赖该顺序。
func (r *UserPackageRepository) ConsumeHoursTx(tx *gorm.DB, userID uint, hours float64) ([]PackageDeduction, error) {
	var list []model.UserPackage
	if err := tx.Clauses(clauseLocking()).
		Where("user_id = ? AND status = ? AND remaining_hours > 0", userID, "active").
		Order("expire_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	legs := make([]PackageDeduction, 0)
	need := hours
	for i := range list {
		if need <= 0.0001 {
			break
		}
		up := &list[i]
		if up.ExpireAt != nil && !up.ExpireAt.After(time.Now()) {
			up.Status = "expired"
			if err := tx.Save(up).Error; err != nil {
				return nil, err
			}
			continue
		}
		use := need
		if up.RemainingHours < use {
			use = up.RemainingHours
		}
		use = roundHours(use)
		up.RemainingHours = roundHours(up.RemainingHours - use)
		need = roundHours(need - use)
		if up.RemainingHours <= 0.0001 {
			up.Status = "used"
		}
		if err := tx.Save(up).Error; err != nil {
			return nil, err
		}
		legs = append(legs, PackageDeduction{UserPackageID: up.ID, Hours: use})
	}
	return legs, nil
}

// RefundHoursTx 事务内向会员时长包返还小时数（故障中断退费）。
// 返还后时长包仍在有效期内则恢复为 active；expireAt 已过则保持 expired（由上层折算余额）。
func (r *UserPackageRepository) RefundHoursTx(tx *gorm.DB, userPackageID uint, hours float64) (*model.UserPackage, error) {
	up, err := r.LockByIDTx(tx, userPackageID)
	if err != nil {
		return nil, err
	}
	up.RemainingHours = roundHours(up.RemainingHours + hours)
	if up.ExpireAt != nil && !up.ExpireAt.After(time.Now()) {
		up.Status = "expired"
	} else if up.RemainingHours > 0.0001 && up.Status != "active" {
		up.Status = "active"
	}
	if err := tx.Save(up).Error; err != nil {
		return nil, err
	}
	return up, nil
}

// CreditHours 充值/购买后为会员时长包增加小时数。
func (r *UserPackageRepository) CreditHours(tx *gorm.DB, up *model.UserPackage) error {
	return tx.Create(up).Error
}

// roundHours 小时数保留两位小数。
func roundHours(v float64) float64 {
	return math.Round(v*100) / 100
}
