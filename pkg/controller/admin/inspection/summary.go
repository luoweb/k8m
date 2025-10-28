package inspection

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/duke-git/lancet/v2/slice"
	"github.com/gin-gonic/gin"
	"github.com/weibaohui/k8m/internal/dao"
	"github.com/weibaohui/k8m/pkg/comm/utils"
	"github.com/weibaohui/k8m/pkg/comm/utils/amis"
	"github.com/weibaohui/k8m/pkg/constants"
	"github.com/weibaohui/k8m/pkg/lua"
	"github.com/weibaohui/k8m/pkg/models"
	"gorm.io/gorm"
	"k8s.io/klog/v2"
)

// @Summary 统计巡检计划执行情况
// @Description 统计指定巡检计划的执行情况，支持按时间范围和集群过滤
// @Security BearerAuth
// @Param id path string false "巡检计划ID"
// @Param cluster path string false "集群名称"
// @Param start_time path string false "开始时间(RFC3339格式)"
// @Param end_time path string false "结束时间(RFC3339格式)"
// @Success 200 {object} string
// @Router /admin/inspection/schedule/id/{id}/summary [post]
// @Router /admin/inspection/schedule/id/{id}/summary/cluster/{cluster}/start_time/{start_time}/end_time/{end_time} [post]
func (s *AdminScheduleController) SummaryBySchedule(c *gin.Context) {
	params := dao.BuildParams(c)
	params.PerPage = 100000000
	// 1. 获取scheduleID参数
	scheduleID := c.Param("id")

	// 新增：解析时间范围参数
	var startTime, endTime time.Time
	var err error
	var cluster string
	startTimeStr := c.Param("start_time")
	endTimeStr := c.Param("end_time")
	if startTimeStr != "" {
		startTime, err = time.Parse(time.RFC3339, startTimeStr)
		if err != nil {
			amis.WriteJsonError(c, fmt.Errorf("start_time 格式错误，应为 RFC3339"))
			return
		}
	}
	if endTimeStr != "" {
		endTime, err = time.Parse(time.RFC3339, endTimeStr)
		if err != nil {
			amis.WriteJsonError(c, fmt.Errorf("end_time 格式错误，应为 RFC3339"))
			return
		}
	}

	// 获取cluster参数
	clusterBase64 := c.Param("cluster")

	if clusterDecode, err := utils.UrlSafeBase64Decode(clusterBase64); err == nil {
		cluster = string(clusterDecode)
	} else {
		klog.V(6).Infof("cluster=%s,%v ", cluster, err)
	}

	// 2. 查询所有该scheduleID下的InspectionRecord
	recordModel := &models.InspectionRecord{}
	records, _, err := recordModel.List(params, func(db *gorm.DB) *gorm.DB {
		query := db
		if cluster != "" {
			query = query.Where("cluster = ?", cluster)
		}
		if scheduleID != "" {
			query = query.Where("schedule_id = ?", scheduleID)
		}
		if !startTime.IsZero() {
			query = query.Where("created_at >= ?", startTime)
		}
		if !endTime.IsZero() {
			query = query.Where("created_at <= ?", endTime)
		}
		return query.Order("id desc")
	})
	if err != nil {
		amis.WriteJsonError(c, err)
		return
	}
	if len(records) == 0 {
		amis.WriteJsonData(c, gin.H{"summary": "无执行记录"})
		return
	}
	tempScheduleIDs := make([]*uint, 0, 20)
	clusterSet := map[string]struct{}{}
	for _, r := range records {
		tempScheduleIDs = append(tempScheduleIDs, r.ScheduleID)
		clusterSet[r.Cluster] = struct{}{}
	}

	// 对ScheduleID进行去重
	tempScheduleIDs = slice.UniqueBy(tempScheduleIDs, func(item *uint) uint {
		return *item
	})
	// 3. 查询所有相关InspectionCheckEvent
	eventModel := &models.InspectionCheckEvent{}
	events, _, err := eventModel.List(params, func(db *gorm.DB) *gorm.DB {
		query := db
		if cluster != "" {
			query = query.Where("cluster = ?", cluster)
		}
		if scheduleID != "" {
			query = query.Where("schedule_id in ?", tempScheduleIDs)
		}
		if !startTime.IsZero() {
			query = query.Where("created_at >= ?", startTime)
		}
		if !endTime.IsZero() {
			query = query.Where("created_at <= ?", endTime)
		}
		return query.Order("id desc")
	})
	if err != nil {
		amis.WriteJsonError(c, err)
		return
	}

	// 4. 聚合统计

	totalClusters := len(clusterSet)
	totalRuns := len(records) // 巡检计划执行次数

	clusterKindMap := map[string]map[string]int{}    // cluster -> kind -> count
	clusterKindErrMap := map[string]map[string]int{} // cluster -> kind -> error count
	for _, e := range events {
		if _, ok := clusterKindMap[e.Cluster]; !ok {
			clusterKindMap[e.Cluster] = map[string]int{}
			clusterKindErrMap[e.Cluster] = map[string]int{}
		}
		clusterKindMap[e.Cluster][e.Kind]++
		if e.EventStatus != string(constants.LuaEventStatusNormal) {
			clusterKindErrMap[e.Cluster][e.Kind]++

		}

	}

	// 5. 构建返回结构
	result := gin.H{
		"total_clusters": totalClusters,
		"total_runs":     totalRuns, // 新增字段：执行次数
		"clusters":       []gin.H{},
	}
	// 新增：如果 scheduleID 为空，增加运行巡检计划数
	if scheduleID == "" {
		var count int64
		dao.DB().Model(&models.InspectionRecord{}).Distinct("schedule_id").Count(&count)
		result["total_schedules"] = count
	}
	// 统计每个集群的执行次数
	clusterRunCount := map[string]int{}
	for _, r := range records {
		clusterRunCount[r.Cluster]++
	}
	for cluster, kindMap := range clusterKindMap {
		var kindArr []gin.H
		for kind, count := range kindMap {
			errCount := clusterKindErrMap[cluster][kind]
			kindArr = append(kindArr, gin.H{
				"kind":        kind,
				"count":       count,
				"error_count": errCount,
			})
		}
		result["clusters"] = append(result["clusters"].([]gin.H), gin.H{
			"cluster":   cluster,
			"run_count": clusterRunCount[cluster], // 新增字段：该集群执行次数
			"kinds":     kindArr,
		})
	}
	// 新增：统计最新一次执行情况
	var latestRun gin.H
	if len(records) > 0 {
		latestRecord := records[0]
		kindStatus := map[string]map[string]int{} // kind -> status -> count
		for _, e := range events {
			if e.RecordID == latestRecord.ID {
				if _, ok := kindStatus[e.Kind]; !ok {
					kindStatus[e.Kind] = map[string]int{"pass": 0, "fail": 0}
				}
				if e.EventStatus == string(constants.LuaEventStatusNormal) {
					kindStatus[e.Kind]["pass"]++
				} else {
					kindStatus[e.Kind]["fail"]++
				}
			}
		}
		var kindArr []gin.H
		for kind, statusMap := range kindStatus {
			kindArr = append(kindArr, gin.H{
				"kind":         kind,
				"normal_count": statusMap["pass"],
				"error_count":  statusMap["fail"],
			})
		}
		latestRun = gin.H{
			"record_id":   latestRecord.ID,
			"schedule_id": latestRecord.ScheduleID,
			"run_time":    latestRecord.CreatedAt,
			"kinds":       kindArr,
		}
		result["latest_run"] = latestRun
	}
	amis.WriteJsonData(c, result)
}

