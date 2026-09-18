package service

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/esportsbar/backend/internal/constants"
	"github.com/esportsbar/backend/internal/dto"
	"github.com/esportsbar/backend/internal/model"
	"github.com/esportsbar/backend/internal/repository"
	"github.com/esportsbar/backend/internal/util"
)

// SessionService 上机记录服务（含排行榜、故障中断结算）。
type SessionService struct {
	sessionRepo     *repository.SessionRepository
	stationService  *StationService
	userPkgRepo     *repository.UserPackageRepository
	userRepo        *repository.UserRepository
	walletRepo      *repository.WalletTransactionRepository
	reservationRepo *repository.ReservationRepository
	db              *gorm.DB
	logger          *slog.Logger
}

// NewSessionService 构造上机记录服务。
func NewSessionService(
	sessionRepo *repository.SessionRepository,
	stationService *StationService,
	userPkgRepo *repository.UserPackageRepository,
	userRepo *repository.UserRepository,
	walletRepo *repository.WalletTransactionRepository,
	reservationRepo *repository.ReservationRepository,
	db *gorm.DB,
	logger *slog.Logger,
) *SessionService {
	return &SessionService{
		sessionRepo:     sessionRepo,
		stationService:  stationService,
		userPkgRepo:     userPkgRepo,
		userRepo:        userRepo,
		walletRepo:      walletRepo,
		reservationRepo: reservationRepo,
		db:              db,
		logger:          logger,
	}
}

// Start 会员上机开机：锁定机位并置为使用中（开机不计费，续费/下机时结算）。
func (s *SessionService) Start(userID uint, req *dto.StartSessionReq) (*model.Session, error) {
	station, err := s.stationService.GetByID(req.StationID)
	if err != nil {
		return nil, err
	}
	if station.Status != constants.StationIdle && station.Status != constants.StationReserved {
		return nil, util.NewAppError(constants.CodeStationBusy, "机位当前不可开机")
	}
	sess := &model.Session{
		UserID:        userID,
		StationID:     req.StationID,
		ReservationID: req.ReservationID,
		StartTime:     time.Now(),
		GameType:      defaultGameType(req.GameType),
		Status:        constants.SessionActive,
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		locked, err := s.stationService.LockForUpdate(tx, req.StationID)
		if err != nil {
			return err
		}
		if locked.Status == constants.StationUsing {
			return util.NewAppError(constants.CodeSessionOpen, "该机位已有进行中的上机记录")
		}
		locked.Status = constants.StationUsing
		if err := tx.Save(locked).Error; err != nil {
			return err
		}
		if req.ReservationID > 0 {
			res, err := s.reservationRepo.FindByID(req.ReservationID)
			if err == nil && res.Status == constants.ReservationConfirmed {
				res.Status = constants.ReservationCheckedIn
				if err := tx.Save(res).Error; err != nil {
					return err
				}
			}
		}
		return s.sessionRepo.Create(sess)
	})
	if err != nil {
		return nil, fmt.Errorf("session start tx: %w", err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["session_start_ok"], userID, req.StationID, sess.ID))
	return sess, nil
}

// Renew 续费：事务内延长上机结束时间，并从时长包/余额扣除费用、写入资金流水。
func (s *SessionService) Renew(userID, sessionID uint, req *dto.RenewSessionReq) (*model.Session, error) {
	sess, err := s.sessionRepo.FindByID(sessionID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, "上机记录不存在")
		}
		return nil, fmt.Errorf("session renew find: %w", err)
	}
	if sess.Status != constants.SessionActive || sess.UserID != userID {
		return nil, util.NewAppError(constants.CodeConflict, "仅进行中的上机可以续费")
	}
	station, err := s.stationService.GetByID(sess.StationID)
	if err != nil {
		return nil, err
	}
	neededHours := float64(req.AddMinutes) / 60
	now := time.Now()
	end := now.Add(time.Duration(req.AddMinutes) * time.Minute)
	if sess.EndTime != nil && sess.EndTime.After(now) {
		end = sess.EndTime.Add(time.Duration(req.AddMinutes) * time.Minute)
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		locked, err := s.sessionRepo.LockByIDTx(tx, sessionID)
		if err != nil {
			return err
		}
		if locked.Status != constants.SessionActive {
			return util.NewAppError(constants.CodeConflict, "仅进行中的上机可以续费")
		}
		if err := s.consumeHoursAndChargeTx(tx, userID, neededHours, station.PricePerHour, sessionID, 0, "上机续费扣费"); err != nil {
			return err
		}
		locked.EndTime = &end
		return s.sessionRepo.UpdateTx(tx, locked)
	})
	if err != nil {
		return nil, fmt.Errorf("session renew tx: %w", err)
	}
	sess.EndTime = &end
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["session_renew_ok"], sessionID, req.AddMinutes))
	return sess, nil
}

