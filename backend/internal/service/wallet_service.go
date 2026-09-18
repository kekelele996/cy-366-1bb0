package service

import (
	"fmt"
	"log/slog"

	"github.com/esportsbar/backend/internal/model"
	"github.com/esportsbar/backend/internal/repository"
)

// WalletService 会员资金流水查询服务（看板与流水回读一致）。
type WalletService struct {
	walletRepo *repository.WalletTransactionRepository
	logger     *slog.Logger
}

// NewWalletService 构造资金流水服务。
func NewWalletService(walletRepo *repository.WalletTransactionRepository, logger *slog.Logger) *WalletService {
	return &WalletService{walletRepo: walletRepo, logger: logger}
}

// ListMyTransactions 查询会员本人资金流水。
func (s *WalletService) ListMyTransactions(userID uint, page, pageSize int) ([]model.WalletTransaction, int64, error) {
	list, total, err := s.walletRepo.ListByUser(userID, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("wallet list transactions: %w", err)
	}
	return list, total, nil
}
