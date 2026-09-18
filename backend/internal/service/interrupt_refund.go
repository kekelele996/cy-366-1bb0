package service

import (
	"fmt"
	"time"

	"github.com/esportsbar/backend/internal/constants"
	"github.com/esportsbar/backend/internal/model"
)

// interruptRefundItem 单条扣费明细的故障中断回退方案。
type interruptRefundItem struct {
	ChargeID       uint
	UserPackageID  uint
	PackageName    string
	ChargeKind     string  // balance/package
	Hours          float64 // 原扣小时（时长包明细）
	Cash           float64 // 货币价值：余额明细为原扣金额，时长包明细为小时折算金额
	RefundKind     string  // balance/hours/expired_cash
}

// interruptRefundPlan 一条上机记录的完整回退方案。
type interruptRefundPlan struct {
	Items              []interruptRefundItem
	TotalBalanceRefund float64 // 需要一次性回补到会员余额的总额（余额原额 + 过期包折算 + 兜底）
}

// buildInterruptRefundPlan 依据扣费明细按原扣顺序生成故障中断回退方案。
//
// 规则：
//  1. 余额扣费：已扣余额原额回补（RefundKindBalance）。
//  2. 时长包小时：按原始扣减顺序（charges 已按 id 升序）返还；
//     上机期间已过期的时长包（过期时间落在 (sessionStart, interruptAt] 内）不返还小时，
//     折算成等额余额（按扣费时的时价快照计算，RefundKindExpiredCash）。
//  3. 上机开始前就已过期的时长包不属于“上机期间已过期”，正常返还小时。
func buildInterruptRefundPlan(charges []model.SessionCharge, sessionStart, interruptAt time.Time) interruptRefundPlan {
	plan := interruptRefundPlan{Items: make([]interruptRefundItem, 0, len(charges))}
	for _, c := range charges {
		item := interruptRefundItem{
			ChargeID:      c.ID,
			UserPackageID: c.UserPackageID,
			PackageName:   c.PackageName,
			ChargeKind:    c.Kind,
			Hours:         c.Hours,
			Cash:          c.Amount,
		}
		switch c.Kind {
		case constants.ChargeKindPackage:
			if c.PackageExpireAt != nil &&
				c.PackageExpireAt.After(sessionStart) &&
				!c.PackageExpireAt.After(interruptAt) {
				item.RefundKind = constants.RefundKindExpiredCash
				plan.TotalBalanceRefund += c.Amount
			} else {
				item.RefundKind = constants.RefundKindHours
			}
		default:
			item.RefundKind = constants.RefundKindBalance
			plan.TotalBalanceRefund += c.Amount
		}
		plan.Items = append(plan.Items, item)
	}
	return plan
}

// interruptRefundRemark 生成回退流水文案（含实体名/字段名，便于审计）。
func interruptRefundRemark(item *interruptRefundItem, sessionID uint) string {
	switch item.RefundKind {
	case constants.RefundKindHours:
		return fmt.Sprintf("机位故障中断上机 #%d：时长包「%s」返还 %.2f 小时", sessionID, item.PackageName, item.Hours)
	case constants.RefundKindExpiredCash:
		return fmt.Sprintf("机位故障中断上机 #%d：时长包「%s」上机期间已过期，%.2f 小时折算余额 ¥%.2f", sessionID, item.PackageName, item.Hours, item.Cash)
	default:
		return fmt.Sprintf("机位故障中断上机 #%d：已扣余额原额回补 ¥%.2f", sessionID, item.Cash)
	}
}
