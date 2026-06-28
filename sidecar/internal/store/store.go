// Package store 提供 SQLite 持久化层。
// sidecar 启动时打开数据库并执行 migrations，之后所有读写都通过 Store 进行。
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store 包装 *sql.DB，提供类型化的数据访问。
type Store struct {
	db *sql.DB
}

// Open 打开指定路径的 SQLite 数据库并执行 migrations。
// Open 会自动创建父目录。busyTimeout 防止并发写时立即失败。
func Open(dbPath string) (*Store, error) {
	// 确保父目录存在
	if dir := filepath.Dir(dbPath); dir != "" {
		_ = mkdirAll(dir)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite 单写多读，连接池调小避免锁竞争报错
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// DB 暴露底层 *sql.DB 供需要自定义查询的模块使用。
func (s *Store) DB() *sql.DB { return s.db }

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

// migrate 按文件名顺序执行 migrations/*.sql。
// 采用简单的"一次性执行"策略：所有脚本都是幂等的（CREATE IF NOT EXISTS / INSERT OR IGNORE），
// 因此重复执行安全。后续如需 schema 演进可引入 schema_version 表。
func (s *Store) migrate() error {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		content, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := s.db.Exec(string(content)); err != nil {
			return fmt.Errorf("exec migration %s: %w", name, err)
		}
	}
	return nil
}
