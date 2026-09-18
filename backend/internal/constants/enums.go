package constants

// StationStatus 机位/包厢状态枚举。
const (
	StationIdle     = "idle"     // 空闲
	StationUsing    = "using"    // 使用中
	StationFault    = "fault"    // 故障
	StationReserved = "reserved" // 预约
)

// AllStationStatus 所有机位状态。
var AllStationStatus = []string{StationIdle, StationUsing, StationFault, StationReserved}

// IsValidStationStatus 判断机位状态是否合法。
func IsValidStationStatus(s string) bool {
	switch s {
	case StationIdle, StationUsing, StationFault, StationReserved:
		return true
	}
	return false
}

// ReservationStatus 预约状态枚举。
const (
	ReservationPending   = "pending"    // 待确认
	ReservationConfirmed = "confirmed"  // 已确认
	ReservationCheckedIn = "checked_in" // 已到店开机
	ReservationCompleted = "completed"  // 已完成
	ReservationCancelled = "cancelled"  // 已取消
)

// AllReservationStatus 所有预约状态。
var AllReservationStatus = []string{ReservationPending, ReservationConfirmed, ReservationCheckedIn, ReservationCompleted, ReservationCancelled}

// IsValidReservationStatus 判断预约状态是否合法。
func IsValidReservationStatus(s string) bool {
	switch s {
	case ReservationPending, ReservationConfirmed, ReservationCheckedIn, ReservationCompleted, ReservationCancelled:
		return true
	}
	return false
}

// SessionStatus 上机记录状态枚举。
const (
	SessionActive      = "active"      // 进行中
	SessionCompleted   = "completed"   // 已结束（正常下机结算）
	SessionInterrupted = "interrupted" // 已中断（管理员标记机位故障，按实际使用分钟结算）
)

// IsValidSessionStatus 判断上机记录状态是否合法。
func IsValidSessionStatus(s string) bool {
	switch s {
	case SessionActive, SessionCompleted, SessionInterrupted:
		return true
	}
	return false
}

// WalletAccountType 钱包账户类型枚举。
const (
	WalletAccountBalance = "balance" // 余额账户
	WalletAccountPackage = "package" // 时长包账户（小时）
)

// WalletDirection 资金流水方向枚举。
const (
	WalletDirDebit  = "debit"  // 出账（扣减）
	WalletDirCredit = "credit" // 入账（回补/返还）
)

// WalletChangeType 资金流水业务类型枚举。
const (
	WalletChangeRecharge         = "recharge"           // 充值
	WalletChangeBuyPackage       = "buy_package"        // 购买时长包（余额扣款）
	WalletChangeConsume          = "consume"            // 上机消费（续费/下机扣费）
	WalletChangeRefundBalance    = "refund_balance"     // 故障中断退回已扣余额
	WalletChangeRefundPackage    = "refund_package"     // 故障中断返还时长包小时
	WalletChangeExpiredToBalance = "expired_to_balance" // 上机期间已过期时长包折算余额
)

// AllWalletChangeTypes 所有资金流水业务类型。
var AllWalletChangeTypes = []string{
	WalletChangeRecharge, WalletChangeBuyPackage, WalletChangeConsume,
	WalletChangeRefundBalance, WalletChangeRefundPackage, WalletChangeExpiredToBalance,
}

// IsValidWalletChangeType 判断资金流水业务类型是否合法。
func IsValidWalletChangeType(s string) bool {
	for _, v := range AllWalletChangeTypes {
		if s == v {
			return true
		}
	}
	return false
}

// TournamentStatus 赛事状态枚举。
const (
	TournamentDraft    = "draft"    // 草稿
	TournamentOpen     = "open"     // 报名中
	TournamentReady    = "ready"    // 已抽签分组
	TournamentFinished = "finished" // 已结束
)

// AllTournamentStatus 所有赛事状态。
var AllTournamentStatus = []string{TournamentDraft, TournamentOpen, TournamentReady, TournamentFinished}

// IsValidTournamentStatus 判断赛事状态是否合法。
func IsValidTournamentStatus(s string) bool {
	switch s {
	case TournamentDraft, TournamentOpen, TournamentReady, TournamentFinished:
		return true
	}
	return false
}

// PaymentMethod 支付方式枚举。
const (
	PaymentBalance = "balance" // 余额支付
	PaymentCash    = "cash"    // 现金
	PaymentWechat  = "wechat"  // 微信
	PaymentAlipay  = "alipay"  // 支付宝
)

// AllPaymentMethods 所有支付方式。
var AllPaymentMethods = []string{PaymentBalance, PaymentCash, PaymentWechat, PaymentAlipay}

// IsValidPaymentMethod 判断支付方式是否合法。
func IsValidPaymentMethod(s string) bool {
	switch s {
	case PaymentBalance, PaymentCash, PaymentWechat, PaymentAlipay:
		return true
	}
	return false
}

// GameType 游戏类型枚举。
const (
	GameLOL   = "lol"   // 英雄联盟
	GameCSGO  = "csgo"  // CSGO
	GameKOG   = "kog"   // 王者荣耀
	GameOther = "other" // 其他
)

// AllGameTypes 所有游戏类型。
var AllGameTypes = []string{GameLOL, GameCSGO, GameKOG, GameOther}

// IsValidGameType 判断游戏类型是否合法。
func IsValidGameType(s string) bool {
	switch s {
	case GameLOL, GameCSGO, GameKOG, GameOther:
		return true
	}
	return false
}

// RegistrationMode 报名方式枚举。
const (
	RegistrationSolo = "solo" // 个人报名
	RegistrationTeam = "team" // 战队报名
)

// IsValidRegistrationMode 判断报名方式是否合法。
func IsValidRegistrationMode(s string) bool {
	switch s {
	case RegistrationSolo, RegistrationTeam:
		return true
	}
	return false
}

// RegistrationStatus 报名状态枚举。
const (
	RegistrationPending  = "pending"  // 待审核
	RegistrationJoined   = "joined"   // 已通过
	RegistrationRejected = "rejected" // 已拒绝
)

// IsValidRegistrationStatus 判断报名状态是否合法。
func IsValidRegistrationStatus(s string) bool {
	switch s {
	case RegistrationPending, RegistrationJoined, RegistrationRejected:
		return true
	}
	return false
}

// MatchStatus 比赛状态枚举。
const (
	MatchPending  = "pending"  // 未开始
	MatchPlaying  = "playing"  // 进行中
	MatchFinished = "finished" // 已结束
)

// IsValidMatchStatus 判断比赛状态是否合法。
func IsValidMatchStatus(s string) bool {
	switch s {
	case MatchPending, MatchPlaying, MatchFinished:
		return true
	}
	return false
}

// OrderStatus 时长包订单状态枚举。
const (
	OrderPending   = "pending"   // 待支付
	OrderPaid      = "paid"      // 已支付
	OrderCancelled = "cancelled" // 已取消
)

// IsValidOrderStatus 判断订单状态是否合法。
func IsValidOrderStatus(s string) bool {
	switch s {
	case OrderPending, OrderPaid, OrderCancelled:
		return true
	}
	return false
}
