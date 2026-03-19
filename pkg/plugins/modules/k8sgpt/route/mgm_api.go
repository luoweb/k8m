package route

import (
	"github.com/go-chi/chi/v5"
	"k8s.io/klog/v2"
)

func RegisterMgmRoutes(r chi.Router) {
	// 集群级别的路由已移至 ClusterRouter (cluster_api.go)
	// 集群扫描: POST /cluster/{cluster}/run
	// 扫描结果: GET /cluster/{cluster}/result

	klog.V(6).Infof("注册k8sgpt插件管理路由(mgm)")
}
