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

// SessionService 上机记录服务（含排行榜、扣费流水与故障中断）。
type SessionService struct {
	sessionRepo     *repository.SessionRepository
	chargeRepo      *repository.SessionChargeRepository
	flowRepo        *repository.WalletFlowRepository
	stationService  *StationService
	userPkgRepo     *repository.UserPackageRepository
	userRepo        *repository.UserRepository
	reservationRepo *repository.ReservationRepository
	db              *gorm.DB
	logger          *slog.Logger
}

// NewSessionService 构造上机记录服务。
func NewSessionService(
	sessionRepo *repository.SessionRepository,
	chargeRepo *repository.SessionChargeRepository,
	flowRepo *repository.WalletFlowRepository,
	stationService *StationService,
	userPkgRepo *repository.UserPackageRepository,
	userRepo *repository.UserRepository,
	reservationRepo *repository.ReservationRepository,
	db *gorm.DB,
	logger *slog.Logger,
) *SessionService {
	return &SessionService{
		sessionRepo:     sessionRepo,
		chargeRepo:      chargeRepo,
		flowRepo:        flowRepo,
		stationService:  stationService,
		userPkgRepo:     userPkgRepo,
		userRepo:        userRepo,
		reservationRepo: reservationRepo,
		db:              db,
		logger:          logger,
	}
}

// Start 会员上机开机：锁定机位并置为使用中。
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

// Renew 续费：延长上机结束时间，并在同一事务内从时长包/余额扣除费用、记账扣费明细与流水。
func (s *SessionService) Renew(userID, sessionID uint, req *dto.RenewSessionReq) (*model.Session, error) {
	sess, err := s.sessionRepo.FindByID(sessionID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, "上机记录不存在")
		}
		return nil, fmt.Errorf("session renew find: %w", err)
	}
	if sess.Status != constants.SessionActive {
		return nil, util.NewAppError(constants.CodeConflict, "仅进行中的上机可以续费")
	}
	if sess.UserID != userID {
		return nil, util.NewAppError(constants.CodeForbidden, "仅能为本人的上机记录续费")
	}
	station, err := s.stationService.GetByID(sess.StationID)
	if err != nil {
		return nil, err
	}
	neededHours := float64(req.AddMinutes) / 60
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.sessionRepo.FindByIDTx(tx, sessionID); err != nil {
			return err
		}
		if err := s.chargeWithinTx(tx, sess, neededHours, station.PricePerHour, constants.FlowBizSessionRenew,
			fmt.Sprintf("上机 #%d 续费 %d 分钟", sessionID, req.AddMinutes)); err != nil {
			return err
		}
		now := time.Now()
		if sess.EndTime == nil {
			sess.EndTime = &now
		}
		end := sess.EndTime.Add(time.Duration(req.AddMinutes) * time.Minute)
		sess.EndTime = &end
		return s.sessionRepo.SaveTx(tx, sess)
	})
	if err != nil {
		return nil, fmt.Errorf("session renew tx: %w", err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["session_renew_ok"], sessionID, req.AddMinutes))
	return sess, nil
}

// End 会员下机：按实际使用结算时长与金额，释放机位；扣费、结算、机位状态同一事务生效。
func (s *SessionService) End(userID, sessionID uint, req *dto.EndSessionReq) (*model.Session, error) {
	sess, err := s.sessionRepo.FindByID(sessionID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, "上机记录不存在")
		}
		return nil, fmt.Errorf("session end find: %w", err)
	}
	if sess.Status != constants.SessionActive {
		return nil, util.NewAppError(constants.CodeConflict, "上机记录已结束")
	}
	if sess.UserID != userID {
		return nil, util.NewAppError(constants.CodeForbidden, "仅能结束本人的上机记录")
	}
	now := time.Now()
	end := now
	if sess.EndTime != nil && sess.EndTime.After(now) {
		end = *sess.EndTime
	}
	duration := int(end.Sub(sess.StartTime).Minutes())
	station, err := s.stationService.GetByID(sess.StationID)
	if err != nil {
		return nil, err
	}
	amount := station.PricePerHour * float64(duration) / 60
	neededHours := float64(duration) / 60
	err = s.db.Transaction(func(tx *gorm.DB) error {
		lockedSess, err := s.sessionRepo.FindByIDTx(tx, sessionID)
		if err != nil {
			return err
		}
		if lockedSess.Status != constants.SessionActive {
			return util.NewAppError(constants.CodeConflict, "上机记录已结束")
		}
		if err := s.chargeWithinTx(tx, sess, neededHours, station.PricePerHour, constants.FlowBizSessionEnd,
			fmt.Sprintf("上机 #%d 下机结算 %d 分钟", sessionID, duration)); err != nil {
			return err
		}
		sess.EndTime = &end
		sess.DurationMinutes = duration
		sess.Amount = amount
		sess.GameType = defaultGameType(req.GameType)
		sess.Status = constants.SessionCompleted
		if err := s.sessionRepo.SaveTx(tx, sess); err != nil {
			return err
		}
		locked, err := s.stationService.LockForUpdate(tx, sess.StationID)
		if err != nil {
			return err
		}
		locked.Status = constants.StationIdle
		return tx.Save(locked).Error
	})
	if err != nil {
		return nil, fmt.Errorf("session end tx: %w", err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["session_end_ok"], sessionID, duration, amount))
	return sess, nil
}

