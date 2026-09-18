package service

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/esportsbar/backend/internal/constants"
	"github.com/esportsbar/backend/internal/dto"
	"github.com/esportsbar/backend/internal/model"
	"github.com/esportsbar/backend/internal/repository"
	"github.com/esportsbar/backend/internal/util"
	"gorm.io/gorm"
)

// RechargeService 充值与时长包的服务。
type RechargeService struct {
	userRepo     *repository.UserRepository
	rechargeRepo *repository.RechargeRepository
	packageRepo  *repository.TimePackageRepository
	userPkgRepo  *repository.UserPackageRepository
	walletRepo   *repository.WalletTransactionRepository
	orderRepo    *repository.PackageOrderRepository
	db           *gorm.DB
	logger       *slog.Logger
}

// NewRechargeService 构造充值服务。
func NewRechargeService(
	userRepo *repository.UserRepository,
	rechargeRepo *repository.RechargeRepository,
	packageRepo *repository.TimePackageRepository,
	userPkgRepo *repository.UserPackageRepository,
	walletRepo *repository.WalletTransactionRepository,
	orderRepo *repository.PackageOrderRepository,
	db *gorm.DB,
	logger *slog.Logger,
) *RechargeService {
	return &RechargeService{
		userRepo:     userRepo,
		rechargeRepo: rechargeRepo,
		packageRepo:  packageRepo,
		userPkgRepo:  userPkgRepo,
		walletRepo:   walletRepo,
		orderRepo:    orderRepo,
		db:           db,
		logger:       logger,
	}
}

// Recharge 会员充值：事务内更新余额、写充值记录与资金流水。
func (s *RechargeService) Recharge(req *dto.RechargeReq, operatorID uint) error {
	if _, err := s.userRepo.FindByID(req.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return util.NewAppError(constants.CodeUserNotFound, "会员不存在，无法充值")
		}
		return fmt.Errorf("recharge find user: %w", err)
	}
	rc := &model.Recharge{
		UserID:        req.UserID,
		Amount:        req.Amount,
		PaymentMethod: req.PaymentMethod,
		OperatorID:    operatorID,
		Remark:        req.Remark,
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		newBalance, err := s.userRepo.AdjustBalanceTx(tx, req.UserID, req.Amount)
		if err != nil {
			if errors.Is(err, repository.ErrConflict) {
				return util.NewAppError(constants.CodeConflict, "会员余额状态异常")
			}
			return fmt.Errorf("recharge update balance: %w", err)
		}
		if err := tx.Create(rc).Error; err != nil {
			return fmt.Errorf("recharge create record: %w", err)
		}
		wt := &model.WalletTransaction{
			UserID:       req.UserID,
			ChangeType:   constants.WalletChangeRecharge,
			AccountType:  constants.WalletAccountBalance,
			Direction:    constants.WalletDirCredit,
			Amount:       req.Amount,
			RelatedID:    rc.ID,
			BalanceAfter: newBalance,
			Remark:       fmt.Sprintf("会员充值（%s）", req.PaymentMethod),
		}
		if err := s.walletRepo.CreateTx(tx, wt); err != nil {
			return fmt.Errorf("recharge ledger: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["recharge_create_ok"], req.UserID, req.Amount, req.PaymentMethod))
	return nil
}

// BuyPackage 购买时长包：事务内扣款、创建订单、发放时长并写资金流水。
func (s *RechargeService) BuyPackage(userID uint, req *dto.BuyPackageReq) (*model.PackageOrder, error) {
	pkg, err := s.packageRepo.FindByID(req.PackageID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, "时长包不存在")
		}
		return nil, fmt.Errorf("buy package find package: %w", err)
	}
	if pkg.Status != "active" {
		return nil, util.NewAppError(constants.CodeConflict, "时长包已下架，无法购买")
	}
	order := &model.PackageOrder{
		OrderNo:       genOrderNo(),
		UserID:        userID,
		PackageID:     pkg.ID,
		PackageName:   pkg.Name,
		Amount:        pkg.Price,
		Hours:         pkg.Hours,
		PaymentMethod: req.PaymentMethod,
		Status:        constants.OrderPaid,
	}
	expireAt := time.Now().AddDate(0, 0, pkg.ValidDays)
	up := &model.UserPackage{
		UserID:         userID,
		PackageID:      pkg.ID,
		PackageName:    pkg.Name,
		TotalHours:     pkg.Hours,
		RemainingHours: pkg.Hours,
		ExpireAt:       &expireAt,
		Status:         "active",
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if req.PaymentMethod == constants.PaymentBalance {
			newBalance, err := s.userRepo.AdjustBalanceTx(tx, userID, -pkg.Price)
			if err != nil {
				if errors.Is(err, repository.ErrConflict) {
					return util.NewAppError(constants.CodeInsufficient, "会员余额不足，请先充值")
				}
				return fmt.Errorf("buy package deduct balance: %w", err)
			}
			if err := s.walletRepo.CreateTx(tx, &model.WalletTransaction{
				UserID:       userID,
				ChangeType:   constants.WalletChangeBuyPackage,
				AccountType:  constants.WalletAccountBalance,
				Direction:    constants.WalletDirDebit,
				Amount:       -pkg.Price,
				BalanceAfter: newBalance,
				Remark:       fmt.Sprintf("购买时长包「%s」", pkg.Name),
			}); err != nil {
				return fmt.Errorf("buy package balance ledger: %w", err)
			}
		}
		if err := tx.Create(order).Error; err != nil {
			return fmt.Errorf("buy package create order: %w", err)
		}
		if err := s.userPkgRepo.CreditHours(tx, up); err != nil {
			return fmt.Errorf("buy package credit hours: %w", err)
		}
		if err := s.walletRepo.CreateTx(tx, &model.WalletTransaction{
			UserID:        userID,
			ChangeType:    constants.WalletChangeBuyPackage,
			AccountType:   constants.WalletAccountPackage,
			Direction:     constants.WalletDirCredit,
			Hours:         pkg.Hours,
			UserPackageID: up.ID,
			RelatedID:     order.ID,
			Remark:        fmt.Sprintf("购买时长包「%s」发放小时", pkg.Name),
		}); err != nil {
			return fmt.Errorf("buy package hours ledger: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["package_order_ok"], userID, pkg.ID, pkg.Price))
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["user_package_credit"], userID, pkg.ID, pkg.Hours))
	return order, nil
}

// ListRecharges 查询充值记录。
func (s *RechargeService) ListRecharges(userID uint, page, pageSize int) ([]model.Recharge, int64, error) {
	return s.rechargeRepo.ListByUser(userID, page, pageSize)
}

// ListOrders 查询订单记录。
func (s *RechargeService) ListOrders(userID uint, page, pageSize int) ([]model.PackageOrder, int64, error) {
	return s.orderRepo.ListByUser(userID, page, pageSize)
}

// genOrderNo 生成订单号。
func genOrderNo() string {
	return fmt.Sprintf("PO%s%06d", time.Now().Format("20060102150405"), time.Now().UnixNano()%1000000)
}
