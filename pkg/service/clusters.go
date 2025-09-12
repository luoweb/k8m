package service

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/duke-git/lancet/v2/slice"
	"github.com/robfig/cron/v3"
	"github.com/weibaohui/k8m/internal/dao"
	"github.com/weibaohui/k8m/pkg/comm/utils"
	"github.com/weibaohui/k8m/pkg/constants"
	"github.com/weibaohui/k8m/pkg/flag"
	"github.com/weibaohui/k8m/pkg/k8sgpt/analysis"
	"github.com/weibaohui/k8m/pkg/models"
	"github.com/weibaohui/kom/kom"
	komaws "github.com/weibaohui/kom/kom/aws"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

type clusterService struct {
	clusterConfigs        []*ClusterConfig                    // 文件名+context名称 -> 集群配置
	AggregateDelaySeconds int                                 // 聚合延迟时间
	callbackRegisterFunc  func(cluster *ClusterConfig) func() // 用来注册回调参数的回调方法

}

func (c *clusterService) SetRegisterCallbackFunc(callback func(cluster *ClusterConfig) func()) {
	c.callbackRegisterFunc = callback
}

type ClusterConfig struct {
	ClusterID               string                         `json:"cluster_id,omitempty"`              // 自动生成，不要赋值
	ClusterIDBase64         string                         `json:"cluster_id_base64,omitempty"`       // 自动生成，不要赋值
	FileName                string                         `json:"fileName,omitempty"`                // kubeconfig 文件名称
	ContextName             string                         `json:"contextName,omitempty"`             // context名称
	ClusterName             string                         `json:"clusterName,omitempty"`             // 集群名称
	Server                  string                         `json:"server,omitempty"`                  // 集群地址
	ServerVersion           string                         `json:"serverVersion,omitempty"`           // 通过这个值来判断集群是否可用
	UserName                string                         `json:"userName,omitempty"`                // 用户名
	Namespace               string                         `json:"namespace,omitempty"`               // kubeconfig 限制Namespace
	Err                     string                         `json:"err,omitempty"`                     // 连接错误信息
	NodeStatusAggregated    bool                           `json:"nodeStatusAggregated,omitempty"`    // 是否已聚合节点状态
	PodStatusAggregated     bool                           `json:"podStatusAggregated,omitempty"`     // 是否已聚合容器组状态
	PVCStatusAggregated     bool                           `json:"pvcStatusAggregated,omitempty"`     // 是否已聚合pcv状态
	PVStatusAggregated      bool                           `json:"pvStatusAggregated,omitempty"`      // 是否已聚合pv状态
	IngressStatusAggregated bool                           `json:"ingressStatusAggregated,omitempty"` // 是否已聚合ingress状态
	ClusterConnectStatus    constants.ClusterConnectStatus `json:"clusterConnectStatus,omitempty"`    // 集群连接状态
	IsInCluster             bool                           `json:"isInCluster,omitempty"`             // 是否为集群内运行获取到的配置
	watchStatus             map[string]*clusterWatchStatus // watch 类型为key，比如pod,deploy,node,pvc,sc
	restConfig              *rest.Config                   // 直连rest.Config
	kubeConfig              []byte                         // 集群配置.kubeconfig原始文件内容
	watchStatusLock         sync.RWMutex                   // watch状态读写锁
	Source                  ClusterConfigSource            `json:"source,omitempty"`                 // 配置文件来源
	K8sGPTProblemsCount     int                            `json:"k8s_gpt_problems_count,omitempty"` // k8sGPT 扫描结果
	K8sGPTProblemsResult    *analysis.ResultWithStatus     `json:"k8s_gpt_problems,omitempty"`       // k8sGPT 扫描结果
	NotAfter                *time.Time                     `json:"not_after,omitempty"`
	AWSConfig               *komaws.EKSAuthConfig          `json:"aws_config,omitempty"` // AWS EKS配置信息
	IsAWSEKS                bool                           `json:"is_aws_eks,omitempty"` // 标识是否为AWS EKS集群
}
type ClusterConfigSource string