// chargeWithinTx 事务内扣费：优先消耗时长包小时数，不足部分按时价从余额扣除；
// 每一笔扣减同时写入 session_charges（故障中断回退依据）与 wallet_flows（流水回读依据）。
func (s *SessionService) chargeWithinTx(tx *gorm.DB, sess *model.Session, neededHours, pricePerHour float64, bizType, remark string) error {
	now := time.Now()
	pkgs, err := s.userPkgRepo.LockActiveByUserTx(tx, sess.UserID)
	if err != nil {
		return fmt.Errorf("charge lock packages: %w", err)
	}
	remaining := neededHours
	for i := range pkgs {
		if remaining <= 0.0001 {
			break
		}
		up := &pkgs[i]
		// 锁内再次确认过期状态：过期包不再用于扣减。
		if up.ExpireAt != nil && !up.ExpireAt.After(now) {
			up.Status = "expired"
			if err := s.userPkgRepo.SaveTx(tx, up); err != nil {
				return err
			}
			continue
		}
		use := remaining
		if up.RemainingHours < use {
			use = up.RemainingHours
		}
		up.RemainingHours -= use
		if up.RemainingHours <= 0.0001 {
			up.Status = "used"
		}
		if err := s.userPkgRepo.SaveTx(tx, up); err != nil {
			return err
		}
		expire := up.ExpireAt
		cashValue := s.roundMoney(use * pricePerHour)
		charge := &model.SessionCharge{
			SessionID:       sess.ID,
			UserID:          sess.UserID,
			UserPackageID:   up.ID,
			PackageID:       up.PackageID,
			PackageName:     up.PackageName,
			Kind:            constants.ChargeKindPackage,
			Hours:           s.roundHours(use),
			Amount:          cashValue,
			PricePerHour:    pricePerHour,
			BizType:         bizType,
			PackageExpireAt: expire,
		}
		if err := s.chargeRepo.CreateTx(tx, charge); err != nil {
			return fmt.Errorf("charge create package record: %w", err)
		}
		if err := s.flowRepo.CreateTx(tx, &model.WalletFlow{
			UserID:        sess.UserID,
			SessionID:     sess.ID,
			UserPackageID: up.ID,
			Direction:     constants.FlowDirectionDeduct,
			Kind:          constants.FlowKindPackageHours,
			BizType:       bizType,
			Amount:        cashValue,
			Hours:         s.roundHours(use),
			Remark:        remark,
		}); err != nil {
			return fmt.Errorf("charge write package flow: %w", err)
		}
		remaining -= use
	}
	if remaining > 0.0001 {
		cost := s.roundMoney(remaining * pricePerHour)
		if err := s.userRepo.UpdateBalanceTx(tx, sess.UserID, -cost); err != nil {
			if errors.Is(err, repository.ErrConflict) {
				return util.NewAppError(constants.CodeInsufficient, "会员余额不足，请先充值")
			}
			return fmt.Errorf("charge update balance: %w", err)
		}
		if err := s.chargeRepo.CreateTx(tx, &model.SessionCharge{
			SessionID:    sess.ID,
			UserID:       sess.UserID,
			Kind:         constants.ChargeKindBalance,
			Amount:       cost,
			PricePerHour: pricePerHour,
			BizType:      bizType,
		}); err != nil {
			return fmt.Errorf("charge create balance record: %w", err)
		}
		if err := s.flowRepo.CreateTx(tx, &model.WalletFlow{
			UserID:    sess.UserID,
			SessionID: sess.ID,
			Direction: constants.FlowDirectionDeduct,
			Kind:      constants.FlowKindBalance,
			BizType:   bizType,
			Amount:    cost,
			Remark:    remark,
		}); err != nil {
			return fmt.Errorf("charge write balance flow: %w", err)
		}
	}
	return nil
}