// End 会员下机：事务内锁定上机记录结算时长与金额，释放机位为空闲。
func (s *SessionService) End(userID, sessionID uint, req *dto.EndSessionReq) (*model.Session, error) {
	now := time.Now()
	var settled *model.Session
	err := s.db.Transaction(func(tx *gorm.DB) error {
		sess, err := s.sessionRepo.LockByIDTx(tx, sessionID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, "上机记录不存在")
			}
			return fmt.Errorf("session end lock: %w", err)
		}
		if sess.Status != constants.SessionActive {
			return util.NewAppError(constants.CodeConflict, "上机记录已结束")
		}
		if sess.UserID != userID {
			return util.NewAppError(constants.CodeForbidden, "仅上机会员本人可以下机")
		}
		station, err := s.stationService.LockForUpdate(tx, sess.StationID)
		if err != nil {
			return err
		}
		end := now
		// 已续费到更晚时间的，按续费结束时间结算；提前下机按实际使用分钟结算。
		if sess.EndTime != nil && sess.EndTime.After(now) {
			end = *sess.EndTime
		}
		duration := int(end.Sub(sess.StartTime).Minutes())
		if duration < 0 {
			duration = 0
		}
		amount := roundMoney(station.PricePerHour * float64(duration) / 60)
		neededHours := float64(duration) / 60
		if err := s.consumeHoursAndChargeTx(tx, userID, neededHours, station.PricePerHour, sessionID, 0, "下机结算扣费"); err != nil {
			return err
		}
		sess.EndTime = &end
		sess.DurationMinutes = duration
		sess.Amount = amount
		sess.GameType = defaultGameType(req.GameType)
		sess.Status = constants.SessionCompleted
		if err := s.sessionRepo.UpdateTx(tx, sess); err != nil {
			return err
		}
		station.Status = constants.StationIdle
		if err := tx.Save(station).Error; err != nil {
			return err
		}
		settled = sess
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("session end tx: %w", err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["session_end_ok"], sessionID, settled.DurationMinutes, settled.Amount))
	return settled, nil
}

// consumeHoursAndChargeTx 事务内消费扣款并写流水：先扣时长包（按最接近过期顺序），不足扣余额。
// 每腿一条 consume 流水，sessionID 关联上机记录，供故障中断回读重算。
func (s *SessionService) consumeHoursAndChargeTx(tx *gorm.DB, userID uint, neededHours, pricePerHour float64, sessionID, relatedID uint, remark string) error {
	if neededHours <= moneyEpsilon {
		return nil
	}
	legs, err := s.userPkgRepo.ConsumeHoursTx(tx, userID, neededHours)
	if err != nil {
		return fmt.Errorf("session consume package: %w", err)
	}
	consumedHours := 0.0
	for _, leg := range legs {
		consumedHours += leg.Hours
		wt := &model.WalletTransaction{
			UserID:        userID,
			ChangeType:    constants.WalletChangeConsume,
			AccountType:   constants.WalletAccountPackage,
			Direction:     constants.WalletDirDebit,
			Hours:         -roundHours(leg.Hours),
			UserPackageID: leg.UserPackageID,
			SessionID:     sessionID,
			RelatedID:     relatedID,
			Remark:        remark,
		}
		if err := s.walletRepo.CreateTx(tx, wt); err != nil {
			return fmt.Errorf("session consume package ledger: %w", err)
		}
	}
	remainingHours := roundHours(neededHours - consumedHours)
	if remainingHours > moneyEpsilon {
		cost := roundMoney(remainingHours * pricePerHour)
		if cost <= moneyEpsilon {
			return nil
		}
		newBalance, err := s.userRepo.AdjustBalanceTx(tx, userID, -cost)
		if err != nil {
			if errors.Is(err, repository.ErrConflict) {
				return util.NewAppError(constants.CodeInsufficient, "会员余额不足，请先充值")
			}
			return fmt.Errorf("session consume balance: %w", err)
		}
		wt := &model.WalletTransaction{
			UserID:       userID,
			ChangeType:   constants.WalletChangeConsume,
			AccountType:  constants.WalletAccountBalance,
			Direction:    constants.WalletDirDebit,
			Amount:       -cost,
			SessionID:    sessionID,
			RelatedID:    relatedID,
			BalanceAfter: newBalance,
			Remark:       remark,
		}
		if err := s.walletRepo.CreateTx(tx, wt); err != nil {
			return fmt.Errorf("session consume balance ledger: %w", err)
		}
	}
	return nil
}