// @Summary 生成巡检记录AI总结
// @Description 为指定巡检记录生成AI总结
// @Security BearerAuth
// @Param id path string true "巡检记录ID"
// @Success 200 {object} string
// @Router /admin/inspection/schedule/record/id/{id}/summary [post]
func (s *AdminScheduleController) SummaryByRecordID(c *gin.Context) {
	recordIDStr := c.Param("id")
	if recordIDStr == "" {
		amis.WriteJsonError(c, fmt.Errorf("缺少 record_id 参数"))
		return
	}
	recordID := utils.ToUInt(recordIDStr)
	if recordID == 0 {
		amis.WriteJsonError(c, fmt.Errorf("record_id 参数无效"))
		return
	}

	sb := lua.ScheduleBackground{}
	msg, err := sb.GetSummaryMsg(recordID)
	if err != nil {
		amis.WriteJsonError(c, err)
		return
	}

	// 将原始巡检结果转换为JSON字符串
	resultRawBytes, err := json.Marshal(msg)
	if err != nil {
		klog.Errorf("序列化原始巡检结果失败: %v", err)
		resultRawBytes = []byte("{}")
	}
	resultRaw := string(resultRawBytes)

	summary, summaryErr := sb.SummaryByAI(context.Background(), msg)

	err = sb.SaveSummaryBack(recordID, summary, summaryErr, resultRaw)
	if err != nil {
		amis.WriteJsonError(c, err)
		return
	}
	amis.WriteJsonData(c, summary)
}
