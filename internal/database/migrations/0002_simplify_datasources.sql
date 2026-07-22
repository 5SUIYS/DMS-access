-- 简化 datasources 表：移除连接信息字段，仅保留 ARN 标记
-- DMS endpoint 本身已含全部连接信息，本地不需要冗余存储

ALTER TABLE datasources DROP COLUMN IF EXISTS env;
ALTER TABLE datasources DROP COLUMN IF EXISTS host;
ALTER TABLE datasources DROP COLUMN IF EXISTS port;
ALTER TABLE datasources DROP COLUMN IF EXISTS database_name;
ALTER TABLE datasources DROP COLUMN IF EXISTS username;
ALTER TABLE datasources DROP COLUMN IF EXISTS password_enc;
ALTER TABLE datasources DROP COLUMN IF EXISTS region;
ALTER TABLE datasources DROP COLUMN IF EXISTS extra_config;
ALTER TABLE datasources DROP COLUMN IF EXISTS created_by;

-- endpoint_arn 改为 NOT NULL（必须有）
ALTER TABLE datasources ALTER COLUMN endpoint_arn SET NOT NULL;

-- 删除无用索引
DROP INDEX IF EXISTS idx_datasources_type;
DROP INDEX IF EXISTS idx_datasources_env;
