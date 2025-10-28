package lua

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/weibaohui/k8m/internal/dao"
	"github.com/weibaohui/k8m/pkg/comm/utils"
	"github.com/weibaohui/k8m/pkg/constants"
	"github.com/weibaohui/k8m/pkg/models"
	"github.com/weibaohui/k8m/pkg/service"
	"github.com/weibaohui/kom/kom"
	"gorm.io/gorm"
	"k8s.io/klog/v2"
)

type ScheduleBackground struct {
}

var once sync.Once
var localTaskManager *TaskManager

var localScheduleBackground *ScheduleBackground
var sbOnce sync.Once // 仅负责保证 ScheduleBackground 单例非空的 once

// init 在包被导入时执行，用于初始化并启动 TaskManager
// 注意：init 的执行时机由 Go 运行时决定，无法保证一定在其他组件之前；
// TaskManager必须先启动，否则在TM中添加、更新、删除任务时会报错
func init() {
	localTaskManager = NewTaskManager()
	// 启动TaskManager
	localTaskManager.Start()
}
func InitClusterInspection() {
	once.Do(func() {
		// 确保实例非空后再加载 DB 任务
		sb := NewScheduleBackground()
		sb.AddCronJobFromDB()
		klog.V(6).Infof("新增 集群巡检 定时任务 ")
	})
}
func NewScheduleBackground() *ScheduleBackground {
	sbOnce.Do(func() {
		if localScheduleBackground == nil {
			localScheduleBackground = &ScheduleBackground{}
		}
	})
	return localScheduleBackground
}

// TriggerTypeManual 表示手动触发
const TriggerTypeManual = "manual"

// TriggerTypeCron 表示定时触发
const TriggerTypeCron = "cron"

// RunByCluster 启动一次巡检任务，并记录执行及每个脚本的结果到数据库
// scheduleID: 定时任务ID
// cluster: 目标集群
// triggerType: 触发类型（manual/cron）
func (s *ScheduleBackground) RunByCluster(ctx context.Context, scheduleID *uint, cluster string, triggerType string) (*models.InspectionRecord, error) {
	k := kom.Cluster(cluster)
	if k == nil {
		klog.V(6).Infof("巡检 集群【%s】，但是该集群未连接，尝试连接该集群", cluster)
		service.ClusterService().Connect(cluster)
		k = kom.Cluster(cluster)
		if k == nil {
			klog.Errorf("巡检 集群【%s】，但是该集群未连接，尝试连接该集群失败，跳过执行", cluster)
			return nil, fmt.Errorf("巡检 集群【%s】未连接，尝试连接未成功，跳过执行", cluster)
		}
		klog.V(6).Infof("巡检 集群【%s】，已连接", cluster)
	}

	klog.V(6).Infof("开始巡检, scheduleID: %v, cluster: %s", scheduleID, cluster)
	// 如果scheduleID 不为空，
	// 从数据库中读取scheduleName
	var scheduleName string
	if scheduleID == nil {
		return nil, fmt.Errorf("参数错误，scheduleID不能为空")
	}
	schedule := &models.InspectionSchedule{}
	schedule.ID = *scheduleID
	schedule, err := schedule.GetOne(nil)
	if err != nil {
		return nil, fmt.Errorf("根据ID获取巡检计划失败: %w", err)
	}
	scheduleName = schedule.Name

	// 创建一条执行记录
	record := &models.InspectionRecord{
		ScheduleID:   scheduleID,
		ScheduleName: scheduleName,
		Cluster:      cluster,
		TriggerType:  triggerType,
		Status:       "running",
		StartTime:    time.Now(),
	}

	if err := record.Save(nil); err != nil {
		return nil, fmt.Errorf("保存巡检计划执行记录失败: %w", err)
	}

	// 执行所有巡检脚本
	inspection := NewLuaInspection(schedule, cluster)
	results := inspection.Start()

	var scriptResults []*models.InspectionScriptResult
	var checkEvents []*models.InspectionCheckEvent
	var errorCount int
	for _, res := range results {
		result := models.InspectionScriptResult{
			RecordID:   record.ID,
			ScheduleID: scheduleID,
			ScriptName: res.Name,
			StartTime:  res.StartTime,
			EndTime:    res.EndTime,
			StdOutput:  res.LuaRunOutput,
			Cluster:    cluster,
		}
		if res.LuaRunError != nil {
			result.ErrorMsg = res.LuaRunError.Error()
		}
		scriptResults = append(scriptResults, &result)
		for _, e := range res.Events {
			ce := &models.InspectionCheckEvent{
				RecordID:    record.ID,
				ScheduleID:  scheduleID,
				EventStatus: e.Status,
				EventMsg:    e.Msg,
				Extra:       utils.ToJSON(e.Extra),
				ScriptName:  e.ScriptName,
				Kind:        e.Kind,
				CheckDesc:   e.CheckDesc,
				Namespace:   e.Namespace,
				Name:        e.Name,
				Cluster:     cluster,
			}
			if s.IsEventStatusPass(e.Status) {
				ce.EventStatus = string(constants.LuaEventStatusNormal) // 统一状态描述为正常
			} else {
				errorCount += 1
				ce.EventStatus = string(constants.LuaEventStatusFailed)
			}
			checkEvents = append(checkEvents, ce)

		}
	}
	// 保存脚本运行中产生的事件记录
	_ = dao.GenericBatchSave(nil, checkEvents, 100)
	// 保存脚本本身执行结果
	_ = dao.GenericBatchSave(nil, scriptResults, 100)

	// 统计错误数

	//  更新执行记录
	endTime := time.Now()
	record.Status = "success"
	record.EndTime = &endTime
	record.ErrorCount = errorCount

	_ = record.Save(nil)

	klog.V(6).Infof("集群巡检完成。集群巡检记录ID=%d", record.ID)

	// 更新集群巡检计划运行结果
	schedule.LastRunTime = &endTime
	schedule.ErrorCount = errorCount
	_ = schedule.Save(nil, func(db *gorm.DB) *gorm.DB {
		return db.Select("last_run_time", "error_count")
	})

	// 自动生成总结，包括使用AI
	s.AutoGenerateSummary(record.ID)

	// 发送webhook通知
	go func() {
		_, _ = s.PushToHooksByRecordID(record.ID)
	}()

	return record, nil
}

