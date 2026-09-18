package service

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/esportsbar/backend/internal/constants"
	"github.com/esportsbar/backend/internal/dto"
	"github.com/esportsbar/backend/internal/model"
	"github.com/esportsbar/backend/internal/repository"
)

func newInterruptTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_busy_timeout=5000"), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Station{}, &model.TimePackage{}, &model.UserPackage{},
		&model.Recharge{}, &model.PackageOrder{}, &model.Reservation{}, &model.Session{},
		&model.SessionCharge{}, &model.WalletFlow{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 每个用例独立内存库：清空共享库内数据。
	db.Exec("DELETE FROM wallet_flows")
	db.Exec("DELETE FROM session_charges")
	db.Exec("DELETE FROM sessions")
	db.Exec("DELETE FROM user_packages")
	db.Exec("DELETE FROM stations")
	db.Exec("DELETE FROM users")
	return db
}

func setupInterruptScenario(t *testing.T, db *gorm.DB, now time.Time) (*model.User, *model.Station, *model.Session, *model.UserPackage, *model.UserPackage) {
	t.Helper()
	member := &model.User{Username: "m_it", Password: "x", Role: "member", Balance: 100, Status: "active"}
	if err := db.Create(member).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	station := &model.Station{Name: "A-IT", Area: "A区", StationType: "seat", PricePerHour: 10, Status: constants.StationUsing}
	if err := db.Create(station).Error; err != nil {
		t.Fatalf("create station: %v", err)
	}
	sess := &model.Session{UserID: member.ID, StationID: station.ID, StartTime: now.Add(-60 * time.Minute), Status: constants.SessionActive, GameType: constants.GameOther}
	if err := db.Create(sess).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	// 包1：续费期间扣了 1 小时，上机期间过期（start 后 30 分钟到期）。
	expire1 := now.Add(-30 * time.Minute)
	pkg1 := &model.UserPackage{UserID: member.ID, PackageID: 1, PackageName: "10小时包", TotalHours: 10, RemainingHours: 9, ExpireAt: &expire1, Status: "expired"}
	if err := db.Create(pkg1).Error; err != nil {
		t.Fatalf("create pkg1: %v", err)
	}
	// 包2：续费期间扣了 1 小时，仍未过期。
	expire2 := now.Add(7 * 24 * time.Hour)
	pkg2 := &model.UserPackage{UserID: member.ID, PackageID: 2, PackageName: "30小时包", TotalHours: 30, RemainingHours: 29, ExpireAt: &expire2, Status: "active"}
	if err := db.Create(pkg2).Error; err != nil {
		t.Fatalf("create pkg2: %v", err)
	}
	// 模拟一次续费 3 小时的扣费：1h 包1（过期中）+ 1h 包2 + 1h 余额 10 元。
	charges := []model.SessionCharge{
		{SessionID: sess.ID, UserID: member.ID, UserPackageID: pkg1.ID, PackageID: 1, PackageName: pkg1.PackageName, Kind: constants.ChargeKindPackage, Hours: 1, Amount: 10, PricePerHour: 10, BizType: constants.FlowBizSessionRenew, PackageExpireAt: &expire1},
		{SessionID: sess.ID, UserID: member.ID, UserPackageID: pkg2.ID, PackageID: 2, PackageName: pkg2.PackageName, Kind: constants.ChargeKindPackage, Hours: 1, Amount: 10, PricePerHour: 10, BizType: constants.FlowBizSessionRenew, PackageExpireAt: &expire2},
		{SessionID: sess.ID, UserID: member.ID, Kind: constants.ChargeKindBalance, Amount: 10, PricePerHour: 10, BizType: constants.FlowBizSessionRenew},
	}
	for i := range charges {
		if err := db.Create(&charges[i]).Error; err != nil {
			t.Fatalf("create charge: %v", err)
		}
	}
	// 余额扣费已落账：100 - 10 = 90。
	if err := db.Model(&model.User{}).Where("id = ?", member.ID).Update("balance", 90).Error; err != nil {
		t.Fatalf("deduct balance: %v", err)
	}
	return member, station, sess, pkg1, pkg2
}

