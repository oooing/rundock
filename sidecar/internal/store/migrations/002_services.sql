-- 002_services.sql: 多服务模型
-- 一个项目(App)下可包含多个服务(前端/后端/DB)，每个服务对应一个监听端口。
-- 项目整体状态由各服务健康状态按"木桶原则"综合得出。

CREATE TABLE IF NOT EXISTS app_services (
    id           TEXT PRIMARY KEY,
    app_id       TEXT NOT NULL,              -- 所属项目
    app_run_id   TEXT NOT NULL,              -- 在哪次运行中发现
    port         INTEGER NOT NULL,           -- 监听端口
    url          TEXT NOT NULL DEFAULT '',   -- 推断的 URL，如 http://127.0.0.1:8765
    health       TEXT NOT NULL DEFAULT 'unknown',  -- healthy/unhealthy/unknown
    last_checked TEXT,
    detected_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_services_app ON app_services(app_id);
CREATE INDEX IF NOT EXISTS idx_services_run ON app_services(app_run_id);
CREATE INDEX IF NOT EXISTS idx_services_port ON app_services(port);
