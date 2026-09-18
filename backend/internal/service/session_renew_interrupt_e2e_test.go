package service

import (
	"testing"
	"time"

	"github.com/esportsbar/backend/internal/constants"
	"github.com/esportsbar/backend/internal/dto"
	"github.com/esportsbar/backend/internal/model"
)

// TestRenewThenFaultInterruptE2E 真实续费扣费 → 标记故障中断：
// 验证扣费明细按原扣顺序落账，中断时余额原额回补、小时包按顺序返还、上机期间过期的包折算余额。
func TestRenewThenFaultInterruptE2E(t *testing.T) {
	db := newInterruptTestDB(t)
	now := time.Now()

	member := &model.User{Username: "m_e2e", Password: "x", Role: "member", Balance: 100, Status: "active"}
	if err := db.Create(member).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	station := &model.Station{Name: "A-E2E", Area: "A区", StationType: "seat", PricePerHour: 20, Status: constants.StationUsing}
	if err := db.Create(station).Error; err != nil {
		t.Fatalf("create station: %v", err)
	}
	// 上机开始于 30 分钟前。
	sess := &model.Session{UserID: member.ID, StationID: station.ID, StartTime: now.Add(-30 * time.Minute), Status: constants.SessionActive, GameType: constants.GameOther}
	if err := db.Create(sess).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}

	// 场景 1：20 分钟前续费时，会员有一个 0.5h 的短包（续费当时还有 5 分钟才过期），
	// 续费时被扣 0.5h；该包在 15 分钟前（上机期间）过期。用扣费快照还原该历史。
	earlyExpire := now.Add(-15 * time.Minute)
	earlyPkg := &model.UserPackage{UserID: member.ID, PackageID: 1, PackageName: "体验包", TotalHours: 2, RemainingHours: 0.5, ExpireAt: &earlyExpire, Status: "expired"}
	if err := db.Create(earlyPkg).Error; err != nil {
		t.Fatalf("create early pkg: %v", err)
	}
	chargeExpireSnapshot := now.Add(-15 * time.Minute) // 快照：上机期间过期
	earlyCharge := model.SessionCharge{
		SessionID: sess.ID, UserID: member.ID, UserPackageID: earlyPkg.ID, PackageID: 1,
		PackageName: earlyPkg.PackageName, Kind: constants.ChargeKindPackage, Hours: 0.5, Amount: 10,
		PricePerHour: 20, BizType: constants.FlowBizSessionRenew, PackageExpireAt: &chargeExpireSnapshot,
	}
	if err := db.Create(&earlyCharge).Error; err != nil {
		t.Fatalf("create early charge: %v", err)
	}
	if err := db.Create(&model.WalletFlow{
		UserID: member.ID, SessionID: sess.ID, UserPackageID: earlyPkg.ID,
		Direction: constants.FlowDirectionDeduct, Kind: constants.FlowKindPackageHours,
		BizType: constants.FlowBizSessionRenew, Amount: 10, Hours: 0.5, Remark: "历史续费扣体验包",
	}).Error; err != nil {
		t.Fatalf("create early flow: %v", err)
	}

	// 场景 2：此刻再续费 60 分钟，会员有一个未过期月卡（10h 余量），扣 1h；
	// 1h 时价 20，月卡足够，不再扣余额。
	laterExpire := now.Add(30 * 24 * time.Hour)
	laterPkg := &model.UserPackage{UserID: member.ID, PackageID: 2, PackageName: "月卡", TotalHours: 100, RemainingHours: 10, ExpireAt: &laterExpire, Status: "active"}
	if err := db.Create(laterPkg).Error; err != nil {
		t.Fatalf("create later pkg: %v", err)
	}

	_, sessionSvc := newInterruptServices(db)
	if _, err := sessionSvc.Renew(member.ID, sess.ID, &dto.RenewSessionReq{AddMinutes: 60}); err != nil {
		t.Fatalf("renew: %v", err)
	}
	var gotMember model.User
	db.First(&gotMember, member.ID)
	if gotMember.Balance != 100 {
		t.Fatalf("balance after renew = %f, want 100（月卡足够，不应扣余额）", gotMember.Balance)
	}
	var gotLater model.UserPackage
	db.First(&gotLater, laterPkg.ID)
	if gotLater.RemainingHours != 9 {
		t.Fatalf("later pkg remaining = %f, want 9", gotLater.RemainingHours)
	}
	var charges []model.SessionCharge
	db.Order("id ASC").Find(&charges, "session_id = ?", sess.ID)
	if len(charges) != 2 {
		t.Fatalf("charges = %d, want 2", len(charges))
	}
	if charges[0].UserPackageID != earlyPkg.ID || charges[1].UserPackageID != laterPkg.ID {
		t.Fatalf("charges 原扣顺序错误: %d then %d", charges[0].UserPackageID, charges[1].UserPackageID)
	}

	// 管理员标记故障：全链路中断结算。
	stationSvc, _ := newInterruptServices(db)
	stationSvc.SetFaultInterrupter(sessionSvc)
	res, err := stationSvc.UpdateStatus(station.ID, &dto.UpdateStationStatusReq{Status: constants.StationFault})
	if err != nil {
		t.Fatalf("fault: %v", err)
	}
	if res.Station.Status != constants.StationFault {
		t.Fatalf("station = %s, want fault", res.Station.Status)
	}
	it := res.Interrupt
	if it == nil {
		t.Fatal("interrupt result missing")
	}
	// 体验包 0.5h 上机期间过期 → 折算余额 0.5*20=10；月卡 1h 返还小时；无余额原扣。
	if it.RefundBalance != 10 {
		t.Fatalf("refund balance = %f, want 10（过期包折算）", it.RefundBalance)
	}
	if it.RefundedHours != 1 {
		t.Fatalf("refunded hours = %f, want 1", it.RefundedHours)
	}
	if it.ExpiredHours != 0.5 || it.ExpiredCash != 10 {
		t.Fatalf("expired = %fh/¥%f, want 0.5h/¥10", it.ExpiredHours, it.ExpiredCash)
	}
	db.First(&gotMember, member.ID)
	if gotMember.Balance != 110 {
		t.Fatalf("balance after interrupt = %f, want 110（100 + 10 折算）", gotMember.Balance)
	}
	db.First(&gotLater, laterPkg.ID)
	if gotLater.RemainingHours != 10 || gotLater.Status != "active" {
		t.Fatalf("later pkg after interrupt = %+v, want 10h active", gotLater)
	}
	var gotEarly model.UserPackage
	db.First(&gotEarly, earlyPkg.ID)
	if gotEarly.RemainingHours != 0.5 || gotEarly.Status != "expired" {
		t.Fatalf("early pkg after interrupt = %+v, want 0.5h expired（小时不返还）", gotEarly)
	}
	// 流水回读：历史扣 1 + 续费扣 1 + 退 2 = 4 条，且看板机位故障/上机中断一致。
	var allFlows []model.WalletFlow
	if err := db.Order("id ASC").Find(&allFlows, "session_id = ?", sess.ID).Error; err != nil {
		t.Fatalf("list flows: %v", err)
	}
	if len(allFlows) != 4 {
		t.Fatalf("flows total = %d, want 4", len(allFlows))
	}
	var gotSess model.Session
	db.First(&gotSess, sess.ID)
	if gotSess.Status != constants.SessionInterrupted || gotSess.Amount != 0 {
		t.Fatalf("session after interrupt = %+v, want interrupted/amount 0", gotSess)
	}
}
