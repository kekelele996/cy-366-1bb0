package repository

import (
	"errors"
	"math"

	"gorm.io/gorm"

	"github.com/esportsbar/backend/internal/model"
)

// 仓储层哨兵错误。
var (
	ErrNotFound = errors.New("record not found")
	ErrConflict = errors.New("record conflict")
)

// UserRepository 用户仓储。
type UserRepository struct {
	db *gorm.DB
}

// NewUserRepository 构造用户仓储。
func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Create 创建用户。
func (r *UserRepository) Create(u *model.User) error {
	return r.db.Create(u).Error
}

// FindByUsername 按用户名查询用户。
func (r *UserRepository) FindByUsername(username string) (*model.User, error) {
	var u model.User
	err := r.db.Where("username = ?", username).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &u, err
}

// FindByID 按 ID 查询用户。
func (r *UserRepository) FindByID(id uint) (*model.User, error) {
	var u model.User
	err := r.db.First(&u, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &u, err
}

// Update 更新用户。
func (r *UserRepository) Update(u *model.User) error {
	return r.db.Save(u).Error
}

// Delete 删除用户。
func (r *UserRepository) Delete(id uint) error {
	return r.db.Delete(&model.User{}, id).Error
}

// List 分页查询用户。
func (r *UserRepository) List(page, pageSize int) ([]model.User, int64, error) {
	var users []model.User
	var total int64
	query := r.db.Model(&model.User{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&users).Error
	return users, total, err
}

// UpdateBalance 更新余额（扣款时校验余额充足）。
func (r *UserRepository) UpdateBalance(userID uint, delta float64) error {
	_, err := r.AdjustBalanceTx(nil, userID, delta)
	return err
}

// AdjustBalanceTx 事务内行锁调整会员余额，返回变动后的余额。
// tx 为 nil 时在独立事务内执行。delta 为负表示扣款，余额不足返回 ErrConflict。
func (r *UserRepository) AdjustBalanceTx(tx *gorm.DB, userID uint, delta float64) (float64, error) {
	run := func(db *gorm.DB) (float64, error) {
		var u model.User
		if err := db.Clauses(clauseLocking()).First(&u, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, ErrNotFound
			}
			return 0, err
		}
		if roundMoney(u.Balance+delta) < 0 {
			return 0, ErrConflict
		}
		newBalance := roundMoney(u.Balance + delta)
		if err := db.Model(&model.User{}).Where("id = ?", userID).Update("balance", newBalance).Error; err != nil {
			return 0, err
		}
		return newBalance, nil
	}
	if tx != nil {
		return run(tx)
	}
	var balance float64
	err := r.db.Transaction(func(inner *gorm.DB) error {
		var err error
		balance, err = run(inner)
		return err
	})
	return balance, err
}

// roundMoney 金额保留两位小数，避免浮点尾差导致余额不平。
func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}