func newInterruptServices(db *gorm.DB) (*StationService, *SessionService) {
	logger := newTestLogger()
	stationSvc := NewStationService(repository.NewStationRepository(db), db, logger)
	sessionSvc := NewSessionService(
		repository.NewSessionRepository(db),
		repository.NewSessionChargeRepository(db),
		repository.NewWalletFlowRepository(db),
		stationSvc,
		repository.NewUserPackageRepository(db),
		repository.NewUserRepository(db),
		repository.NewReservationRepository(db),
		db, logger,
	)
	stationSvc.SetFaultInterrupter(sessionSvc)
	return stationSvc, sessionSvc
}

// TestInterruptByFaultFullRefund 使用中机位标记故障：余额原额回补、有效包返还小时、上机期间过期包折算余额，
// 机位停在故障，上机置为 interrupted，流水与明细齐全。
func TestInterruptByFaultFullRefund(t *testing.T) {
	db := newInterruptTestDB(t)
	now := time.Now()
	member, station, sess, pkg1, pkg2 := setupInterruptScenario(t, db, now)
	stationSvc, _ := newInterruptServices(db)

	res, err := stationSvc.UpdateStatus(station.ID, &dto.UpdateStationStatusReq{Status: constants.StationFault})
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	if res.Station.Status != constants.StationFault {
		t.Fatalf("station status = %s, want fault（机位停在故障而不是空闲）", res.Station.Status)
	}
	if res.Interrupt == nil {
		t.Fatal("interrupt result missing")
	}
	it := res.Interrupt
	if it.SessionID != sess.ID {
		t.Fatalf("interrupt session id = %d, want %d", it.SessionID, sess.ID)
	}
	// 回补余额 = 余额原扣 10 + 过期包 1h 折算 10 = 20；账户 90 + 20 = 110。
	if it.RefundBalance != 20 {
		t.Fatalf("refund balance = %f, want 20", it.RefundBalance)
	}
	if it.RefundedHours != 1 {
		t.Fatalf("refunded hours = %f, want 1", it.RefundedHours)
	}
	if it.ExpiredHours != 1 || it.ExpiredCash != 10 {
		t.Fatalf("expired hours/cash = %f/%f, want 1/10", it.ExpiredHours, it.ExpiredCash)
	}
	var gotUser model.User
	db.First(&gotUser, member.ID)
	if gotUser.Balance != 110 {
		t.Fatalf("member balance = %f, want 110", gotUser.Balance)
	}
	// 有效包返还 1 小时并保持 active：29 + 1 = 30。
	var gotPkg2 model.UserPackage
	db.First(&gotPkg2, pkg2.ID)
	if gotPkg2.RemainingHours != 30 || gotPkg2.Status != "active" {
		t.Fatalf("pkg2 = %+v, want 30 hours active", gotPkg2)
	}
	// 过期包不返还小时，保持 expired 与 9 小时。
	var gotPkg1 model.UserPackage
	db.First(&gotPkg1, pkg1.ID)
	if gotPkg1.RemainingHours != 9 || gotPkg1.Status != "expired" {
		t.Fatalf("pkg1 = %+v, want 9 hours expired", gotPkg1)
	}
	// 上机记录：interrupted、金额 0、实际使用分钟已记。
	var gotSess model.Session
	db.First(&gotSess, sess.ID)
	if gotSess.Status != constants.SessionInterrupted {
		t.Fatalf("session status = %s, want interrupted", gotSess.Status)
	}
	if gotSess.Amount != 0 {
		t.Fatalf("session amount = %f, want 0（故障补偿）", gotSess.Amount)
	}
	if gotSess.DurationMinutes < 59 || gotSess.DurationMinutes > 61 {
		t.Fatalf("duration = %d, want ~60 实际使用分钟", gotSess.DurationMinutes)
	}
	if gotSess.EndTime == nil {
		t.Fatal("session end time missing")
	}
	// 流水：3 条退费（余额/小时/折算），明细全部 refunded。
	var flows []model.WalletFlow
	if err := db.Where("session_id = ? AND direction = ?", sess.ID, constants.FlowDirectionRefund).Find(&flows).Error; err != nil {
		t.Fatalf("query flows: %v", err)
	}
	if len(flows) != 3 {
		t.Fatalf("refund flows = %d, want 3", len(flows))
	}
	var openCharges int64
	db.Model(&model.SessionCharge{}).Where("session_id = ? AND refunded = ?", sess.ID, false).Count(&openCharges)
	if openCharges != 0 {
		t.Fatalf("unrefunded charges = %d, want 0", openCharges)
	}
}

