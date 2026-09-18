package service

import (
	"math"

	"github.com/esportsbar/backend/internal/model"
)

// 退费金额/小时比较与计算的精度阈值。
const moneyEpsilon = 0.005

// refundLeg 一条时长包退费腿的结算结果。
type refundLeg struct {
	UserPackageID uint
	Hours         float64 // 返还小时（按原扣顺序）
	Expired       bool    // 上机期间已过期：不返还小时，折算余额
}

// refundPlan 故障中断重算后的退费方案（纯计算，便于单测）。
type refundPlan struct {
	ChargedBalance float64 // 本次上机累计已扣余额（原额）
	ChargedHours   float64 // 本次上机累计已扣时长包小时
	ActualCost     float64 // 按实际使用分钟重算的应付费用
	Overpaid       float64 // 多扣总额
	RefundBalance  float64 // 退回余额：已扣余额原额回补
	Legs           []refundLeg
	RefundHours    float64 // 返还时长包小时合计
	ExpiredHours   float64 // 已过期时长包被折算的小时合计
	ExpiredBalance float64 // 已过期时长包折算余额合计
}

// packageSnapshot 退费重算时时长包的当前快照（Expired 表示上机期间已过期）。
type packageSnapshot struct {
	ID      uint
	Expired bool
}

// planRefund 依据上机消费流水回读结果重算退费方案。
//
// 结算规则：
//   - 应付 actualCost = 实际使用分钟 × 机位时价 / 60；
//   - 多扣 overpaid = max(0, 已扣总额 - actualCost)，不足部分不补扣；
//   - 已扣余额原额回补（先全额退余额）；
//   - 剩余多扣按“原扣费顺序”逐条处理时长包：上机期间仍有效的时长包返还小时，
//     上机期间已过期的时长包不返还小时、按时价折算成等额余额。
func planRefund(legs []model.WalletTransaction, packages map[uint]packageSnapshot, pricePerHour float64, actualMinutes int) refundPlan {
	plan := refundPlan{Legs: []refundLeg{}}
	plan.ActualCost = roundMoney(float64(actualMinutes) * pricePerHour / 60)

	var packageLegs []model.WalletTransaction
	for _, leg := range legs {
		switch leg.AccountType {
		case "balance":
			plan.ChargedBalance = roundMoney(plan.ChargedBalance + math.Abs(leg.Amount))
		case "package":
			plan.ChargedHours = roundHours(plan.ChargedHours + math.Abs(leg.Hours))
			packageLegs = append(packageLegs, leg)
		}
	}

	chargedTotal := roundMoney(plan.ChargedBalance + plan.ChargedHours*pricePerHour)
	overpaid := roundMoney(chargedTotal - plan.ActualCost)
	if overpaid < 0 {
		overpaid = 0
	}
	plan.Overpaid = overpaid

	// 退费总额必须等于多扣额度（保证会员最终只承担实际使用费）。
	// 分配顺序：已扣余额优先原额回补（以多扣额度为上限），剩余再按原扣费顺序处理时长包。
	remainingOverpaid := overpaid
	if plan.ChargedBalance <= remainingOverpaid {
		plan.RefundBalance = plan.ChargedBalance
	} else {
		plan.RefundBalance = remainingOverpaid
	}
	remainingOverpaid = roundMoney(remainingOverpaid - plan.RefundBalance)

	for _, leg := range packageLegs {
		if remainingOverpaid <= moneyEpsilon {
			break
		}
		chargedHours := math.Abs(leg.Hours)
		hoursValue := roundMoney(chargedHours * pricePerHour)
		covered := hoursValue
		if covered > remainingOverpaid {
			covered = remainingOverpaid
		}
		hours := roundHours(covered / pricePerHour)
		if hours > chargedHours {
			hours = chargedHours
		}
		if hours <= moneyEpsilon {
			break
		}

		snap := packages[leg.UserPackageID]
		rl := refundLeg{UserPackageID: leg.UserPackageID, Hours: hours, Expired: snap.Expired}
		plan.Legs = append(plan.Legs, rl)

		usedValue := roundMoney(hours * pricePerHour)
		remainingOverpaid = roundMoney(remainingOverpaid - usedValue)
		if rl.Expired {
			plan.ExpiredHours = roundHours(plan.ExpiredHours + hours)
			plan.ExpiredBalance = roundMoney(plan.ExpiredBalance + usedValue)
		} else {
			plan.RefundHours = roundHours(plan.RefundHours + hours)
		}
	}
	return plan
}
