package lua

import (
	"io"
	"time"

	"github.com/weibaohui/k8m/pkg/comm/utils"
	"github.com/weibaohui/kom/kom"
	lua "github.com/yuin/gopher-lua"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

type Kubectl struct {
	k *kom.Kubectl
}

// 实现 kubectl:GVK(group, version, kind) 方法
func gvkFunc(L *lua.LState) int {
	ud := L.CheckUserData(1)
	obj, ok := ud.Value.(*Kubectl)
	if !ok {
		L.ArgError(1, "expected kubectl")
		return 0
	}

	// 获取 GVK 相关参数
	group := L.CheckString(2)
	version := L.CheckString(3)
	kind := L.CheckString(4)
	klog.V(6).Infof("执行GVK查询: %s/%s/%s", group, version, kind)
	// 确保每次 GVK 查询，返回新的 LuaKubectl 实例链，避免嵌套调用时混乱

	ctx := utils.GetContextWithAdmin()
	newObj := &Kubectl{obj.k.GVK(group, version, kind).WithContext(ctx).RemoveManagedFields()}
	newUd := L.NewUserData()
	newUd.Value = newObj
	L.SetMetatable(newUd, L.GetTypeMetatable("kubectl"))
	L.Push(newUd)
	L.Push(lua.LNil)

	return 2
}

// 实现 kubectl:WithLabelSelector(selector) 方法
func withLabelSelectorFunc(L *lua.LState) int {
	ud := L.CheckUserData(1)
	obj, ok := ud.Value.(*Kubectl)
	if !ok {
		L.ArgError(1, "expected kubectl")
		return 0
	}

	// 获取 labelSelector 参数
	selector := L.CheckString(2)
	if selector != "" {
		obj.k = obj.k.WithLabelSelector(selector)
	}
	L.Push(ud)
	L.Push(lua.LNil)
	return 2
}

// 实现 kubectl:WithLabelSelector(selector) 方法
func withNameFunc(L *lua.LState) int {
	ud := L.CheckUserData(1)
	obj, ok := ud.Value.(*Kubectl)
	if !ok {
		L.ArgError(1, "expected kubectl")
		return 0
	}

	name := L.CheckString(2)
	if name != "" {
		obj.k = obj.k.Name(name)
	}
	L.Push(ud)
	L.Push(lua.LNil)
	return 2
}

// 实现 kubectl:Namespace(ns) 方法
func withNamespaceFunc(L *lua.LState) int {
	ud := L.CheckUserData(1)
	obj, ok := ud.Value.(*Kubectl)
	if !ok {
		L.ArgError(1, "expected kubectl")
		return 0
	}

	name := L.CheckString(2)
	if name != "" {
		obj.k = obj.k.Namespace(name)
	}
	L.Push(ud)
	L.Push(lua.LNil)
	return 2
}

// 实现 kubectl:Cache(t) 方法
// 该方法用于设置缓存时间，参数t为缓存时长（单位：秒）
func withCacheFunc(L *lua.LState) int {
	ud := L.CheckUserData(1)
	obj, ok := ud.Value.(*Kubectl)
	if !ok {
		L.ArgError(1, "expected kubectl")
		return 0
	}

	timeSeconds := L.CheckNumber(2)
	if timeSeconds > 0 {
		dur := time.Duration(int64(timeSeconds)) * time.Second
		obj.k = obj.k.WithCache(dur)
	}
	L.Push(ud)
	L.Push(lua.LNil)
	return 2
}

// 实现 kubectl:AllNamespace() 方法
func withAllNamespaceFunc(L *lua.LState) int {
	ud := L.CheckUserData(1)
	obj, ok := ud.Value.(*Kubectl)
	if !ok {
		L.ArgError(1, "expected kubectl")
		return 0
	}

	obj.k = obj.k.AllNamespace()
	L.Push(ud)
	L.Push(lua.LNil)
	return 2
}

// 实现 kubectl:List() 方法
func listResource(L *lua.LState) int {
	klog.V(6).Infof("执行List查询")
	ud := L.CheckUserData(1)
	obj, ok := ud.Value.(*Kubectl)
	if !ok {
		L.ArgError(1, "expected kubectl")
		return 0
	}

	// 查询资源
	var result []*unstructured.Unstructured
	err := obj.k.List(&result).Error
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	// 转换为 Lua 表
	table := toLValue(L, result)
	// 返回查询结果
	L.Push(table)
	L.Push(lua.LNil)
	return 2
}

// 实现 kubectl:Get() 方法
// 用于获取单个资源，返回 Lua 表和错误信息
func getResource(L *lua.LState) int {
	ud := L.CheckUserData(1)
	obj, ok := ud.Value.(*Kubectl)
	if !ok {
		L.ArgError(1, "expected kubectl")
		return 0
	}

	// 查询单个资源
	var result *unstructured.Unstructured
	err := obj.k.Get(&result).Error
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	// 转换为 Lua 表
	table := toLValue(L, result.Object)

	// 返回查询结果
	L.Push(table)
	L.Push(lua.LNil)
	return 2
}

// 实现 kubectl:Doc('spec.replicas') 方法
// 用于获取某个字段的解释，返回 Lua 表和错误信息
func getDoc(L *lua.LState) int {
	ud := L.CheckUserData(1)
	obj, ok := ud.Value.(*Kubectl)
	if !ok {
		L.ArgError(1, "expected kubectl")
		return 0
	}

	field := L.CheckString(2)
	if field != "" {
		obj.k = obj.k.DocField(field)
	}
	// 查询单个资源
	var result []byte
	err := obj.k.Doc(&result).Error
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	// 转换为 Lua 表
	table := toLValue(L, string(result))

	// 返回查询结果
	L.Push(table)
	L.Push(lua.LNil)
	return 2
}

// 实现 kubectl:GetLogs({tailLines=100, container="xxx"}) 方法
// 返回日志文本和错误信息（若有）
// - local logs, err = kubectl.GVK("", "v1", "Pod").Namespace("default").Name("mypod").GetLogs({tailLines=200, container="app"})
// - if err ~= nil then print("error:", err) else print(logs) end
func getLogs(L *lua.LState) int {
	ud := L.CheckUserData(1)
	obj, ok := ud.Value.(*Kubectl)
	if !ok {
		L.ArgError(1, "expected kubectl")
		return 0
	}

	// 解析可选参数表
	var opt v1.PodLogOptions
	if L.GetTop() >= 2 {
		if tbl, ok := L.Get(2).(*lua.LTable); ok {
			// container 字段
			if v := tbl.RawGetString("container"); v.Type() == lua.LTString {
				opt.Container = v.String()
			} else if v := tbl.RawGetString("Container"); v.Type() == lua.LTString {
				opt.Container = v.String()
			}
			// tailLines 字段
			if v := tbl.RawGetString("tailLines"); v.Type() == lua.LTNumber {
				t := int64(lua.LVAsNumber(v))
				opt.TailLines = &t
			} else if v := tbl.RawGetString("TailLines"); v.Type() == lua.LTNumber {
				t := int64(lua.LVAsNumber(v))
				opt.TailLines = &t
			}
		}
	}

	var stream io.ReadCloser
	err := obj.k.Ctl().Pod().GetLogs(&stream, &opt).Error
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	if stream == nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("empty log stream"))
		return 2
	}
	defer stream.Close()

	// 读取全部日志内容
	data, rerr := io.ReadAll(stream)
	if rerr != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(rerr.Error()))
		return 2
	}

	// 返回字符串
	L.Push(toLValue(L, string(data)))
	L.Push(lua.LNil)
	return 2
}

// 实现 kubectl:GetPodResourceUsage() 方法
// 用于获取Pod的资源使用情况，返回 Lua 表和错误信息
// 使用方式：local usage, err = kubectl:GVK("", "v1", "Pod"):Namespace("kube-system"):Name("coredns-ccb96694c-jprpf"):GetPodResourceUsage()
func getPodResourceUsage(L *lua.LState) int {
	ud := L.CheckUserData(1)
	obj, ok := ud.Value.(*Kubectl)
	if !ok {
		L.ArgError(1, "expected kubectl")
		return 0
	}

	// 调用kom库的ResourceUsage方法
	result, err := obj.k.Ctl().Pod().ResourceUsage()
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	// 转换为 Lua 表
	table := toLValue(L, result)

	// 返回查询结果
	L.Push(table)
	L.Push(lua.LNil)
	return 2
}