var ClusterConfigSourceFile ClusterConfigSource = "File"
var ClusterConfigSourceDB ClusterConfigSource = "DB"
var ClusterConfigSourceInCluster ClusterConfigSource = "InCluster"
var ClusterConfigSourceAWS ClusterConfigSource = "AWS"

// 记录每个集群的watch 启动情况
// watch 有多种类型，需要记录
type clusterWatchStatus struct {
	WatchType   string          `json:"watchType,omitempty"`
	Started     bool            `json:"started,omitempty"`
	StartedTime time.Time       `json:"startedTime,omitempty"`
	Watcher     watch.Interface `json:"-"`
}

// SetClusterWatchStarted 设置集群Watch启动状态
func (c *ClusterConfig) SetClusterWatchStarted(watchType string, watcher watch.Interface) {
	c.watchStatusLock.Lock()
	defer c.watchStatusLock.Unlock()
	c.watchStatus[watchType] = &clusterWatchStatus{
		WatchType:   watchType,
		Started:     true,
		StartedTime: time.Now(),
		Watcher:     watcher,
	}
}

// GetClusterWatchStatus 获取集群Watch状态
func (c *ClusterConfig) GetClusterWatchStatus(watchType string) bool {
	watcher := c.watchStatus[watchType]
	if watcher == nil {
		return false
	}
	return watcher.Started
}
func (c *ClusterConfig) GetKubeconfig() string {
	return string(c.kubeConfig)
}

// GetClusterID 根据ClusterConfig，按照 文件名+context名称 获取clusterID
func (c *ClusterConfig) GetClusterID() string {

	// 原有逻辑
	id := fmt.Sprintf("%s/%s", c.FileName, c.ContextName)
	if c.IsInCluster {
		id = "InCluster"
	}
	if id == "InCluster/InCluster" {
		id = "InCluster"
	}
	c.ClusterID = id
	c.ClusterIDBase64 = base64.StdEncoding.EncodeToString([]byte(id))
	return id
}

func (c *ClusterConfig) GetRestConfig() *rest.Config {
	return c.restConfig
}

// ClusterID 根据ClusterConfig，按照 文件名+context名称 获取clusterID
func (c *clusterService) ClusterID(clusterConfig *ClusterConfig) string {
	return clusterConfig.GetClusterID()
}

// GetClusterByID 获取ClusterConfig
func (c *clusterService) GetClusterByID(id string) *ClusterConfig {
	if id == "" {
		return nil
	}
	if id == "InCluster" {
		// InCluster 并没有使用ClusterConfig
		predicate := func(index int, item *ClusterConfig) bool {
			return item.IsInCluster
		}
		if v, ok := slice.FindBy(c.clusterConfigs, predicate); ok {
			return v
		}
	}
	// 解析selectedCluster
	// 第一个/前面的字符是fileName。其他的都是contextName
	// 有可能会出现多个/，如config/aws-x-x-x/demo
	slashIndex := strings.Index(id, "/")
	if slashIndex == -1 {
		return nil
	}
	fileName := id[:slashIndex]
	contextName := id[slashIndex+1:]
	for _, clusterConfig := range c.clusterConfigs {
		if clusterConfig.FileName == fileName && clusterConfig.ContextName == contextName {
			return clusterConfig
		}
	}
	return nil
}

