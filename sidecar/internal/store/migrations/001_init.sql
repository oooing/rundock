-- 001_init.sql: 初始 schema
-- Windows AI 启动平台 MVP 数据层

-- App 实体：一个被托管的启动单元
CREATE TABLE IF NOT EXISTS apps (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    entry_script    TEXT NOT NULL,          -- 拖入的脚本绝对路径
    cwd             TEXT NOT NULL,          -- 工作目录
    adapter_type    TEXT NOT NULL DEFAULT 'batch',  -- batch/ps1/npm/yarn/pnpm
    cmd             TEXT NOT NULL DEFAULT '',        -- prepare 后的可执行程序
    args_json       TEXT NOT NULL DEFAULT '[]',      -- 参数数组 JSON
    env_json        TEXT NOT NULL DEFAULT '{}',      -- 注入的环境变量 JSON
    tags_json       TEXT NOT NULL DEFAULT '[]',      -- 标签 JSON
    group_id        TEXT,                            -- 所属分组
    port_hints_json TEXT NOT NULL DEFAULT '[]',      -- 端口提示 JSON
    health_url      TEXT NOT NULL DEFAULT '',        -- 健康检查 URL（可空）
    script_hash     TEXT NOT NULL DEFAULT '',        -- 脚本内容 SHA256，用于白名单
    confirmed       INTEGER NOT NULL DEFAULT 0,      -- 是否已首次确认执行
    confirmed_hash  TEXT NOT NULL DEFAULT '',        -- 确认时对应的脚本哈希
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    last_started_at TEXT,
    last_url        TEXT,                            -- 最近一次识别到的 URL
    last_status     TEXT NOT NULL DEFAULT 'stopped', -- 缓存的最近状态
    sort_order      INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_apps_group ON apps(group_id);
CREATE INDEX IF NOT EXISTS idx_apps_status ON apps(last_status);

-- 分组
CREATE TABLE IF NOT EXISTS groups (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    color      TEXT NOT NULL DEFAULT '',
    order_json TEXT NOT NULL DEFAULT '[]',   -- 该组内的 App 启动顺序
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- 运行实例：每次启动产生一条
CREATE TABLE IF NOT EXISTS app_runs (
    id         TEXT PRIMARY KEY,
    app_id     TEXT NOT NULL,
    pid        INTEGER NOT NULL DEFAULT 0,
    root_pid   INTEGER NOT NULL DEFAULT 0,
    status     TEXT NOT NULL DEFAULT 'starting',  -- starting/running/degraded/stopped/failed
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    stopped_at TEXT,
    exit_code  INTEGER
);

CREATE INDEX IF NOT EXISTS idx_runs_app ON app_runs(app_id);
CREATE INDEX IF NOT EXISTS idx_runs_status ON app_runs(status);

-- 日志：原始流全量
CREATE TABLE IF NOT EXISTS logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    app_run_id TEXT NOT NULL,
    ts         TEXT NOT NULL DEFAULT (datetime('now')),
    stream     TEXT NOT NULL,        -- stdout/stderr/event
    level      TEXT NOT NULL DEFAULT 'info',
    text       TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_logs_run ON logs(app_run_id, id);
CREATE INDEX IF NOT EXISTS idx_logs_ts ON logs(app_run_id, ts);

-- 端口发现记录
CREATE TABLE IF NOT EXISTS ports (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    app_run_id   TEXT NOT NULL,
    port         INTEGER NOT NULL,
    proto        TEXT NOT NULL DEFAULT 'tcp',
    detected_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_ports_run ON ports(app_run_id);

-- 设置 k/v
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- 默认设置
INSERT OR IGNORE INTO settings (key, value) VALUES ('grace_period_seconds', '8');
INSERT OR IGNORE INTO settings (key, value) VALUES ('health_check_interval_seconds', '3');
INSERT OR IGNORE INTO settings (key, value) VALUES ('health_check_timeout_seconds', '15');
INSERT OR IGNORE INTO settings (key, value) VALUES ('url_discover_timeout_seconds', '30');
INSERT OR IGNORE INTO settings (key, value) VALUES ('log_retention_per_run', '5000');