// InterruptResult 故障中断结算结果（与机位状态在同一事务内返回，看板与流水回读一致）。
type InterruptResult struct {
	SessionID         uint    `json:"session_id"`
	StationID         uint    `json:"station_id"`
	UserID            uint    `json:"user_id"`
	EndTime           string  `json:"end_time"`
	DurationMinutes   int     `json:"duration_minutes"`
	RefundBalance     float64 `json:"refund_balance"`       // 回补到余额的总金额
	RefundedHours     float64 `json:"refunded_hours"`       // 返还到时长包的小时数
	ExpiredHours      float64 `json:"expired_hours"`        // 上机期间已过期、折算余额的小时数
	ExpiredCash       float64 `json:"expired_cash"`         // 过期小时折算的等额余额
	Flows             []model.WalletFlow `json:"flows"`
	AlreadyInterrupted bool   `json:"already_interrupted"` // 重复中断时为 true，不重复结算
}

// InterruptByStation 机位标记故障时调用：中断该机位进行中的上机并回退已扣费用。
// 必须由机位状态流转事务（tx）调用；机位状态与费用回补、流水任一步失败全部回滚。
// 对同一条上机记录重复中断只结算一次。
func (s *SessionService) InterruptByStation(tx *gorm.DB, stationID uint) (*InterruptResult, error) {
	sess, err := s.sessionRepo.FindActiveByStationTx(tx, stationID)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, fmt.Errorf("interrupt find active session: %w", err)
		}
		// 没有进行中的上机：可能是从未上机，也可能这条上机已被故障中断过（重复中断只结算一次）。
		last, lastErr := s.sessionRepo.FindLastByStationTx(tx, stationID)
		if lastErr != nil {
			if errors.Is(lastErr, repository.ErrNotFound) {
				return nil, nil
			}
			return nil, fmt.Errorf("interrupt find last session: %w", lastErr)
		}
		if last.Status != constants.SessionInterrupted {
			// 最近一条是正常下机等非中断记录：无需处理。
			return nil, nil
		}
		return &InterruptResult{
			SessionID:          last.ID,
			StationID:          last.StationID,
			UserID:             last.UserID,
			EndTime:            formatTimeOrEmpty(last.EndTime),
			DurationMinutes:    last.DurationMinutes,
			AlreadyInterrupted: true,
		}, nil
	}
	charges, err := s.chargeRepo.ListUnrefundedBySessionTx(tx, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("interrupt list charges: %w", err)
	}
	now := time.Now()
	// 中断按实际使用分钟重算：故障机位不收取本次上机费用（金额置 0），实际使用分钟仍记录用于看板/排行榜。
	duration := int(now.Sub(sess.StartTime).Minutes())
	plan := buildInterruptRefundPlan(charges, sess.StartTime, now)

	result := &InterruptResult{
		SessionID:       sess.ID,
		StationID:       sess.StationID,
		UserID:          sess.UserID,
		EndTime:         now.Format(time.RFC3339),
		DurationMinutes: duration,
	}

	// 行锁会员账户，余额变动一次完成（原额回补 + 过期包折算）。
	totalBalance := plan.TotalBalanceRefund
	// 明细指向的时长包若已不存在，按等额余额兜底退回，保证会员资金不丢。
	missingPackageCash := 0.0
	for i := range plan.Items {
		if plan.Items[i].UserPackageID == 0 || plan.Items[i].RefundKind == constants.RefundKindBalance {
			continue
		}
		if _, err := s.userPkgRepo.LockByIDTx(tx, plan.Items[i].UserPackageID); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				plan.Items[i].RefundKind = constants.RefundKindBalance
				missingPackageCash += plan.Items[i].Cash
				continue
			}
			return nil, fmt.Errorf("interrupt lock package: %w", err)
		}
	}
	totalBalance += missingPackageCash
	if totalBalance > 0.0001 {
		if err := s.userRepo.UpdateBalanceTx(tx, sess.UserID, totalBalance); err != nil {
			return nil, fmt.Errorf("interrupt refund balance: %w", err)
		}
		result.RefundBalance = s.roundMoney(totalBalance)
	}

	// 按原始扣减顺序逐条回退明细。
	for i := range plan.Items {
		item := &plan.Items[i]
		// 时长包还存在的：小时返还 / 过期折算余额，并恢复其状态与余量。
		if item.UserPackageID > 0 && item.RefundKind != constants.RefundKindBalance {
			up, err := s.userPkgRepo.LockByIDTx(tx, item.UserPackageID)
			if err != nil {
				return nil, fmt.Errorf("interrupt relock package: %w", err)
			}
			switch item.RefundKind {
			case constants.RefundKindHours:
				up.RemainingHours += item.Hours
				// used/expired 终态的包重新激活，保证返还的小时可用。
				if up.Status == "used" || up.Status == "expired" {
					up.Status = "active"
				}
				if err := s.userPkgRepo.SaveTx(tx, up); err != nil {
					return nil, fmt.Errorf("interrupt return package hours: %w", err)
				}
				result.RefundedHours += s.roundHours(item.Hours)
			case constants.RefundKindExpiredCash:
				// 上机期间已过期：小时不返还，确保包为 expired 终态。
				if up.Status != "expired" {
					up.Status = "expired"
					if err := s.userPkgRepo.SaveTx(tx, up); err != nil {
						return nil, fmt.Errorf("interrupt mark package expired: %w", err)
					}
				}
				result.ExpiredHours += s.roundHours(item.Hours)
				result.ExpiredCash += s.roundMoney(item.Cash)
			}
		}
		flow := &model.WalletFlow{
			UserID:        sess.UserID,
			SessionID:     sess.ID,
			UserPackageID: item.UserPackageID,
			Direction:     constants.FlowDirectionRefund,
			BizType:       constants.FlowBizSessionInterrupt,
			Remark:        interruptRefundRemark(item, sess.ID),
		}
		switch item.RefundKind {
		case constants.RefundKindHours:
			flow.Kind = constants.FlowKindPackageHours
			flow.Hours = s.roundHours(item.Hours)
			flow.Amount = item.Cash
			if err := s.markAndWrite(tx, item, flow); err != nil {
				return nil, err
			}
		case constants.RefundKindExpiredCash, constants.RefundKindBalance:
			flow.Kind = constants.FlowKindBalance
			flow.Amount = s.roundMoney(item.Cash)
			if err := s.markAndWrite(tx, item, flow); err != nil {
				return nil, err
			}
		}
		result.Flows = append(result.Flows, *flow)
	}
	result.ExpiredHours = s.roundHours(result.ExpiredHours)
	result.ExpiredCash = s.roundMoney(result.ExpiredCash)
	result.RefundedHours = s.roundHours(result.RefundedHours)
	// 结算上机记录：实际使用分钟落账，金额置 0（故障补偿），状态停在 interrupted。
	sess.EndTime = &now
	sess.DurationMinutes = duration
	sess.Amount = 0
	sess.Status = constants.SessionInterrupted
	if err := s.sessionRepo.SaveTx(tx, sess); err != nil {
		return nil, fmt.Errorf("interrupt save session: %w", err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["session_interrupt_ok"],
		sess.ID, stationID, duration, result.RefundBalance, result.RefundedHours, result.ExpiredCash))
	return result, nil
}