// GetCertificateExpiry 获取集群证书的过期时间
func (c *ClusterConfig) GetCertificateExpiry() time.Time {
	// 检查 kubeConfig 是否为空
	if len(c.kubeConfig) == 0 {
		klog.V(8).Infof("设置NotAfter, 集群[%s] kubeConfig为空", c.ClusterID)
		return time.Time{}
	}

	config, err := clientcmd.Load(c.kubeConfig)
	if err != nil {
		klog.V(8).Infof("设置NotAfter, 解析文件[%s]失败: %v", c.ClusterID, err)
		return time.Time{}
	}

	// 检查 config 是否为空
	if config == nil {
		klog.V(8).Infof("设置NotAfter, 集群[%s] config为空", c.ClusterID)
		return time.Time{}
	}

	// 检查 CurrentContext 是否为空
	if config.CurrentContext == "" {
		klog.V(8).Infof("设置NotAfter, 集群[%s] CurrentContext为空", c.ClusterID)
		return time.Time{}
	}

	// 检查 Contexts 是否为空
	if config.Contexts == nil {
		klog.V(8).Infof("设置NotAfter, 集群[%s] Contexts为空", c.ClusterID)
		return time.Time{}
	}

	// 检查当前 context 是否存在
	currentContext, contextExists := config.Contexts[config.CurrentContext]
	if !contextExists || currentContext == nil {
		klog.V(8).Infof("设置NotAfter, 集群[%s] 当前context[%s]不存在", c.ClusterID, config.CurrentContext)
		return time.Time{}
	}

	// 检查 AuthInfos 是否为空
	if config.AuthInfos == nil {
		klog.V(8).Infof("设置NotAfter, 集群[%s] AuthInfos为空", c.ClusterID)
		return time.Time{}
	}

	// 检查 AuthInfo 名称是否为空
	if currentContext.AuthInfo == "" {
		klog.V(8).Infof("设置NotAfter, 集群[%s] AuthInfo名称为空", c.ClusterID)
		return time.Time{}
	}

	// 获取 authInfo
	authInfo, exists := config.AuthInfos[currentContext.AuthInfo]
	if !exists || authInfo == nil {
		klog.V(8).Infof("设置NotAfter, 集群[%s] authInfo[%s]不存在", c.ClusterID, currentContext.AuthInfo)
		return time.Time{}
	}

	// 检查证书数据是否为空
	if len(authInfo.ClientCertificateData) == 0 {
		klog.V(8).Infof("设置NotAfter, 集群[%s] ClientCertificateData为空", c.ClusterID)
		return time.Time{}
	}

	// 解析证书
	cert, err := utils.ParseCertificate(authInfo.ClientCertificateData)
	if err != nil {
		klog.V(8).Infof("设置NotAfter, 集群[%s]解析证书失败: %v", c.ClusterID, err)
		return time.Time{}
	}

	// 检查证书是否为空
	if cert == nil {
		klog.V(8).Infof("设置NotAfter, 集群[%s]解析出的证书为空", c.ClusterID)
		return time.Time{}
	}

	return cert.NotAfter.Local()
}

// IsConnected 判断集群是否连接
func (c *clusterService) IsConnected(selectedCluster string) bool {
	cluster := c.GetClusterByID(selectedCluster)
	if cluster == nil {
		return false
	}
	if cluster.ClusterConnectStatus == "" {
		return false
	}
	connected := cluster.ClusterConnectStatus == constants.ClusterConnectStatusConnected
	return connected
}

func (c *clusterService) DelayStartFunc(f func()) {
	// 延迟启动cron
	// 设置一次性任务的执行时间，例如 5 秒后执行
	schedule := utils.DelayStartSchedule(c.AggregateDelaySeconds)
	cronInstance := cron.New()
	_, err := cronInstance.AddFunc(schedule, f)
	if err != nil {
		klog.Errorf("延迟方法注册失败%v", err)
		return
	}
	cronInstance.Start()
	klog.V(6).Infof("延迟启动cron %ds: %s", c.AggregateDelaySeconds, schedule)
}

