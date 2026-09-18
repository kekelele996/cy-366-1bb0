package service

import "math"

// roundMoney 金额保留两位小数，避免浮点尾差导致费用/余额不平。
func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}

// roundHours 时长包小时保留两位小数。
func roundHours(v float64) float64 {
	return math.Round(v*100) / 100
}