// TestInterruptIdempotent 同一条上机记录重复中断只结算一次。
func TestInterruptIdempotent(t *testing.T) {
	db := newInterruptTestDB(t)
	now := time.Now()
	_, station, sess, _, _ := setupInterruptScenario(t, db, now)
	stationSvc, _ := newInterruptServices(db)

	if _, err := stationSvc.UpdateStatus(station.ID, &dto.UpdateStationStatusReq{Status: constants.StationFault}); err != nil {
		t.Fatalf("first fault: %v", err)
	}
	// 故障机位再次标记故障（同状态）允许幂等通过；直接调用中断也不应二次结算。
	err := db.Transaction(func(tx *gorm.DB) error {
		r2, err := stationSvc.interrupter.InterruptByStation(tx, station.ID)
		if err != nil {
			return err
		}
		if r2 == nil || !r2.AlreadyInterrupted {
			t.Fatalf("repeat interrupt should be idempotent, got %+v", r2)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("repeat interrupt: %v", err)
	}
	// 退费流水仍只有 3 条，余额未被二次回补。
	var flowCount int64
	db.Model(&model.WalletFlow{}).Where("session_id = ? AND direction = ?", sess.ID, constants.FlowDirectionRefund).Count(&flowCount)
	if flowCount != 3 {
		t.Fatalf("refund flows after repeat = %d, want 3", flowCount)
	}
	var u model.User
	db.First(&u, sess.UserID)
	if u.Balance != 110 {
		t.Fatalf("balance after repeat = %f, want 110（不能重复退费）", u.Balance)
	}
}

// TestInterruptRollbackAllOrNothing 任一步失败全部回滚：机位状态、余额、小时、流水都不变。
func TestInterruptRollbackAllOrNothing(t *testing.T) {
	db := newInterruptTestDB(t)
	now := time.Now()
	_, station, sess, _, pkg2 := setupInterruptScenario(t, db, now)
	stationSvc, _ := newInterruptServices(db)

	// 让回补余额必然失败：删除会员账户行（UpdateBalanceTx 找不到用户）。
	if err := db.Delete(&model.User{}, sess.UserID).Error; err != nil {
		t.Fatalf("delete user: %v", err)
	}
	_, err := stationSvc.UpdateStatus(station.ID, &dto.UpdateStationStatusReq{Status: constants.StationFault})
	if err == nil {
		t.Fatal("expected interrupt tx failure")
	}
	// 机位仍为 using，没有停在 fault。
	var st model.Station
	db.First(&st, station.ID)
	if st.Status != constants.StationUsing {
		t.Fatalf("station status after rollback = %s, want using", st.Status)
	}
	// 上机仍为 active，未结算。
	var ss model.Session
	db.First(&ss, sess.ID)
	if ss.Status != constants.SessionActive {
		t.Fatalf("session status after rollback = %s, want active", ss.Status)
	}
	// 时长包小时未变，无退费流水。
	var p2 model.UserPackage
	db.First(&p2, pkg2.ID)
	if p2.RemainingHours != 29 {
		t.Fatalf("pkg2 hours after rollback = %f, want 29", p2.RemainingHours)
	}
	var flowCount int64
	db.Model(&model.WalletFlow{}).Where("session_id = ?", sess.ID).Count(&flowCount)
	if flowCount != 0 {
		t.Fatalf("wallet flows after rollback = %d, want 0", flowCount)
	}
	var openCharges int64
	db.Model(&model.SessionCharge{}).Where("session_id = ? AND refunded = ?", sess.ID, false).Count(&openCharges)
	if openCharges != 3 {
		t.Fatalf("unrefunded charges after rollback = %d, want 3", openCharges)
	}
}

// TestInterruptIdleStationNoSession 空闲机位标记故障不受影响（无进行中上机）。
func TestInterruptIdleStationNoSession(t *testing.T) {
	db := newInterruptTestDB(t)
	st := &model.Station{Name: "A-IDLE", Area: "A区", StationType: "seat", PricePerHour: 8, Status: constants.StationIdle}
	if err := db.Create(st).Error; err != nil {
		t.Fatalf("create station: %v", err)
	}
	svc, _ := newInterruptServices(db)
	res, err := svc.UpdateStatus(st.ID, &dto.UpdateStationStatusReq{Status: constants.StationFault})
	if err != nil {
		t.Fatalf("idle->fault: %v", err)
	}
	if res.Station.Status != constants.StationFault || res.Interrupt != nil {
		t.Fatalf("idle station fault result = %+v", res)
	}
}