// Connect 重新连接集群
func (c *clusterService) Connect(clusterID string) {
	klog.V(4).Infof("连接集群 %s 开始", clusterID)
	// 先清除原来的状态
	cc := c.GetClusterByID(clusterID)
	if cc != nil && !(cc.ClusterConnectStatus == constants.ClusterConnectStatusConnected || cc.ClusterConnectStatus == constants.ClusterConnectStatusConnecting) {
		klog.V(4).Infof("Connect 发现原集群,非连接中，非已连接状态，清理集群 %s  原始信息", clusterID)
		cc.ServerVersion = ""
		cc.restConfig = nil
		cc.Err = ""
		cc.ClusterConnectStatus = constants.ClusterConnectStatusDisconnected
		_, _ = c.RegisterCluster(cc)
	}

	klog.V(4).Infof("连接集群 %s 完毕", clusterID)
}

// Disconnect 断开连接
func (c *clusterService) Disconnect(clusterID string) {
	klog.V(4).Infof("Disconnect 清理集群 %s 原始信息", clusterID)

	// 先清除原来的状态
	cc := c.GetClusterByID(clusterID)
	if cc == nil {
		return
	}
	cc.ServerVersion = ""
	cc.restConfig = nil
	cc.Err = ""
	cc.ClusterConnectStatus = constants.ClusterConnectStatusDisconnected
	for _, v := range cc.watchStatus {
		if v.Watcher != nil {
			v.Watcher.Stop()
			klog.V(6).Infof("%s 停止 Watch  %s", cc.ClusterName, v.WatchType)
		}
	}
	// 从kom解除
	kom.Clusters().RemoveClusterById(clusterID)
}

// Scan 扫描集群
func (c *clusterService) Scan() {
	cfg := flag.Init()
	c.ScanClustersInDir(cfg.KubeConfig)

	c.ScanClustersInDB()
}

// AllClusters 获取所有集群
func (c *clusterService) AllClusters() []*ClusterConfig {
	return c.clusterConfigs
}

// ConnectedClusters 获取已连接的集群
func (c *clusterService) ConnectedClusters() []*ClusterConfig {
	connected := slice.Filter(c.AllClusters(), func(index int, item *ClusterConfig) bool {
		return item.ClusterConnectStatus == constants.ClusterConnectStatusConnected
	})
	return connected
}

// FirstClusterID 获取第一个集群ID
func (c *clusterService) FirstClusterID() string {
	clusters := c.ConnectedClusters()
	var selectedCluster string
	if len(clusters) > 0 {
		cluster := clusters[0]
		selectedCluster = c.ClusterID(cluster)
	}
	return selectedCluster
}

// RegisterClustersByPath 根据kubeconfig地址注册集群
func (c *clusterService) RegisterClustersByPath(filePath string) {
	// 如果c.clusterConfigs为空，则返回
	if len(c.clusterConfigs) == 0 {
		klog.V(6).Infof("clusterConfigs为空，不进行注册")
		return
	}
	// 处理路径中的 ~ 符号
	expandedPath, err := utils.ExpandHomePath(filePath)
	if err != nil {
		klog.V(6).Infof("展开路径失败: %v", err)
		return
	}
	filePath = expandedPath
	content, err := os.ReadFile(filePath)
	if err != nil {
		klog.V(6).Infof("读取文件[%s]失败: %v", filePath, err)
		return
	}

	config, err := clientcmd.Load(content)
	if err != nil {
		klog.V(6).Infof("解析文件[%s]失败: %v", filePath, err)
	}
	contextName := config.CurrentContext

	fileName := filepath.Base(filePath)
	c.Connect(fmt.Sprintf("%s/%s", fileName, contextName))
}

