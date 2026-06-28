// Package adapter 抽象不同脚本类型的启动语义，统一成相同的接口。
//
// 报告建议的统一接口：detect / prepare / launch / discover / stop / serialize。
// 本包实现 detect + prepare（把脚本归一为一条 proc.PreparedCommand）；
// launch/discover/stop 由 launcher（app 层）组合 proc + probe 完成，不在适配器内耦合。
package adapter

import "strings"

// Adapter 是脚本类型适配器的统一接口。
type Adapter interface {
	// Type 返回适配器标识，如 "batch"/"ps1"/"npm"。
	Type() string
	// Detect 根据项目根与入口脚本判断匹配度（0~100）。0 表示不匹配。
	Detect(projectRoot, entryFile string) int
	// Prepare 把脚本配置归一为可直接执行的命令。
	Prepare(cfg *PrepareInput) (*PrepareOutput, error)
}

// PrepareInput prepare 的输入。
type PrepareInput struct {
	EntryScript string            // 入口脚本路径
	Cwd         string            // 工作目录
	Env         map[string]string // 注入环境变量
	PortHints   []int             // 端口提示
}

// PrepareOutput prepare 的输出。
type PrepareOutput struct {
	Cmd  string
	Args []string
	Cwd  string
	Env  map[string]string
}

// Registry 持有所有适配器，按 Type 索引；并提供 Select 选出最佳匹配。
type Registry struct {
	adapters []Adapter
}

func NewRegistry() *Registry {
	return &Registry{}
}

// Register 注册一个适配器。
func (r *Registry) Register(a Adapter) {
	r.adapters = append(r.adapters, a)
}

// Get 按 Type 取适配器，找不到返回 nil。
func (r *Registry) Get(typ string) Adapter {
	for _, a := range r.adapters {
		if strings.EqualFold(a.Type(), typ) {
			return a
		}
	}
	return nil
}

// Select 根据项目根与入口脚本，选出置信度最高的适配器。
// 都不匹配时返回 DefaultAdapter（按文件后缀兜底）。
func (r *Registry) Select(projectRoot, entryFile string) Adapter {
	best := DefaultBatchAdapter{}
	bestScore := best.Detect(projectRoot, entryFile)
	var chosen Adapter = &best
	for _, a := range r.adapters {
		if score := a.Detect(projectRoot, entryFile); score > bestScore {
			bestScore = score
			chosen = a
		}
	}
	return chosen
}

// All 返回所有已注册适配器（用于自检）。
func (r *Registry) All() []Adapter { return r.adapters }
