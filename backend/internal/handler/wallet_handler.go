package handler

import (
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/esportsbar/backend/internal/constants"
	"github.com/esportsbar/backend/internal/dto"
	"github.com/esportsbar/backend/internal/service"
	"github.com/esportsbar/backend/pkg/response"
)

// WalletHandler 会员资金流水接口处理器。
type WalletHandler struct {
	walletService *service.WalletService
	logger        *slog.Logger
}

// NewWalletHandler 构造资金流水接口处理器。
func NewWalletHandler(walletService *service.WalletService, logger *slog.Logger) *WalletHandler {
	return &WalletHandler{walletService: walletService, logger: logger}
}

// ListMine 查询当前登录会员的资金流水（余额与时长包小时）。
func (h *WalletHandler) ListMine(c *gin.Context) {
	var page dto.PageQuery
	if err := c.ShouldBindQuery(&page); err != nil {
		response.Fail(c, 400, constants.CodeValidation, "资金流水分页参数校验失败："+err.Error())
		return
	}
	userID, _ := c.Get("user_id")
	uid, _ := userID.(uint)
	list, total, err := h.walletService.ListMyTransactions(uid, page.Page, page.PageSize)
	if err != nil {
		h.logger.Error(fmt.Sprintf("wallet handler error: %v", err))
		response.Fail(c, 500, constants.CodeInternal, "服务器内部错误，请稍后重试")
		return
	}
	response.OK(c, dto.PageResult{List: list, Total: total, Page: page.Page, PageSize: page.PageSize})
}