// ScanClustersInDir 扫描文件夹下的kubeconfig文件，仅扫描形成列表但是不注册集群
func (c *clusterService) ScanClustersInDir(path string) {
	// 处理路径中的 ~ 符号
	expandedPath, err := utils.ExpandHomePath(path)
	if err != nil {
		klog.V(6).Infof("展开路径失败: %v", err)
		return
	}
	path = expandedPath

	// 1. 通过kubeconfig文件，找到所在目录
	dir := filepath.Dir(path)

	// 2. 通过所在目录，找到同目录下的所有文件
	files, err := os.ReadDir(dir)
	if err != nil {
		klog.V(6).Infof("读取文件夹[%s]失败: %v", dir, err)
		return
	}

	// 3. 检查每个文件是否为有效的kubeconfig文件

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		filePath := filepath.Join(dir, file.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			klog.V(6).Infof("读取文件[%s]失败: %v", filePath, err)
			continue
		}

		config, err := clientcmd.Load(content)
		if err != nil {
			klog.V(6).Infof("解析文件[%s]失败: %v", filePath, err)
			continue // 解析失败，跳过该文件
		}
		for contextName := range config.Contexts {
			context := config.Contexts[contextName]
			cluster := config.Clusters[context.Cluster]

			clusterConfig := &ClusterConfig{
				FileName:             file.Name(),
				ContextName:          contextName,
				ClusterID:            fmt.Sprintf("%s/%s", file.Name(), contextName),
				UserName:             context.AuthInfo,
				ClusterName:          context.Cluster,
				Namespace:            context.Namespace,
				kubeConfig:           content,
				watchStatus:          make(map[string]*clusterWatchStatus),
				ClusterConnectStatus: constants.ClusterConnectStatusDisconnected,
				Source:               ClusterConfigSourceFile,
			}
			clusterConfig.Server = cluster.Server
			c.AddToClusterList(clusterConfig)
		}
	}

}
func (c *clusterService) ScanClustersInDB() {

	var list []*models.KubeConfig
	err := dao.DB().Model(&models.KubeConfig{}).Find(&list).Error
	if err != nil {
		klog.Errorf("查询集群失败: %v", err)
		return
	}

	for i, cc := range c.clusterConfigs {
		if cc.Source == ClusterConfigSourceDB || cc.Source == ClusterConfigSourceAWS {
			// 查一下list中是否存在
			filter := slice.Filter(list, func(index int, item *models.KubeConfig) bool {
				if item.Server == cc.Server && item.User == cc.UserName && item.Cluster == cc.ClusterName {
					return true
				}
				return false
			})
			if len(filter) == 0 {
				// 在数据库中也不存在
				// 从list中删除
				// 删除前先断开连接，避免watcher泄露
				c.Disconnect(cc.ClusterID)
				c.clusterConfigs = slice.DeleteAt(c.clusterConfigs, i)
			}
		}
	}

	// 2. 处理数据库中的配置
	for _, item := range list {
		config, err := clientcmd.Load([]byte(item.Content))
		if err != nil {
			klog.V(6).Infof("解析集群 [%s]失败: %v", item.Server, err)
			continue
		}

		// 检查每个context
		for contextName := range config.Contexts {
			context := config.Contexts[contextName]
			cluster := config.Clusters[context.Cluster]

			if context.AuthInfo == item.User {
				// 检查是否已存在该配置
				exists := false
				for _, cc := range c.clusterConfigs {
					if (cc.FileName == string(ClusterConfigSourceAWS)) && cc.Server == cluster.Server && cc.ContextName == contextName {
						exists = true
						break
					}
				}

				// 如果不存在，添加新配置
				if !exists {
					clusterConfig := &ClusterConfig{
						ContextName:          contextName,
						UserName:             context.AuthInfo,
						ClusterName:          context.Cluster,
						Namespace:            context.Namespace,
						kubeConfig:           []byte(item.Content),
						watchStatus:          make(map[string]*clusterWatchStatus),
						ClusterConnectStatus: constants.ClusterConnectStatusDisconnected,
						Server:               cluster.Server,
						Source:               ClusterConfigSourceDB,
					}
					if item.DisplayName != "" {
						clusterConfig.FileName = item.DisplayName
					} else {
						clusterConfig.FileName = fmt.Sprintf("%d-%s", item.ID, contextName)
					}

					// aws 单独处理
					if item.IsAWSEKS {
						clusterConfig.Source = ClusterConfigSourceAWS
						eksConfig := &komaws.EKSAuthConfig{
							AccessKey:       item.AccessKey,
							SecretAccessKey: item.SecretAccessKey,
							Region:          item.Region,
							ClusterName:     item.ClusterName,
						}
						clusterConfig.AWSConfig = eksConfig
						clusterConfig.IsAWSEKS = true
						clusterConfig.FileName = string(ClusterConfigSourceAWS)
					}
					clusterConfig.Server = cluster.Server
					c.AddToClusterList(clusterConfig)
				}

			}

		}
	}
}

