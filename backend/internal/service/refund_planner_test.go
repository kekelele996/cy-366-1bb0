package service

import (
	"testing"

	"github.com/esportsbar/backend/internal/constants"
	"github.com/esportsbar/backend/internal/model"
)

func leg(seq uint, account, dir string, amount, hours float64, pkgID uint) model.WalletTransaction {
	w := model.WalletTransaction{
		ID:            seq,
		AccountType:   account,
		Direction:     dir,
		Amount:        amount,
		Hours:         hours,
		UserPackageID: pkgID,
	}
	return w
}

func TestPlanRefund(t *testing.T) {
	const price = 10.0 // ¥10/小时
	cases := []struct {
		name           string
		legs           []model.WalletTransaction
		pkgs           map[uint]packageSnapshot
		minutes        int
		wantRefundBal  float64
		wantRefundHrs  float64
		wantExpiredHrs float64
		wantExpiredBal float64
		wantActual     float64
		wantOverpaid   float64
		legCount       int
	}{
		{
			name: "余额原额回补_无时长包",
			// 续费扣 20 元余额（2 小时），实际使用 1 小时应付 10，多扣 10；余额优先回补 10
			legs: []model.WalletTransaction{
				leg(1, constants.WalletAccountBalance, constants.WalletDirDebit, -20, 0, 0),
			},
			minutes:        60,
			wantRefundBal:  10,
			wantRefundHrs:  0,
			wantExpiredHrs: 0,
			wantExpiredBal: 0,
			wantActual:     10,
			wantOverpaid:   10,
			legCount:       0,
		},
		{
			name: "时长包按原顺序返还小时",
			// 两个包各扣 1 小时（共 2 小时 ¥20），实际 1 小时应付 ¥10，多扣 1 小时
			legs: []model.WalletTransaction{
				leg(1, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 101),
				leg(2, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 102),
			},
			pkgs: map[uint]packageSnapshot{
				101: {ID: 101, Expired: false},
				102: {ID: 102, Expired: false},
			},
			minutes:        60,
			wantRefundBal:  0,
			wantRefundHrs:  1,
			wantExpiredHrs: 0,
			wantExpiredBal: 0,
			wantActual:     10,
			wantOverpaid:   10,
			legCount:       1,
		},
		{
			name: "上机期间过期的包折算余额_不返还小时",
			// 第一个包（先扣、已过期）1 小时 + 第二个包（有效）1 小时，实际 1 小时，多扣 1 小时
			// 退费按原顺序先遇到过期包 101：不返还小时，折算 ¥10 余额
			legs: []model.WalletTransaction{
				leg(1, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 101),
				leg(2, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 102),
			},
			pkgs: map[uint]packageSnapshot{
				101: {ID: 101, Expired: true},
				102: {ID: 102, Expired: false},
			},
			minutes:        60,
			wantRefundBal:  0,
			wantRefundHrs:  0,
			wantExpiredHrs: 1,
			wantExpiredBal: 10,
			wantActual:     10,
			wantOverpaid:   10,
			legCount:       1,
		},
		{
			name: "混合_余额加时长包_部分过期",
			// 扣 1 小时有效包(201) + 1 小时过期包(202) + ¥10 余额 = 共 3 小时价值 ¥30
			// 实际 1 小时应付 ¥10，多扣 ¥20
			// 余额原额退 ¥10；剩 ¥10 多扣按顺序：201 有效退 1 小时（¥10），额度耗尽，202 不处理
			legs: []model.WalletTransaction{
				leg(1, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 201),
				leg(2, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 202),
				leg(3, constants.WalletAccountBalance, constants.WalletDirDebit, -10, 0, 0),
			},
			pkgs: map[uint]packageSnapshot{
				201: {ID: 201, Expired: false},
				202: {ID: 202, Expired: true},
			},
			minutes:        60,
			wantRefundBal:  10,
			wantRefundHrs:  1,
			wantExpiredHrs: 0,
			wantExpiredBal: 0,
			wantActual:     10,
			wantOverpaid:   20,
			legCount:       1,
		},
		{
			name: "多扣额度落在过期包与有效包之间",
			// 3 个包各 1 小时，实际 1 小时，多扣 2 小时 = ¥20
			// 顺序：301 过期(¥10 折算余额) -> 302 有效(退 1 小时) -> 303 不再处理
			legs: []model.WalletTransaction{
				leg(1, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 301),
				leg(2, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 302),
				leg(3, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 303),
			},
			pkgs: map[uint]packageSnapshot{
				301: {ID: 301, Expired: true},
				302: {ID: 302, Expired: false},
				303: {ID: 303, Expired: false},
			},
			minutes:        60,
			wantRefundBal:  0,
			wantRefundHrs:  1,
			wantExpiredHrs: 1,
			wantExpiredBal: 10,
			wantActual:     10,
			wantOverpaid:   20,
			legCount:       2,
		},
		{
			name: "已扣余额属于多扣部分时原额回补",
			// 续费 1 小时扣 ¥10 余额 + 两个包各 1 小时，实际使用 30 分钟应付 ¥5，多扣 ¥25
			// 余额 ¥10 全额原额回补；剩 ¥15 由时长包按顺序：包1 退 1 小时、包2 退 0.5 小时
			legs: []model.WalletTransaction{
				leg(1, constants.WalletAccountBalance, constants.WalletDirDebit, -10, 0, 0),
				leg(2, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 501),
				leg(3, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 502),
			},
			pkgs: map[uint]packageSnapshot{
				501: {ID: 501, Expired: false},
				502: {ID: 502, Expired: false},
			},
			minutes:        30,
			wantRefundBal:  10,
			wantRefundHrs:  1.5,
			wantExpiredHrs: 0,
			wantExpiredBal: 0,
			wantActual:     5,
			wantOverpaid:   25,
			legCount:       2,
		},
		{
			name: "实际费用超过已扣_不退不补",
			// 预付 1 小时，实际使用 2 小时（超时未续费），应付 ¥20 > 已扣 ¥10，不多扣不退
			legs: []model.WalletTransaction{
				leg(1, constants.WalletAccountPackage, constants.WalletDirDebit, 0, -1, 401),
			},
			pkgs: map[uint]packageSnapshot{
				401: {ID: 401, Expired: false},
			},
			minutes:        120,
			wantRefundBal:  0,
			wantRefundHrs:  0,
			wantExpiredHrs: 0,
			wantExpiredBal: 0,
			wantActual:     20,
			wantOverpaid:   0,
			legCount:       0,
		},
		{
			name:           "无任何扣费流水",
			legs:           nil,
			minutes:        30,
			wantRefundBal:  0,
			wantRefundHrs:  0,
			wantExpiredHrs: 0,
			wantExpiredBal: 0,
			wantActual:     5,
			wantOverpaid:   0,
			legCount:       0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planRefund(tc.legs, tc.pkgs, price, tc.minutes)
			if got.RefundBalance != tc.wantRefundBal {
				t.Errorf("RefundBalance = %v, want %v", got.RefundBalance, tc.wantRefundBal)
			}
			if got.RefundHours != tc.wantRefundHrs {
				t.Errorf("RefundHours = %v, want %v", got.RefundHours, tc.wantRefundHrs)
			}
			if got.ExpiredHours != tc.wantExpiredHrs {
				t.Errorf("ExpiredHours = %v, want %v", got.ExpiredHours, tc.wantExpiredHrs)
			}
			if got.ExpiredBalance != tc.wantExpiredBal {
				t.Errorf("ExpiredBalance = %v, want %v", got.ExpiredBalance, tc.wantExpiredBal)
			}
			if got.ActualCost != tc.wantActual {
				t.Errorf("ActualCost = %v, want %v", got.ActualCost, tc.wantActual)
			}
			if got.Overpaid != tc.wantOverpaid {
				t.Errorf("Overpaid = %v, want %v", got.Overpaid, tc.wantOverpaid)
			}
			if len(got.Legs) != tc.legCount {
				t.Errorf("len(Legs) = %d, want %d", len(got.Legs), tc.legCount)
			}
		})
	}
}
