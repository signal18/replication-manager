-- =============================================================
-- MariaDB test tables: various Primary Key data types
-- for replication manager testing
-- Re-runnable: uses CREATE OR REPLACE TABLE
-- =============================================================

CREATE DATABASE IF NOT EXISTS repl_test;
USE repl_test;

-- -------------------------------------------------------------
-- 1. TINYINT PK  (-128..127 / 0..255)
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_tinyint (
    id        TINYINT        NOT NULL AUTO_INCREMENT,
    label     VARCHAR(100)   NOT NULL,
    created   DATETIME       DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_tinyint (label) VALUES
    ('first'),
    ('second'),
    ('O''Brien'),
    ('tab	here'),
    ('emoji 🎯');

-- -------------------------------------------------------------
-- 2. SMALLINT PK
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_smallint (
    id        SMALLINT       NOT NULL AUTO_INCREMENT,
    label     VARCHAR(100)   NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_smallint (label) VALUES ('alpha'), ('beta'), ('gamma');

-- -------------------------------------------------------------
-- 3. INT / INTEGER PK  (most common)
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_int (
    id        INT            NOT NULL AUTO_INCREMENT,
    label     VARCHAR(100)   NOT NULL,
    payload   JSON,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_int (label, payload) VALUES
    ('row one',  '{"x":1}'),
    ('row two',  NULL),
    ('row three','{"nested":{"a":true}}');

-- -------------------------------------------------------------
-- 4. BIGINT PK
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_bigint (
    id        BIGINT         NOT NULL AUTO_INCREMENT,
    label     VARCHAR(100)   NOT NULL,
    amount    DECIMAL(18,4),
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_bigint (label, amount) VALUES
    ('invoice-1', 1234.5678),
    ('invoice-2', 0.0001),
    ('invoice-3', -9999999.9999);

-- -------------------------------------------------------------
-- 5. UNSIGNED BIGINT PK  (snowflake-style IDs)
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_ubigint (
    id        BIGINT UNSIGNED NOT NULL,
    label     VARCHAR(100)    NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_ubigint (id, label) VALUES
    (18446744073709551615, 'max uint64'),
    (1,                    'one'),
    (9223372036854775808,  'beyond int64');

-- -------------------------------------------------------------
-- 6. VARCHAR PK  (natural / business key)
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_varchar (
    id        VARCHAR(64)    NOT NULL,
    label     VARCHAR(255)   NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB COLLATE=utf8mb4_unicode_ci;

INSERT INTO pk_varchar (id, label) VALUES
    ('US',              'United States'),
    ('FR',              'France'),
    ('O''Reilly',       'publisher with quote in PK'),
    ('key with spaces', 'spaces in PK'),
    ('ключ',            'cyrillic PK');

-- -------------------------------------------------------------
-- 7. CHAR PK  (fixed-width codes)
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_char (
    id        CHAR(3)        NOT NULL,
    name      VARCHAR(100)   NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_char (id, name) VALUES
    ('USD', 'US Dollar'),
    ('EUR', 'Euro'),
    ('JPY', 'Japanese Yen');

-- -------------------------------------------------------------
-- 8. UUID PK stored as CHAR(36)
--    DEFAULT (UUID()) supported in MariaDB 10.6+
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_uuid_char (
    id        CHAR(36)       NOT NULL DEFAULT (UUID()),
    label     VARCHAR(100)   NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_uuid_char (id, label) VALUES
    (UUID(), 'first uuid row'),
    (UUID(), 'second uuid row'),
    ('00000000-0000-0000-0000-000000000001', 'fixed uuid');

-- -------------------------------------------------------------
-- 9. UUID PK stored as BINARY(16)  (compact, common in ORMs)
--    FIX: UUID_TO_BIN() cannot be used in DEFAULT clause in MariaDB
--    → column has no DEFAULT; INSERT must always supply the value
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_uuid_binary (
    id        BINARY(16)     NOT NULL,
    label     VARCHAR(100)   NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_uuid_binary (id, label) VALUES
    (UUID_TO_BIN(UUID()), 'binary uuid row 1'),
    (UUID_TO_BIN(UUID()), 'binary uuid row 2'),
    (UUID_TO_BIN('00000000-0000-0000-0000-000000000002'), 'fixed binary uuid');

-- -------------------------------------------------------------
-- 10. DATE PK
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_date (
    id        DATE           NOT NULL,
    event     VARCHAR(100)   NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_date (id, event) VALUES
    ('2024-01-01', 'New Year'),
    ('2024-07-04', 'Independence Day'),
    ('2024-12-25', 'Christmas');

-- -------------------------------------------------------------
-- 11. DATETIME PK  (microsecond precision)
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_datetime (
    id        DATETIME(6)    NOT NULL,
    label     VARCHAR(100)   NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_datetime (id, label) VALUES
    ('2024-01-01 00:00:00.000000', 'midnight'),
    ('2024-06-15 12:34:56.789012', 'microsecond precision'),
    (NOW(6),                       'current time');

-- -------------------------------------------------------------
-- 12. TIMESTAMP PK  (millisecond precision)
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_timestamp (
    id        TIMESTAMP(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    label     VARCHAR(100)   NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_timestamp (label) VALUES ('ts row 1');
SELECT SLEEP(0.01);   -- avoid duplicate PK on fast hardware
INSERT INTO pk_timestamp (label) VALUES ('ts row 2');

-- -------------------------------------------------------------
-- 13. DECIMAL PK  (financial / precision keys)
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_decimal (
    id        DECIMAL(10,4)  NOT NULL,
    label     VARCHAR(100)   NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_decimal (id, label) VALUES
    (1.0000,    'one'),
    (1.0001,    'one plus epsilon'),
    (9999.9999, 'max');

-- -------------------------------------------------------------
-- 14. ENUM PK  (rare but valid)
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_enum (
    id        ENUM('draft','published','archived') NOT NULL,
    label     VARCHAR(100)   NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

INSERT INTO pk_enum (id, label) VALUES
    ('draft',     'in progress'),
    ('published', 'live'),
    ('archived',  'retired');

-- -------------------------------------------------------------
-- 15. Composite PK: INT + VARCHAR
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_composite_int_varchar (
    tenant_id   INT           NOT NULL,
    user_key    VARCHAR(64)   NOT NULL,
    role        VARCHAR(50)   DEFAULT 'viewer',
    PRIMARY KEY (tenant_id, user_key)
) ENGINE=InnoDB;

INSERT INTO pk_composite_int_varchar (tenant_id, user_key, role) VALUES
    (1, 'alice',    'admin'),
    (1, 'bob',      'viewer'),
    (2, 'alice',    'admin'),
    (2, 'O''Brien', 'editor');

-- -------------------------------------------------------------
-- 16. Composite PK: BIGINT + DATE
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_composite_bigint_date (
    device_id   BIGINT        NOT NULL,
    reading_day DATE          NOT NULL,
    value       DOUBLE,
    PRIMARY KEY (device_id, reading_day)
) ENGINE=InnoDB;

INSERT INTO pk_composite_bigint_date (device_id, reading_day, value) VALUES
    (1001, '2024-01-01', 23.5),
    (1001, '2024-01-02', 24.1),
    (1002, '2024-01-01', 19.9);

-- -------------------------------------------------------------
-- 17. Composite PK: UUID CHAR(36) + TINYINT
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_composite_uuid_tinyint (
    session_id  CHAR(36)      NOT NULL,
    seq         TINYINT       NOT NULL,
    payload     TEXT,
    PRIMARY KEY (session_id, seq)
) ENGINE=InnoDB;

INSERT INTO pk_composite_uuid_tinyint (session_id, seq, payload) VALUES
    ('aaaaaaaa-0000-0000-0000-000000000001', 1, 'chunk one'),
    ('aaaaaaaa-0000-0000-0000-000000000001', 2, 'chunk two'),
    ('bbbbbbbb-0000-0000-0000-000000000001', 1, 'other session');

-- -------------------------------------------------------------
-- 18. Composite PK: ENUM + DATE
--     daily status snapshot (e.g. market open/close per day)
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_composite_enum_date (
    status       ENUM('open','closed','suspended')  NOT NULL,
    snapshot_day DATE                               NOT NULL,
    record_count INT            DEFAULT 0,
    PRIMARY KEY (status, snapshot_day)
) ENGINE=InnoDB;

INSERT INTO pk_composite_enum_date (status, snapshot_day, record_count) VALUES
    ('open',      '2024-01-01', 120),
    ('open',      '2024-01-02', 135),
    ('closed',    '2024-01-01',  45),
    ('suspended', '2024-01-01',   3);

-- -------------------------------------------------------------
-- 19. Composite PK: ENUM + DATETIME(6)
--     microsecond-precision event log partitioned by event type
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_composite_enum_datetime (
    event_type  ENUM('login','logout','error','warning')  NOT NULL,
    occurred_at DATETIME(6)                               NOT NULL,
    message     VARCHAR(255),
    PRIMARY KEY (event_type, occurred_at)
) ENGINE=InnoDB;

INSERT INTO pk_composite_enum_datetime (event_type, occurred_at, message) VALUES
    ('login',   '2024-06-01 08:00:00.000000', 'user alice'),
    ('login',   '2024-06-01 08:01:23.456789', 'user bob'),
    ('logout',  '2024-06-01 17:00:00.000000', 'user alice'),
    ('error',   '2024-06-01 09:15:00.123456', 'disk full'),
    ('warning', '2024-06-01 09:15:01.000000', 'retry attempt');

-- -------------------------------------------------------------
-- 20. Composite PK: ENUM + TIMESTAMP(3)
--     queue table partitioned by priority + millisecond enqueue time
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_composite_enum_timestamp (
    priority    ENUM('high','normal','low')  NOT NULL,
    enqueued_at TIMESTAMP(3)                NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    payload     TEXT,
    PRIMARY KEY (priority, enqueued_at)
) ENGINE=InnoDB;

INSERT INTO pk_composite_enum_timestamp (priority, enqueued_at, payload) VALUES
    ('high',   '2024-06-01 10:00:00.001', 'urgent job A'),
    ('high',   '2024-06-01 10:00:00.002', 'urgent job B'),
    ('normal', '2024-06-01 10:00:01.000', 'regular job'),
    ('low',    '2024-06-01 10:05:00.000', 'background job');

-- -------------------------------------------------------------
-- 21. Composite PK: ENUM + YEAR
--     annual budget / report table
-- -------------------------------------------------------------
CREATE OR REPLACE TABLE pk_composite_enum_year (
    department  ENUM('engineering','sales','marketing','ops')  NOT NULL,
    fiscal_year YEAR                                           NOT NULL,
    budget      DECIMAL(15,2),
    PRIMARY KEY (department, fiscal_year)
) ENGINE=InnoDB;

INSERT INTO pk_composite_enum_year (department, fiscal_year, budget) VALUES
    ('engineering', 2023, 1500000.00),
    ('engineering', 2024, 1750000.00),
    ('sales',       2023,  800000.00),
    ('sales',       2024,  950000.00),
    ('marketing',   2024,  300000.00);

-- =============================================================
-- Quick sanity-check selects
-- =============================================================
SELECT 'pk_tinyint'                     AS tbl, COUNT(*) AS rows FROM pk_tinyint
UNION ALL SELECT 'pk_smallint',                 COUNT(*) FROM pk_smallint
UNION ALL SELECT 'pk_int',                      COUNT(*) FROM pk_int
UNION ALL SELECT 'pk_bigint',                   COUNT(*) FROM pk_bigint
UNION ALL SELECT 'pk_ubigint',                  COUNT(*) FROM pk_ubigint
UNION ALL SELECT 'pk_varchar',                  COUNT(*) FROM pk_varchar
UNION ALL SELECT 'pk_char',                     COUNT(*) FROM pk_char
UNION ALL SELECT 'pk_uuid_char',                COUNT(*) FROM pk_uuid_char
UNION ALL SELECT 'pk_uuid_binary',              COUNT(*) FROM pk_uuid_binary
UNION ALL SELECT 'pk_date',                     COUNT(*) FROM pk_date
UNION ALL SELECT 'pk_datetime',                 COUNT(*) FROM pk_datetime
UNION ALL SELECT 'pk_timestamp',                COUNT(*) FROM pk_timestamp
UNION ALL SELECT 'pk_decimal',                  COUNT(*) FROM pk_decimal
UNION ALL SELECT 'pk_enum',                     COUNT(*) FROM pk_enum
UNION ALL SELECT 'pk_composite_int_varchar',    COUNT(*) FROM pk_composite_int_varchar
UNION ALL SELECT 'pk_composite_bigint_date',    COUNT(*) FROM pk_composite_bigint_date
UNION ALL SELECT 'pk_composite_uuid_tinyint',   COUNT(*) FROM pk_composite_uuid_tinyint
UNION ALL SELECT 'pk_composite_enum_date',      COUNT(*) FROM pk_composite_enum_date
UNION ALL SELECT 'pk_composite_enum_datetime',  COUNT(*) FROM pk_composite_enum_datetime
UNION ALL SELECT 'pk_composite_enum_timestamp', COUNT(*) FROM pk_composite_enum_timestamp
UNION ALL SELECT 'pk_composite_enum_year',      COUNT(*) FROM pk_composite_enum_year;