func (c *clusterService) AddToClusterList(clusterConfig *ClusterConfig) {
	// 判断是否已经存在
	if c.GetClusterByID(clusterConfig.GetClusterID()) != nil {
		return
	}
	c.clusterConfigs = append(c.clusterConfigs, clusterConfig)
}

// Deprecated
// RegisterClustersInDir 注册集群,扫描文件夹下的kubeconfig文件，注册集群
func (c *clusterService) RegisterClustersInDir(path string) {
	// 1. 通过kubeconfig文件，找到所在目录
	dir := filepath.Dir(path)

	// 2. 通过所在目录，找到同目录下的所有文件
	files, err := os.ReadDir(dir)
	if err != nil {
		klog.V(6).Infof("读取文件夹[%s]失败: %v", dir, err)
		return
	}

	// 3. 检查每个文件是否为有效的kubeconfig文件

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		filePath := filepath.Join(dir, file.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			klog.V(6).Infof("读取文件[%s]失败: %v", filePath, err)
			continue
		}

		config, err := clientcmd.Load(content)
		if err != nil {
			klog.V(6).Infof("解析文件[%s]失败: %v", filePath, err)
			continue // 解析失败，跳过该文件
		}
		for contextName := range config.Contexts {
			context := config.Contexts[contextName]
			cluster := config.Clusters[context.Cluster]

			clusterConfig := &ClusterConfig{
				FileName:             file.Name(),
				ContextName:          contextName,
				UserName:             context.AuthInfo,
				ClusterName:          context.Cluster,
				Namespace:            context.Namespace,
				kubeConfig:           content,
				watchStatus:          make(map[string]*clusterWatchStatus),
				ClusterConnectStatus: constants.ClusterConnectStatusDisconnected,
				Source:               ClusterConfigSourceFile,
			}
			clusterConfig.Server = cluster.Server
			c.AddToClusterList(clusterConfig)
		}
	}

	// 注册
	for _, clusterConfig := range c.clusterConfigs {
		// 改为只注册CurrentContext的这个
		_, _ = c.RegisterCluster(clusterConfig)
	}
	// 打印serverVersion
	for _, clusterConfig := range c.clusterConfigs {
		klog.V(6).Infof("ServerVersion: %s/%s: %s[%s] using user: %s", clusterConfig.FileName, clusterConfig.ContextName, clusterConfig.ServerVersion, clusterConfig.Server, clusterConfig.UserName)
	}
}

