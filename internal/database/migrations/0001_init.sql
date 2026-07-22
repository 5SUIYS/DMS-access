-- 初始迁移：创建 DMS-access 所有核心表
-- 对应设计文档 Data Models 章节

CREATE TABLE IF NOT EXISTS users (
    id          BIGSERIAL PRIMARY KEY,
    username    VARCHAR(64)  NOT NULL UNIQUE,
    email       VARCHAR(128),
    uniauth_uid VARCHAR(128) NOT NULL UNIQUE,
    perm_mask   VARCHAR(256),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS datasources (
    id              BIGSERIAL PRIMARY KEY,
    name            VARCHAR(128) NOT NULL,
    type            VARCHAR(32)  NOT NULL,
    env             VARCHAR(32)  NOT NULL,
    host            VARCHAR(256) NOT NULL,
    port            INT          NOT NULL,
    database_name   VARCHAR(128),
    username        VARCHAR(128) NOT NULL,
    password_enc    TEXT         NOT NULL,
    region          VARCHAR(64),
    endpoint_arn    VARCHAR(512),
    extra_config    JSONB,
    created_by      BIGINT REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_datasources_type ON datasources(type);
CREATE INDEX IF NOT EXISTS idx_datasources_env  ON datasources(env);

CREATE TABLE IF NOT EXISTS tickets (
    id                  BIGSERIAL PRIMARY KEY,
    title               VARCHAR(256),
    status              VARCHAR(32) NOT NULL DEFAULT 'draft',
    src_datasource_id   BIGINT NOT NULL REFERENCES datasources(id),
    dst_datasource_id   BIGINT NOT NULL REFERENCES datasources(id),
    target_schema       VARCHAR(128) NOT NULL,
    migration_type      VARCHAR(32)  NOT NULL,
    table_selections    JSONB        NOT NULL DEFAULT '[]',
    reason              TEXT,
    submitter_id        BIGINT REFERENCES users(id),
    submitted_at        TIMESTAMPTZ,
    reviewer_id         BIGINT REFERENCES users(id),
    reviewed_at         TIMESTAMPTZ,
    review_comment      TEXT,
    executor_id         BIGINT REFERENCES users(id),
    executed_at         TIMESTAMPTZ,
    dms_task_arn        VARCHAR(512),
    dms_task_status     VARCHAR(64),
    error_detail        TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tickets_status       ON tickets(status);
CREATE INDEX IF NOT EXISTS idx_tickets_submitter_id ON tickets(submitter_id);

CREATE TABLE IF NOT EXISTS dms_plans (
    id                       BIGSERIAL PRIMARY KEY,
    ticket_id                BIGINT NOT NULL UNIQUE REFERENCES tickets(id),
    src_endpoint_arn         VARCHAR(512) NOT NULL,
    dst_endpoint_arn         VARCHAR(512) NOT NULL,
    replication_instance_arn VARCHAR(512) NOT NULL,
    migration_type           VARCHAR(32)  NOT NULL,
    table_mappings_json      TEXT         NOT NULL,
    task_settings_json       TEXT,
    precondition_warnings    JSONB,
    generated_by             BIGINT REFERENCES users(id),
    generated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    ticket_id   BIGINT REFERENCES tickets(id),
    operator_id BIGINT REFERENCES users(id),
    action      VARCHAR(64) NOT NULL,
    detail      JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_ticket_id ON audit_logs(ticket_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_operator_id ON audit_logs(operator_id);
