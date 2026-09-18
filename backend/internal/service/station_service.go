package service

import (
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/esportsbar/backend/internal/constants"
	"github.com/esportsbar/backend/internal/dto"
	"github.com/esportsbar/backend/internal/model"
	"github.com/esportsbar/backend/internal/repository"
	"github.com/esportsbar/backend/internal/util"
)

// StationFaultInterrupter 机位标记故障时的上机中断回调（由 SessionService 实现，避免 service 间循环依赖）。
type StationFaultInterrupter interface {
	// InterruptByStation 在调用方事务内中断机位进行中的上机并回退费用；
	// 机位没有进行中的上机时返回 (nil, nil)；重复中断同一条上机记录只结算一次。
	InterruptByStation(tx *gorm.DB, stationID uint) (*InterruptResult, error)
}

// StationService 机位服务。
type StationService struct {
	stationRepo *repository.StationRepository
	interrupter StationFaultInterrupter
	db          *gorm.DB
	logger      *slog.Logger
}

// NewStationService 构造机位服务。
func NewStationService(stationRepo *repository.StationRepository, db *gorm.DB, logger *slog.Logger) *StationService {
	return &StationService{stationRepo: stationRepo, db: db, logger: logger}
}

// SetFaultInterrupter 注入故障中断回调（main 装配时由 SessionService 注入）。
func (s *StationService) SetFaultInterrupter(interrupter StationFaultInterrupter) {
	s.interrupter = interrupter
}

// Create 创建机位。
func (s *StationService) Create(req *dto.CreateStationReq) (*model.Station, error) {
	station := &model.Station{
		Name:         req.Name,
		Area:         req.Area,
		StationType:  req.StationType,
		PricePerHour: req.PricePerHour,
		Description:  req.Description,
		Status:       constants.StationIdle,
	}
	if err := s.stationRepo.Create(station); err != nil {
		return nil, fmt.Errorf("station create: %w", err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["station_create_ok"], station.Name, station.Area))
	return station, nil
}

// Update 更新机位。
func (s *StationService) Update(id uint, req *dto.UpdateStationReq) (*model.Station, error) {
	station, err := s.stationRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, "机位不存在")
		}
		return nil, fmt.Errorf("station update find: %w", err)
	}
	if req.Name != "" {
		station.Name = req.Name
	}
	if req.Area != "" {
		station.Area = req.Area
	}
	if req.StationType != "" {
		station.StationType = req.StationType
	}
	if req.PricePerHour > 0 {
		station.PricePerHour = req.PricePerHour
	}
	if req.Description != "" {
		station.Description = req.Description
	}
	if err := s.stationRepo.Update(station); err != nil {
		return nil, fmt.Errorf("station update: %w", err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["station_update_ok"], station.ID, "profile"))
	return station, nil
}

// StatusChangeResult 机位状态流转结果：含可能触发的上机中断结算，看板与流水回读一致。
type StatusChangeResult struct {
	Station   *model.Station   `json:"station"`
	Interrupt *InterruptResult `json:"interrupt,omitempty"`
}

// UpdateStatus 机位状态流转：idle<->using/reserved/fault。
// 使用中（using）标记为故障（fault）时，在同一事务内中断进行中的上机、回退费用，
// 机位状态停在故障而不是空闲；费用回补、机位状态、流水任一步失败全部回滚。
func (s *StationService) UpdateStatus(id uint, req *dto.UpdateStationStatusReq) (*StatusChangeResult, error) {
	station, err := s.stationRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, "机位不存在")
		}
		return nil, fmt.Errorf("station status find: %w", err)
	}
	from := station.Status
	if !allowedStationTransition(from, req.Status) {
		return nil, util.NewAppError(constants.CodeConflict,
			fmt.Sprintf("机位状态不允许从 %s 变更为 %s，请先处理当前状态", util.StatusText(from), util.StatusText(req.Status)))
	}

	result := &StatusChangeResult{}
	if from == req.Status {
		// 状态未变化无需事务。
		result.Station = station
		return result, nil
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		locked, err := s.stationRepo.LockByID(tx, id)
		if err != nil {
			return err
		}
		if locked.Status != from {
			return util.NewAppError(constants.CodeConflict, "机位状态已变更，请刷新后重试")
		}
		// 使用中标记故障：同一事务内中断上机并结算退费。
		if from == constants.StationUsing && req.Status == constants.StationFault {
			if s.interrupter == nil {
				return fmt.Errorf("station fault interrupter not wired")
			}
			interrupt, err := s.interrupter.InterruptByStation(tx, id)
			if err != nil {
				return err
			}
			result.Interrupt = interrupt
		}
		locked.Status = req.Status
		if err := tx.Save(locked).Error; err != nil {
			return err
		}
		result.Station = locked
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("station status update tx: %w", err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["station_status_change"], id, from, req.Status, "operator"))
	return result, nil
}

// Delete 删除机位。
func (s *StationService) Delete(id uint) error {
	station, err := s.stationRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("station delete find: %w", err)
	}
	if station.Status == constants.StationUsing {
		return util.NewAppError(constants.CodeStationBusy, "使用中的机位不能删除，请先下机")
	}
	if err := s.stationRepo.Delete(id); err != nil {
		return fmt.Errorf("station delete: %w", err)
	}
	s.logger.Info(fmt.Sprintf(constants.LogTemplates["station_delete_ok"], id))
	return nil
}

// List 分页查询机位。
func (s *StationService) List(query *dto.StationQuery) ([]model.Station, int64, error) {
	page := query.Page
	if page <= 0 {
		page = constants.DefaultPage
	}
	pageSize := query.PageSize
	if pageSize <= 0 {
		pageSize = constants.DefaultPageSize
	}
	return s.stationRepo.List(page, pageSize, query.Area, query.Status)
}

// ListAll 查询全部机位（看板/下拉复用）。
func (s *StationService) ListAll() ([]model.Station, error) {
	return s.stationRepo.ListAll()
}

// GetByID 查询机位详情。
func (s *StationService) GetByID(id uint) (*model.Station, error) {
	station, err := s.stationRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, util.NewAppError(constants.CodeNotFound, "机位不存在")
		}
		return nil, fmt.Errorf("station get: %w", err)
	}
	return station, nil
}

// LockForUpdate 事务内行锁机位，供预约/上机服务复用。
func (s *StationService) LockForUpdate(tx *gorm.DB, id uint) (*model.Station, error) {
	return s.stationRepo.LockByID(tx, id)
}

// allowedStationTransition 机位状态机：using 可由下机释放为 idle，也可因标记故障中断上机进入 fault。
func allowedStationTransition(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case constants.StationIdle:
		return to == constants.StationFault || to == constants.StationReserved || to == constants.StationUsing
	case constants.StationFault:
		return to == constants.StationIdle
	case constants.StationReserved:
		return to == constants.StationIdle || to == constants.StationUsing
	case constants.StationUsing:
		return to == constants.StationIdle || to == constants.StationFault
	}
	return false
}