// RegisterCluster 从已扫描的集群列表中注册指定的某个集群
func (c *clusterService) RegisterCluster(clusterConfig *ClusterConfig) (bool, error) {

	clusterConfig.ClusterConnectStatus = constants.ClusterConnectStatusConnecting
	clusterID := clusterConfig.GetClusterID()

	if clusterConfig.IsAWSEKS {
		if clusterConfig.AWSConfig == nil {
			err := fmt.Errorf("AWS EKS 集群[%s]缺少 AWSConfig", clusterID)
			clusterConfig.ClusterConnectStatus = constants.ClusterConnectStatusFailed
			clusterConfig.Err = err.Error()
			return false, err
		}
		// 使用带 ID 的注册确保幂等
		if _, err := kom.Clusters().RegisterAWSClusterWithID(clusterConfig.AWSConfig, clusterID); err != nil {
			klog.V(4).Infof("注册 AWS 集群[%s]失败: %v", clusterID, err)
			clusterConfig.ClusterConnectStatus = constants.ClusterConnectStatusFailed
			clusterConfig.Err = err.Error()
			return false, err
		}
		clusterConfig.ClusterConnectStatus = constants.ClusterConnectStatusConnected
		if c.callbackRegisterFunc != nil {
			c.callbackRegisterFunc(clusterConfig)
		}

		return true, nil
	}

	err := c.LoadRestConfig(clusterConfig)
	if err != nil {
		clusterConfig.ClusterConnectStatus = constants.ClusterConnectStatusFailed
		clusterConfig.Err = err.Error()
		return false, err
	}
	if clusterConfig.IsInCluster {
		// InCluster模式
		_, err := kom.Clusters().RegisterInCluster()
		if err != nil {
			klog.V(4).Infof("注册集群[%s]失败: %v", clusterID, err)
			clusterConfig.ClusterConnectStatus = constants.ClusterConnectStatusFailed
			clusterConfig.Err = err.Error()
			return false, err
		}
	} else {
		// 集群外模式
		_, err := kom.Clusters().RegisterByConfigWithID(clusterConfig.restConfig, clusterID)

		if err != nil {
			klog.V(4).Infof("注册集群[%s]失败: %v", clusterID, err)
			clusterConfig.ClusterConnectStatus = constants.ClusterConnectStatusFailed
			clusterConfig.Err = err.Error()
			return false, err
		}
	}
	klog.V(4).Infof("成功注册集群: %s [%s]", clusterID, clusterConfig.Server)
	clusterConfig.ClusterConnectStatus = constants.ClusterConnectStatusConnected

	// 先连接了，再执行回调注册，因为是绑定在kom上的， 只有上面的代码执行后，才会连接，才会有kom.Clusters()
	c.callbackRegisterFunc(clusterConfig)

	return true, nil
}

// LoadRestConfig 校验集群是否可连接，并更新状态
func (c *clusterService) LoadRestConfig(config *ClusterConfig) error {
	var restConfig *rest.Config
	var err error
	if config.IsInCluster {
		// 集群内模式
		restConfig, _ = rest.InClusterConfig()
	} else {
		// 集群外模式
		lines := strings.Split(string(config.kubeConfig), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "current-context:") {
				lines[i] = "current-context: " + config.ContextName
			}
		}
		bytes := []byte(strings.Join(lines, "\n"))
		restConfig, err = clientcmd.RESTConfigFromKubeConfig(bytes)

	}
	config.restConfig = restConfig

	// 校验集群是否可连接
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		klog.V(6).Infof("创建clientset失败 %s: %v", config.GetClusterID(), err)
		config.Err = err.Error()
		config.ClusterConnectStatus = constants.ClusterConnectStatusFailed
		return err
	}

	// 尝试获取集群版本以验证连接
	info, err := clientset.ServerVersion()
	if err != nil {
		klog.V(6).Infof("连接集群失败 %s: %v", config.GetClusterID(), err)
		config.Err = err.Error()
		config.ClusterConnectStatus = constants.ClusterConnectStatusFailed
		return err
	}
	klog.V(6).Infof("LoadRestConfig 获取集群 版本成功 %s", config.GetClusterID())
	config.ServerVersion = info.GitVersion
	return err
}

// RegisterInCluster 将InCluster的配置注册到集群列表中
func (c *clusterService) RegisterInCluster() {

	// 获取InCluster的配置
	config, err := rest.InClusterConfig()
	if err != nil {
		cfg := flag.Init()
		cfg.InCluster = false
		klog.Errorf("获取InCluster的配置失败,InCluster模式关闭.错误：%v", err)
		return
	}

	// 3. 生成 ClusterConfig
	clusterConfig := &ClusterConfig{
		ClusterName:          "kubernetes", // InCluster 模式没有 context, 设定默认名称
		FileName:             "InCluster",
		ContextName:          "InCluster",
		ClusterID:            "InCluster",
		Server:               config.Host,
		IsInCluster:          true,
		restConfig:           config,
		watchStatus:          make(map[string]*clusterWatchStatus),
		ClusterConnectStatus: constants.ClusterConnectStatusDisconnected,
		Source:               ClusterConfigSourceInCluster,
	}

	c.AddToClusterList(clusterConfig)
	_, _ = c.RegisterCluster(clusterConfig)
}