// FaultInterruptResult 故障中断结算结果（看板与流水回读一致）。
type FaultInterruptResult struct {
	Station        *model.Station `json:"station"`
	Session        *model.Session `json:"session"`
	Interrupted    bool           `json:"interrupted"`     // 本次是否实际发生中断结算
	ActualMinutes  int            `json:"actual_minutes"`  // 实际使用分钟
	ActualAmount   float64        `json:"actual_amount"`   // 重算后应付金额
	RefundBalance  float64        `json:"refund_balance"`  // 已扣余额原额回补
	RefundHours    float64        `json:"refund_hours"`    // 返还时长包小时
	ExpiredHours   float64        `json:"expired_hours"`   // 已过期折算的时长包小时
	ExpiredBalance float64        `json:"expired_balance"` // 已过期时长包折算余额
}

// MarkStationFault 管理员标记机位故障。
// 若机位使用中：同一事务内中断进行中的上机（按实际使用分钟重算）、退回多扣费用、机位停在故障。
// 若机位无进行中上机：仅置为故障。同一条上机记录重复中断只结算一次。
func (s *SessionService) MarkStationFault(stationID uint) (*FaultInterruptResult, error) {
	result := &FaultInterruptResult{}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		station, err := s.stationService.LockForUpdate(tx, stationID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return util.NewAppError(constants.CodeNotFound, "机位不存在")
			}
			return fmt.Errorf("station fault lock station: %w", err)
		}
		// 已经是故障：幂等返回，不重复结算（同一条上机记录只结算一次）。
		if station.Status == constants.StationFault {
			result.Station = station
			if sess, fErr := s.sessionRepo.LockActiveByStationTx(tx, stationID); fErr == nil {
				result.Session = sess
			}
			return nil
		}
		if station.Status == constants.StationIdle {
			// 空闲机位无进行中上机：直接置故障，不涉及结算。
			station.Status = constants.StationFault
			if err := tx.Save(station).Error; err != nil {
				return err
			}
			result.Station = station
			return nil
		}
		if station.Status != constants.StationUsing {
			return util.NewAppError(constants.CodeConflict,
				fmt.Sprintf("机位当前状态为 %s，请先解除预约再标记故障", util.StatusText(station.Status)))
		}

		sess, err := s.sessionRepo.LockActiveByStationTx(tx, stationID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				// 机位显示使用中但无进行中上机（异常态兜底）：仅置故障，不结算。
				station.Status = constants.StationFault
				if err := tx.Save(station).Error; err != nil {
					return err
				}
				result.Station = station
				return nil
			}
			return fmt.Errorf("station fault lock session: %w", err)
		}
		// 二次防护：已存在退费流水则不重复结算。
		refunded, err := s.walletRepo.ExistsRefundBySessionTx(tx, sess.ID)
		if err != nil {
			return fmt.Errorf("station fault check refund: %w", err)
		}
		if refunded {
			return util.NewAppError(constants.CodeSessionSettled, "上机记录已结算，无法重复中断")
		}

		now := time.Now()
		duration := int(now.Sub(sess.StartTime).Minutes())
		if duration < 0 {
			duration = 0
		}
		actualAmount := roundMoney(station.PricePerHour * float64(duration) / 60)

		// 回读本次上机的全部消费扣费流水（余额腿+时长包腿，按原扣顺序）。
		legs, err := s.walletRepo.ListConsumeBySessionTx(tx, sess.ID)
		if err != nil {
			return fmt.Errorf("station fault read ledger: %w", err)
		}
		// 锁定涉及时长包快照，判断上机期间是否已过期。
		snapshots := make(map[uint]packageSnapshot)
		for _, leg := range legs {
			if leg.AccountType != constants.WalletAccountPackage || leg.UserPackageID == 0 {
				continue
			}
			if _, ok := snapshots[leg.UserPackageID]; ok {
				continue
			}
			up, err := s.userPkgRepo.LockByIDTx(tx, leg.UserPackageID)
			if err != nil {
				return fmt.Errorf("station fault lock package: %w", err)
			}
			expired := (up.ExpireAt != nil && !up.ExpireAt.After(now)) || up.Status == "expired"
			snapshots[up.ID] = packageSnapshot{ID: up.ID, Expired: expired}
		}

		plan := planRefund(legs, snapshots, station.PricePerHour, duration)

		// 1) 已扣余额原额回补 + 已过期时长包折算余额，合并为一次余额入账。
		creditBalance := roundMoney(plan.RefundBalance + plan.ExpiredBalance)
		balanceAfter := 0.0
		if creditBalance > moneyEpsilon {
			balanceAfter, err = s.userRepo.AdjustBalanceTx(tx, sess.UserID, creditBalance)
			if err != nil {
				return fmt.Errorf("station fault credit balance: %w", err)
			}
		}
		if plan.RefundBalance > moneyEpsilon {
			if err := s.walletRepo.CreateTx(tx, &model.WalletTransaction{
				UserID:       sess.UserID,
				ChangeType:   constants.WalletChangeRefundBalance,
				AccountType:  constants.WalletAccountBalance,
				Direction:    constants.WalletDirCredit,
				Amount:       plan.RefundBalance,
				SessionID:    sess.ID,
				BalanceAfter: balanceAfter,
				Remark:       fmt.Sprintf("机位故障中断，上机 #%d 已扣余额原额退回", sess.ID),
			}); err != nil {
				return fmt.Errorf("station fault refund balance ledger: %w", err)
			}
		}
		if plan.ExpiredBalance > moneyEpsilon {
			if err := s.walletRepo.CreateTx(tx, &model.WalletTransaction{
				UserID:       sess.UserID,
				ChangeType:   constants.WalletChangeExpiredToBalance,
				AccountType:  constants.WalletAccountBalance,
				Direction:    constants.WalletDirCredit,
				Amount:       plan.ExpiredBalance,
				SessionID:    sess.ID,
				BalanceAfter: balanceAfter,
				Remark:       fmt.Sprintf("机位故障中断，上机 #%d 期间已过期时长包折算余额", sess.ID),
			}); err != nil {
				return fmt.Errorf("station fault expired ledger: %w", err)
			}
		}

		// 2) 时长包按原扣顺序返还小时（上机期间仍有效的时长包）。
		for _, leg := range plan.Legs {
			if leg.Expired || leg.Hours <= moneyEpsilon {
				continue
			}
			if _, err := s.userPkgRepo.RefundHoursTx(tx, leg.UserPackageID, leg.Hours); err != nil {
				return fmt.Errorf("station fault refund hours: %w", err)
			}
			if err := s.walletRepo.CreateTx(tx, &model.WalletTransaction{
				UserID:        sess.UserID,
				ChangeType:    constants.WalletChangeRefundPackage,
				AccountType:   constants.WalletAccountPackage,
				Direction:     constants.WalletDirCredit,
				Hours:         leg.Hours,
				UserPackageID: leg.UserPackageID,
				SessionID:     sess.ID,
				Remark:        fmt.Sprintf("机位故障中断，上机 #%d 时长包小时按原扣顺序返还", sess.ID),
			}); err != nil {
				return fmt.Errorf("station fault refund hours ledger: %w", err)
			}
		}

		// 3) 上机记录置为中断，按实际使用分钟结算；机位停在故障（不是空闲）。
		sess.EndTime = &now
		sess.DurationMinutes = duration
		sess.Amount = actualAmount
		sess.Status = constants.SessionInterrupted
		if err := s.sessionRepo.UpdateTx(tx, sess); err != nil {
			return fmt.Errorf("station fault update session: %w", err)
		}
		station.Status = constants.StationFault
		if err := tx.Save(station).Error; err != nil {
			return err
		}

		result.Station = station
		result.Session = sess
		result.Interrupted = true
		result.ActualMinutes = duration
		result.ActualAmount = actualAmount
		result.RefundBalance = plan.RefundBalance
		result.RefundHours = plan.RefundHours
		result.ExpiredHours = plan.ExpiredHours
		result.ExpiredBalance = plan.ExpiredBalance
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("mark station fault tx: %w", err)
	}
	if result.Interrupted {
		s.logger.Info(fmt.Sprintf(constants.LogTemplates["session_interrupt_ok"],
			stationID, result.Session.ID, result.ActualMinutes, result.ActualAmount,
			result.RefundBalance, result.RefundHours, result.ExpiredBalance))
	}
	return result, nil
}