// IsEventStatusPass 判断事件状态是否为通过
// 这里的通过状态包括：正常、pass、ok、success、通过
// 入库前将状态描述文字统一为正常、失败两种
func (s *ScheduleBackground) IsEventStatusPass(status string) bool {
	return status == "正常" || status == "pass" || status == "ok" || status == "success" || status == "通过"
}

// AddCronJobFromDB  后台自动执行调度
func (s *ScheduleBackground) AddCronJobFromDB() {

	// 1、读取数据库中的定义，然后创建
	sch := models.InspectionSchedule{}

	list, _, err := sch.List(nil, func(db *gorm.DB) *gorm.DB {
		return db.Where("enabled is true")
	})
	if err != nil {
		klog.Errorf("读取定时任务失败%v", err)
		return
	}
	var count int
	for _, schedule := range list {
		s.Add(schedule.ID)
		count += 1
	}
	klog.V(6).Infof("启动集群巡检任务完成，共启动%d个", count)
}
func (s *ScheduleBackground) Remove(scheduleID uint) {
	localTaskManager.Remove(fmt.Sprintf("%d", scheduleID))
	klog.V(6).Infof("移除巡检任务[id=%d]", scheduleID)
}

func (s *ScheduleBackground) Add(scheduleID uint) {
	// 1、读取数据库中的定义，然后创建
	sch := models.InspectionSchedule{}
	sch.ID = scheduleID
	item, err := sch.GetOne(nil, func(db *gorm.DB) *gorm.DB {
		return db.Where("enabled is true")
	})
	if err != nil {
		klog.Errorf("读取巡检任务[id=%d]失败  %v", scheduleID, err)
		return
	}

	if item.Cron != "" {
		// 创建局部副本以避免闭包变量捕获问题
		// 这样确保每个定时任务都有自己独立的数据副本，不会被后续的Add调用影响
		scheduleIDCopy := item.ID
		clustersCopy := item.Clusters
		cronExpr := item.Cron

		// 添加定时任务到TaskManager，而不是立即执行
		addErr := localTaskManager.Add(fmt.Sprintf("%d", scheduleIDCopy), cronExpr, func(ctx context.Context) {
			klog.V(6).Infof("定时巡检任务 [%s] 开始执行", item.Name)
			// 执行巡检任务：逐项检查取消信号，并清洗 cluster 列表
			clusters := strings.Split(clustersCopy, ",")
			for _, cluster := range clusters {
				select {
				case <-ctx.Done():
					klog.V(6).Infof("定时巡检任务 [%s] 被取消，停止后续集群", item.Name)
					return
				default:
				}
				cluster = strings.TrimSpace(cluster)
				if cluster == "" {
					continue
				}
				_, _ = s.RunByCluster(ctx, &scheduleIDCopy, cluster, TriggerTypeCron)
			}
			klog.V(6).Infof("定时巡检任务 [%s] 执行完成", item.Name)
		})
		if addErr != nil {
			klog.Errorf("添加巡检任务[id=%d]失败  %v", scheduleID, addErr)
			return
		}
	}
	klog.V(6).Infof("添加巡检任务[id=%d]", scheduleID)
}
func (s *ScheduleBackground) Update(scheduleID uint) {
	s.Add(scheduleID)
}
