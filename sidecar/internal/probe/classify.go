package probe

import "strings"

// Role 服务角色。
type Role string

const (
	RoleUnknown  Role = "unknown"
	RoleFrontend Role = "frontend"
	RoleBackend  Role = "backend"
	RoleDatabase Role = "database"
)

// Confidence 识别置信度。高的覆盖低的。
type Confidence int

const (
	ConfNone   Confidence = iota // 无信号
	ConfLow                      // 日志特征
	ConfMedium                   // title / content-type
	ConfHigh                     // 响应头 / DB 端口
)

// ClassifyInput 识别输入。所有字段可选(零值即无该信号)。
type ClassifyInput struct {
	Port     int               // 监听端口
	Headers  map[string]string // HTTP 响应头(键大小写不敏感,值做小写匹配),可空
	Title    string            // HTML <title>,可空
	BodyCT   string            // Content-Type,可空
	LogHints []string          // 命中框架特征的日志片段,可空
}

// 标准 DB 端口(高置信,直接判 database)。
var dbPorts = map[int]bool{
	5432: true,  // PostgreSQL
	3306: true,  // MySQL
	6379: true,  // Redis
	27017: true, // MongoDB
	1433: true,  // SQL Server
	8529: true,  // Dgraph
	9092: true,  // Kafka
	9200: true,  // Elasticsearch
	11211: true, // Memcached
}

// 响应头/Title 关键词表。小写匹配。
var frontendHeaderKW = []string{"vite", "webpack", "next", "nuxt"}
var backendHeaderKW = []string{"express", "fastapi", "uvicorn", "gunicorn", "php", "kestrel", "django", "flask", "gin", "koa"}
var frontendTitleKW = []string{"vite", "react app", "vue", "angular", "nuxt"}

// 日志特征正则片段(用 Contains 子串,大小写不敏感)。
var frontendLogKW = []string{"vite v", "webpack compiled", "ready in", "local: http"}
var backendLogKW = []string{"uvicorn running", "started server on", "listening on", "django version", "flask", "gin-debug"}

// Classify 纯函数:按置信度从高到低短路返回。
func Classify(in ClassifyInput) (Role, Confidence) {
	// 1. DB 端口(高)
	if in.Port > 0 && dbPorts[in.Port] {
		return RoleDatabase, ConfHigh
	}
	// 2. 响应头(高)
	if role, ok := matchHeaders(in.Headers); ok {
		return role, ConfHigh
	}
	// 3. Title / Content-Type(中)
	if role, ok := matchTitleOrCT(in.Title, in.BodyCT); ok {
		return role, ConfMedium
	}
	// 4. 日志(低)
	if role, ok := matchLogs(in.LogHints); ok {
		return role, ConfLow
	}
	return RoleUnknown, ConfNone
}

func matchHeaders(h map[string]string) (Role, bool) {
	if h == nil {
		return RoleUnknown, false
	}
	// 合并所有 header 值(键大小写不敏感:调用方可能传 Go canonical 大写键
	// 如 "Server"/"X-Powered-By",也可能传小写键)。统一收集后小写匹配。
	combined := make([]string, 0, len(h))
	for _, v := range h {
		combined = append(combined, v)
	}
	lc := strings.ToLower(strings.Join(combined, " "))
	if containsAny(lc, frontendHeaderKW) {
		return RoleFrontend, true
	}
	if containsAny(lc, backendHeaderKW) {
		return RoleBackend, true
	}
	return RoleUnknown, false
}

func matchTitleOrCT(title, ct string) (Role, bool) {
	lt := strings.ToLower(title)
	if containsAny(lt, frontendTitleKW) {
		return RoleFrontend, true
	}
	// 根路径返回 application/json 强烈提示 API
	if strings.Contains(strings.ToLower(ct), "application/json") {
		return RoleBackend, true
	}
	return RoleUnknown, false
}

func matchLogs(logs []string) (Role, bool) {
	if len(logs) == 0 {
		return RoleUnknown, false
	}
	combined := strings.ToLower(strings.Join(logs, " "))
	if containsAny(combined, frontendLogKW) {
		return RoleFrontend, true
	}
	if containsAny(combined, backendLogKW) {
		return RoleBackend, true
	}
	return RoleUnknown, false
}

// containsAny 任意子串命中即返回 true。
func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