// List 分页查询上机记录。
func (s *SessionService) List(page, pageSize int, userID uint, status string) ([]model.Session, int64, error) {
	return s.sessionRepo.List(page, pageSize, userID, status)
}

// Rank 上机时长排行榜。
func (s *SessionService) Rank(query *dto.RankQuery) ([]RankItem, error) {
	period := query.Period
	if period == "" {
		period = "week"
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}
	rows, _, err := s.sessionRepo.Rank(period, query.GameType, limit)
	if err != nil {
		return nil, fmt.Errorf("session rank: %w", err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["session_rank_query"], period, query.GameType))
	items := make([]RankItem, 0, len(rows))
	for i, row := range rows {
		user, err := s.userRepo.FindByID(row.UserID)
		if err != nil {
			continue
		}
		items = append(items, RankItem{
			Rank:         i + 1,
			UserID:       row.UserID,
			Username:     user.Username,
			Nickname:     user.Nickname,
			TotalMinutes: row.DurationMinutes,
		})
	}
	return items, nil
}

// RankItem 排行榜条目。
type RankItem struct {
	Rank         int    `json:"rank"`
	UserID       uint   `json:"user_id"`
	Username     string `json:"username"`
	Nickname     string `json:"nickname"`
	TotalMinutes int    `json:"total_minutes"`
}

// defaultGameType 默认游戏类型。
func defaultGameType(gameType string) string {
	if gameType == "" {
		return constants.GameOther
	}
	return gameType
}
