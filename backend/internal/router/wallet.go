package router

import (
	"github.com/gin-gonic/gin"

	"github.com/esportsbar/backend/internal/handler"
	"github.com/esportsbar/backend/internal/middleware"
)

// RegisterWallet 注册会员资金流水路由。
func RegisterWallet(rg *gin.RouterGroup, h *handler.WalletHandler, jwtSecret string) {
	wallet := rg.Group("/wallet", middleware.Auth(jwtSecret))
	{
		wallet.GET("/transactions", h.ListMine)
	}
}
