package service

import (
	"testing"
	"time"

	"github.com/esportsbar/backend/internal/constants"
	"github.com/esportsbar/backend/internal/model"
)

// TestBuildInterruptRefundPlan 故障中断退费方案：余额原额回补、时长包按原扣顺序返还、
// 上机期间过期的时长包折算等额余额、上机开始前过期的时长包仍返还小时。
func TestBuildInterruptRefundPlan(t *testing.T) {
	sessionStart := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	interruptAt := sessionStart.Add(90 * time.Minute)
	duringExpire := sessionStart.Add(30 * time.Minute)  // 上机期间过期
	beforeExpire := sessionStart.Add(-24 * time.Hour)  // 上机开始前已过期
	afterExpire := sessionStart.Add(24 * time.Hour)    // 中断后才过期

	expireDuring := duringExpire
	expireBefore := beforeExpire
	expireAfter := afterExpire

	// 按原始扣减顺序构造明细（id 升序）。
	charges := []model.SessionCharge{
		{ID: 1, UserPackageID: 10, PackageName: "10小时包", Kind: constants.ChargeKindPackage, Hours: 2, Amount: 16, PricePerHour: 8, PackageExpireAt: &expireAfter},
		{ID: 2, UserPackageID: 11, PackageName: "30小时包", Kind: constants.ChargeKindPackage, Hours: 1, Amount: 6, PricePerHour: 6, PackageExpireAt: &expireDuring},
		{ID: 3, Kind: constants.ChargeKindBalance, Amount: 12.5},
		{ID: 4, UserPackageID: 12, PackageName: "月卡", Kind: constants.ChargeKindPackage, Hours: 0.5, Amount: 4, PricePerHour: 8, PackageExpireAt: &expireBefore},
		{ID: 5, UserPackageID: 0, PackageName: "", Kind: constants.ChargeKindPackage, Hours: 0.25, Amount: 2, PricePerHour: 8, PackageExpireAt: nil},
	}

	plan := buildInterruptRefundPlan(charges, sessionStart, interruptAt)

	// 顺序必须与原扣顺序一致。
	wantOrder := []uint{1, 2, 3, 4, 5}
	if len(plan.Items) != len(wantOrder) {
		t.Fatalf("items len = %d, want %d", len(plan.Items), len(wantOrder))
	}
	for i, wantID := range wantOrder {
		if plan.Items[i].ChargeID != wantID {
			t.Fatalf("items[%d].ChargeID = %d, want %d（必须按原扣顺序返还）", i, plan.Items[i].ChargeID, wantID)
		}
	}

	wantKind := map[uint]string{
		1: constants.RefundKindHours,       // 未过期：返还小时
		2: constants.RefundKindExpiredCash, // 上机期间过期：折算余额
		3: constants.RefundKindBalance,     // 余额原额回补
		4: constants.RefundKindHours,       // 上机前已过期不属于“上机期间已过期”：仍返还小时
		5: constants.RefundKindHours,       // 无过期时间：返还小时
	}
	for _, item := range plan.Items {
		if got := wantKind[item.ChargeID]; item.RefundKind != got {
			t.Fatalf("charge %d refund kind = %s, want %s", item.ChargeID, item.RefundKind, got)
		}
	}

	// 余额回补总额 = 余额原扣 12.5 + 过期包折算 6 = 18.5
	if plan.TotalBalanceRefund != 18.5 {
		t.Fatalf("total balance refund = %f, want 18.5", plan.TotalBalanceRefund)
	}
}

// TestBuildInterruptRefundPlanEmpty 无任何扣费明细的进行中上机中断：不产生回补。
func TestBuildInterruptRefundPlanEmpty(t *testing.T) {
	plan := buildInterruptRefundPlan(nil, time.Now(), time.Now().Add(time.Minute))
	if plan.TotalBalanceRefund != 0 || len(plan.Items) != 0 {
		t.Fatalf("empty plan should have no refund, got %+v", plan)
	}
}
