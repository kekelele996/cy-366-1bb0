package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/esportsbar/backend/internal/constants"
	"github.com/esportsbar/backend/internal/model"
	"github.com/esportsbar/backend/internal/repository"
)

// newFaultTestDB 构造内存 SQLite 并迁移故障中断结算涉及的表（每个用例独立内存库）。
func newFaultTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Station{}, &model.Session{},
		&model.UserPackage{}, &model.WalletTransaction{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func buildFaultServices(db *gorm.DB) (*SessionService, *repository.SessionRepository, *repository.StationRepository, *repository.UserRepository, *repository.UserPackageRepository, *repository.WalletTransactionRepository) {
	logger := newTestLogger()
	userRepo := repository.NewUserRepository(db)
	stationRepo := repository.NewStationRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	userPkgRepo := repository.NewUserPackageRepository(db)
	walletRepo := repository.NewWalletTransactionRepository(db)
	stationService := NewStationService(stationRepo, logger)
	sessionService := NewSessionService(sessionRepo, stationService, userPkgRepo, userRepo, walletRepo, nil, db, logger)
	return sessionService, sessionRepo, stationRepo, userRepo, userPkgRepo, walletRepo
}

func seedConsumeLeg(t *testing.T, db *gorm.DB, wt *model.WalletTransaction) {
	t.Helper()
	if err := db.Create(wt).Error; err != nil {
		t.Fatalf("seed consume leg: %v", err)
	}
}

// TestMarkStationFaultExpiredPackageToBalance 场景：
// 已扣 = 过期包1h + 有效包1h + 余额¥10（共¥30）；实际使用90分钟应付¥15，多扣¥15。
// 余额原额退¥10，过期包不返还小时、折算¥5 余额；机位停在故障。
func TestMarkStationFaultExpiredPackageToBalance(t *testing.T) {
	db := newFaultTestDB(t)
	svc, _, stationRepo, userRepo, userPkgRepo, walletRepo := buildFaultServices(db)

	now := time.Now()
	user := &model.User{Username: "m1", Role: "member", Balance: 50, Status: "active"}
	mustCreate(t, db, user)
	station := &model.Station{Name: "S1", Area: "A", StationType: "seat", PricePerHour: 10, Status: constants.StationUsing}
	mustCreate(t, db, station)
	past := now.Add(-time.Hour)
	future := now.Add(30 * 24 * time.Hour)
	pkgExpired := &model.UserPackage{UserID: user.ID, PackageID: 1, TotalHours: 1, RemainingHours: 0, ExpireAt: &past, Status: "expired"}
	pkgValid := &model.UserPackage{UserID: user.ID, PackageID: 2, TotalHours: 1, RemainingHours: 0, ExpireAt: &future, Status: "used"}
	mustCreate(t, db, pkgExpired)
	mustCreate(t, db, pkgValid)
	sess := &model.Session{UserID: user.ID, StationID: station.ID, StartTime: now.Add(-90*time.Minute - 30*time.Second), GameType: "other", Status: constants.SessionActive}
	mustCreate(t, db, sess)

	seedConsumeLeg(t, db, &model.WalletTransaction{UserID: user.ID, ChangeType: constants.WalletChangeConsume, AccountType: "package", Direction: "debit", Hours: -1, UserPackageID: pkgExpired.ID, SessionID: sess.ID})
	seedConsumeLeg(t, db, &model.WalletTransaction{UserID: user.ID, ChangeType: constants.WalletChangeConsume, AccountType: "package", Direction: "debit", Hours: -1, UserPackageID: pkgValid.ID, SessionID: sess.ID})
	seedConsumeLeg(t, db, &model.WalletTransaction{UserID: user.ID, ChangeType: constants.WalletChangeConsume, AccountType: "balance", Direction: "debit", Amount: -10, SessionID: sess.ID, BalanceAfter: 40})

	res, err := svc.MarkStationFault(station.ID)
	if err != nil {
		t.Fatalf("MarkStationFault error: %v", err)
	}
	if !res.Interrupted {
		t.Fatal("expected interrupted=true")
	}
	if res.ActualMinutes != 90 {
		t.Errorf("ActualMinutes = %d, want 90", res.ActualMinutes)
	}
	if res.ActualAmount != 15 {
		t.Errorf("ActualAmount = %v, want 15", res.ActualAmount)
	}
	if res.RefundBalance != 10 {
		t.Errorf("RefundBalance = %v, want 10", res.RefundBalance)
	}
	if res.ExpiredHours != 0.5 || res.ExpiredBalance != 5 {
		t.Errorf("expired conversion = (%v h, ¥%v), want (0.5, 5)", res.ExpiredHours, res.ExpiredBalance)
	}
	if res.RefundHours != 0 {
		t.Errorf("RefundHours = %v, want 0", res.RefundHours)
	}

	// 机位停在故障、上机中断。
	st, _ := stationRepo.FindByID(station.ID)
	if st.Status != constants.StationFault {
		t.Errorf("station status = %s, want fault", st.Status)
	}
	var got model.Session
	db.First(&got, sess.ID)
	if got.Status != constants.SessionInterrupted {
		t.Errorf("session status = %s, want interrupted", got.Status)
	}
	if got.Amount != 15 || got.DurationMinutes != 90 || got.EndTime == nil {
		t.Errorf("session settlement wrong: %+v", got)
	}

	// 余额：50 + 10 退回 + 5 折算 = 65。
	u, _ := userRepo.FindByID(user.ID)
	if u.Balance != 65 {
		t.Errorf("user balance = %v, want 65", u.Balance)
	}
	// 过期包不返还小时，仍为 0/expired；有效包未进入退费腿，保持原状。
	pe, _ := userPkgRepo.FindByID(pkgExpired.ID)
	if pe.RemainingHours != 0 || pe.Status != "expired" {
		t.Errorf("expired package should not be refunded: %+v", pe)
	}
	pv, _ := userPkgRepo.FindByID(pkgValid.ID)
	if pv.RemainingHours != 0 {
		t.Errorf("valid package should be untouched in this scenario: %+v", pv)
	}

	// 流水回读：2 条退费流水（余额退回 + 过期折算），无时长包返还。
	var credits []model.WalletTransaction
	db.Where("session_id = ? AND direction = ?", sess.ID, constants.WalletDirCredit).Find(&credits)
	if len(credits) != 2 {
		t.Fatalf("credit ledger count = %d, want 2", len(credits))
	}
	_ = walletRepo
}

// TestMarkStationFaultRefundValidPackageHours 场景：
// 仅有效包扣 2 小时（¥20），实际 60 分钟应付 ¥10，多扣 1 小时按原顺序返还。
func TestMarkStationFaultRefundValidPackageHours(t *testing.T) {
	db := newFaultTestDB(t)
	svc, _, stationRepo, userRepo, userPkgRepo, _ := buildFaultServices(db)

	now := time.Now()
	user := &model.User{Username: "m2", Role: "member", Balance: 50, Status: "active"}
	mustCreate(t, db, user)
	station := &model.Station{Name: "S2", Area: "A", StationType: "seat", PricePerHour: 10, Status: constants.StationUsing}
	mustCreate(t, db, station)
	future := now.Add(30 * 24 * time.Hour)
	pkg := &model.UserPackage{UserID: user.ID, PackageID: 3, TotalHours: 2, RemainingHours: 0, ExpireAt: &future, Status: "used"}
	mustCreate(t, db, pkg)
	sess := &model.Session{UserID: user.ID, StationID: station.ID, StartTime: now.Add(-60*time.Minute - 30*time.Second), GameType: "other", Status: constants.SessionActive}
	mustCreate(t, db, sess)
	seedConsumeLeg(t, db, &model.WalletTransaction{UserID: user.ID, ChangeType: constants.WalletChangeConsume, AccountType: "package", Direction: "debit", Hours: -2, UserPackageID: pkg.ID, SessionID: sess.ID})

	res, err := svc.MarkStationFault(station.ID)
	if err != nil {
		t.Fatalf("MarkStationFault error: %v", err)
	}
	if res.RefundHours != 1 {
		t.Errorf("RefundHours = %v, want 1", res.RefundHours)
	}
	if res.RefundBalance != 0 || res.ExpiredBalance != 0 {
		t.Errorf("no balance refund expected, got bal=%v exp=%v", res.RefundBalance, res.ExpiredBalance)
	}
	// 时长包返还 1 小时并恢复 active。
	up, _ := userPkgRepo.FindByID(pkg.ID)
	if up.RemainingHours != 1 || up.Status != "active" {
		t.Errorf("package after refund = %+v, want remaining=1 active", up)
	}
	// 余额不变。
	u, _ := userRepo.FindByID(user.ID)
	if u.Balance != 50 {
		t.Errorf("balance = %v, want 50", u.Balance)
	}
	st, _ := stationRepo.FindByID(station.ID)
	if st.Status != constants.StationFault {
		t.Errorf("station = %s, want fault", st.Status)
	}
	var got model.Session
	db.First(&got, sess.ID)
	if got.Status != constants.SessionInterrupted || got.Amount != 10 {
		t.Errorf("session wrong: %+v", got)
	}
	var pkgCredits int64
	db.Model(&model.WalletTransaction{}).
		Where("session_id = ? AND change_type = ?", sess.ID, constants.WalletChangeRefundPackage).Count(&pkgCredits)
	if pkgCredits != 1 {
		t.Errorf("refund_package ledger = %d, want 1", pkgCredits)
	}
}

// TestMarkStationFaultIdempotent 同一条上机重复中断只结算一次：机位已故障时再次调用不产生新流水/余额变动。
func TestMarkStationFaultIdempotent(t *testing.T) {
	db := newFaultTestDB(t)
	svc, _, _, userRepo, _, walletRepo := buildFaultServices(db)

	now := time.Now()
	user := &model.User{Username: "m3", Role: "member", Balance: 50, Status: "active"}
	mustCreate(t, db, user)
	station := &model.Station{Name: "S3", Area: "A", StationType: "seat", PricePerHour: 10, Status: constants.StationUsing}
	mustCreate(t, db, station)
	sess := &model.Session{UserID: user.ID, StationID: station.ID, StartTime: now.Add(-30 * time.Minute), GameType: "other", Status: constants.SessionActive}
	mustCreate(t, db, sess)
	seedConsumeLeg(t, db, &model.WalletTransaction{UserID: user.ID, ChangeType: constants.WalletChangeConsume, AccountType: "balance", Direction: "debit", Amount: -10, SessionID: sess.ID})

	if _, err := svc.MarkStationFault(station.ID); err != nil {
		t.Fatalf("first fault error: %v", err)
	}
	balanceAfterFirst, _ := userRepo.FindByID(user.ID)
	var ledgerAfterFirst int64
	db.Model(&model.WalletTransaction{}).Where("session_id = ?", sess.ID).Count(&ledgerAfterFirst)

	// 再次标记故障：幂等返回，不重复结算。
	res2, err := svc.MarkStationFault(station.ID)
	if err != nil {
		t.Fatalf("second fault should be idempotent, got error: %v", err)
	}
	if res2.Interrupted {
		t.Error("second call must not interrupt/settle again")
	}
	balanceAfterSecond, _ := userRepo.FindByID(user.ID)
	if balanceAfterSecond.Balance != balanceAfterFirst.Balance {
		t.Errorf("balance changed on repeat: %v -> %v", balanceAfterFirst.Balance, balanceAfterSecond.Balance)
	}
	var ledgerAfterSecond int64
	db.Model(&model.WalletTransaction{}).Where("session_id = ?", sess.ID).Count(&ledgerAfterSecond)
	if ledgerAfterSecond != ledgerAfterFirst {
		t.Errorf("ledger rows changed on repeat: %d -> %d", ledgerAfterFirst, ledgerAfterSecond)
	}
	var got model.Session
	db.First(&got, sess.ID)
	if got.Status != constants.SessionInterrupted {
		t.Errorf("session status = %s, want interrupted", got.Status)
	}
	// 流水回读：余额退回流水与最终余额一致。
	list, _, err := walletRepo.ListByUser(user.ID, 1, 50)
	if err != nil {
		t.Fatalf("list ledger: %v", err)
	}
	if len(list) != 2 { // 1 条消费 + 1 条退回
		t.Errorf("ledger total = %d, want 2", len(list))
	}
}

// TestMarkStationFaultIdleDirect 空闲机位标记故障：直接置故障，无结算无流水。
func TestMarkStationFaultIdleDirect(t *testing.T) {
	db := newFaultTestDB(t)
	svc, _, stationRepo, _, _, _ := buildFaultServices(db)

	station := &model.Station{Name: "S4", Area: "A", StationType: "seat", PricePerHour: 10, Status: constants.StationIdle}
	mustCreate(t, db, station)
	res, err := svc.MarkStationFault(station.ID)
	if err != nil {
		t.Fatalf("idle station fault error: %v", err)
	}
	if res.Interrupted {
		t.Error("idle station must not trigger settlement")
	}
	st, _ := stationRepo.FindByID(station.ID)
	if st.Status != constants.StationFault {
		t.Errorf("idle station should become fault, got %s", st.Status)
	}
}

// TestMarkStationFaultRejectsReserved 已预约机位标记故障：拒绝且状态不变。
func TestMarkStationFaultRejectsReserved(t *testing.T) {
	db := newFaultTestDB(t)
	svc, _, stationRepo, _, _, _ := buildFaultServices(db)

	station := &model.Station{Name: "S5", Area: "A", StationType: "seat", PricePerHour: 10, Status: constants.StationReserved}
	mustCreate(t, db, station)
	if _, err := svc.MarkStationFault(station.ID); err == nil {
		t.Fatal("expected conflict error for reserved station via fault action")
	}
	st, _ := stationRepo.FindByID(station.ID)
	if st.Status != constants.StationReserved {
		t.Errorf("reserved station should stay reserved, got %s", st.Status)
	}
}

// TestMarkStationFaultRollback 任一步失败整体回滚：令最后一步机位落库失败，
// 已回补的余额、已返还的时长包、上机中断状态必须一并回滚。
func TestMarkStationFaultRollback(t *testing.T) {
	db := newFaultTestDB(t)
	svc, _, stationRepo, userRepo, userPkgRepo, _ := buildFaultServices(db)

	now := time.Now()
	user := &model.User{Username: "m9", Role: "member", Balance: 50, Status: "active"}
	mustCreate(t, db, user)
	station := &model.Station{Name: "S9", Area: "A", StationType: "seat", PricePerHour: 10, Status: constants.StationUsing}
	mustCreate(t, db, station)
	future := now.Add(30 * 24 * time.Hour)
	pkg := &model.UserPackage{UserID: user.ID, PackageID: 9, TotalHours: 2, RemainingHours: 1, ExpireAt: &future, Status: "active"}
	mustCreate(t, db, pkg)
	sess := &model.Session{UserID: user.ID, StationID: station.ID, StartTime: now.Add(-30 * time.Minute), GameType: "other", Status: constants.SessionActive}
	mustCreate(t, db, sess)
	// 已扣：余额 ¥10 + 有效包 1 小时；实际 30 分钟应付 ¥5，多扣 ¥15（余额退 ¥10、包退 0.5h）。
	seedConsumeLeg(t, db, &model.WalletTransaction{UserID: user.ID, ChangeType: constants.WalletChangeConsume, AccountType: "balance", Direction: "debit", Amount: -10, SessionID: sess.ID})
	seedConsumeLeg(t, db, &model.WalletTransaction{UserID: user.ID, ChangeType: constants.WalletChangeConsume, AccountType: "package", Direction: "debit", Hours: -1, UserPackageID: pkg.ID, SessionID: sess.ID})

	// 注入回调：机位状态落库（事务最后一步）必然失败。
	if err := db.Callback().Update().Before("gorm:update").Register("fault_test_force_fail", func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "stations" {
			tx.AddError(errInjected)
		}
	}); err != nil {
		t.Fatalf("register callback: %v", err)
	}

	if _, err := svc.MarkStationFault(station.ID); err == nil {
		t.Fatal("expected error when final station save fails")
	}

	// 余额回滚：仍为 50（退费 +10 被撤销）。
	u, _ := userRepo.FindByID(user.ID)
	if u.Balance != 50 {
		t.Errorf("balance not rolled back: %v, want 50", u.Balance)
	}
	// 时长包回滚：remaining 仍为 1（返还 0.5 被撤销）。
	up, _ := userPkgRepo.FindByID(pkg.ID)
	if up.RemainingHours != 1 {
		t.Errorf("package hours not rolled back: %v, want 1", up.RemainingHours)
	}
	// 机位回滚：仍使用中。
	st, _ := stationRepo.FindByID(station.ID)
	if st.Status != constants.StationUsing {
		t.Errorf("station not rolled back: %s, want using", st.Status)
	}
	// 上机回滚：仍 active，无结束时间/结算金额。
	var got model.Session
	db.First(&got, sess.ID)
	if got.Status != constants.SessionActive || got.EndTime != nil || got.DurationMinutes != 0 || got.Amount != 0 {
		t.Errorf("session not rolled back: %+v", got)
	}
	// 退费流水回滚：只保留 2 条原始消费流水。
	var credits int64
	db.Model(&model.WalletTransaction{}).Where("session_id = ? AND direction = ?", sess.ID, constants.WalletDirCredit).Count(&credits)
	if credits != 0 {
		t.Errorf("refund ledger not rolled back, credit rows = %d, want 0", credits)
	}
}

var errInjected = errors.New("injected station save failure")

func mustCreate(t *testing.T, db *gorm.DB, v any) {
	t.Helper()
	if err := db.Create(v).Error; err != nil {
		t.Fatalf("seed create: %v", err)
	}
}