// markAndWrite 事务内标记明细已退还并写流水（明细与流水同时生效）。
func (s *SessionService) markAndWrite(tx *gorm.DB, item *interruptRefundItem, flow *model.WalletFlow) error {
	if err := s.chargeRepo.MarkRefundedTx(tx, item.ChargeID, item.RefundKind, s.roundMoney(item.Cash), s.roundHours(item.Hours)); err != nil {
		return fmt.Errorf("interrupt mark charge refunded: %w", err)
	}
	if err := s.flowRepo.CreateTx(tx, flow); err != nil {
		return fmt.Errorf("interrupt write refund flow: %w", err)
	}
	return nil
}

// List 分页查询上机记录。
func (s *SessionService) List(page, pageSize int, userID uint, status string) ([]model.Session, int64, error) {
	return s.sessionRepo.List(page, pageSize, userID, status)
}

// ListFlows 查询上机记录相关流水（看板与流水回读一致）。
func (s *SessionService) ListFlows(sessionID uint) ([]model.WalletFlow, error) {
	return s.flowRepo.ListBySession(sessionID)
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

// roundMoney 金额保留两位小数。
func (s *SessionService) roundMoney(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// roundHours 小时保留两位小数。
func (s *SessionService) roundHours(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

func formatTimeOrEmpty(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}