func (c *ClusterConfig) SetClusterScanStatus(result *analysis.ResultWithStatus) {
	c.K8sGPTProblemsCount = result.Problems
	c.K8sGPTProblemsResult = result

}
func (c *ClusterConfig) GetClusterScanResult() *analysis.ResultWithStatus {

	return c.K8sGPTProblemsResult

}

// =========================== 集群服务增强方法 ===========================

// validateAWSConfig 验证AWS配置
func (c *clusterService) validateAWSConfig(config *komaws.EKSAuthConfig) error {
	if config == nil {
		return fmt.Errorf("AWS配置不能为空")
	}

	if config.AccessKey == "" {
		return fmt.Errorf("AWS Access Key不能为空")
	}

	if config.SecretAccessKey == "" {
		return fmt.Errorf("AWS Secret Access Key不能为空")
	}

	if config.Region == "" {
		return fmt.Errorf("AWS区域不能为空")
	}

	if config.ClusterName == "" {
		return fmt.Errorf("EKS集群名称不能为空")
	}

	return nil
}

// RegisterAWSEKSCluster 注册AWS EKS集群
func (c *clusterService) RegisterAWSEKSCluster(config *komaws.EKSAuthConfig) (*ClusterConfig, error) {
	// 参数验证
	if err := c.validateAWSConfig(config); err != nil {
		return nil, fmt.Errorf("AWS配置验证失败: %w", err)
	}

	// 将项目内部的AWSEKSConfig转换为kom库要求的kom.aws.EKSAuthConfig
	eksAuthConfig := &komaws.EKSAuthConfig{
		AccessKey:       config.AccessKey,
		SecretAccessKey: config.SecretAccessKey,
		Region:          config.Region,
		ClusterName:     config.ClusterName,
	}

	kg := komaws.NewKubeconfigGenerator()
	content, err := kg.GenerateFromAWS(eksAuthConfig)
	if err != nil {
		return nil, fmt.Errorf("生成AWS EKS集群配置文件失败: %w", err)
	}
	kubeconfig, err := clientcmd.Load([]byte(content))
	if err != nil {
		klog.V(6).Infof("解析 AWS EKS集群kubeconfig配置失败: %v", err)
		return nil, fmt.Errorf("生成AWS EKS集群配置文件失败: %w", err)
	}

	// 只取第一个context
	var contextName string
	var clusterConfig *ClusterConfig
	for name := range kubeconfig.Contexts {
		contextName = name
		break
	}
	if contextName != "" {
		context := kubeconfig.Contexts[contextName]
		cluster := kubeconfig.Clusters[context.Cluster]

		clusterConfig = &ClusterConfig{
			FileName:             string(ClusterConfigSourceAWS),
			ContextName:          contextName,
			ClusterName:          context.Cluster,
			Namespace:            context.Namespace,
			Server:               cluster.Server,
			kubeConfig:           []byte(content),
			watchStatus:          make(map[string]*clusterWatchStatus),
			ClusterConnectStatus: constants.ClusterConnectStatusConnected,
			Source:               ClusterConfigSourceAWS,
			IsAWSEKS:             true,
			AWSConfig:            config,
		}
		clusterID := clusterConfig.GetClusterID()

		// 使用kom统一的AWS EKS集群注册方法
		_, err = kom.Clusters().RegisterAWSClusterWithID(eksAuthConfig, clusterID)
		if err != nil {
			return nil, fmt.Errorf("注册AWS EKS集群失败: %w", err)
		}

		// 添加到集群列表
		c.AddToClusterList(clusterConfig)

		klog.V(4).Infof("成功注册AWS EKS集群: %s [%s]", config.ClusterName, clusterID)
	}

	return clusterConfig, nil
}
