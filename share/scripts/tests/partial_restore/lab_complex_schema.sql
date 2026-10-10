-- Complex schema for partialRestore lab tests (MariaDB 10.6+).
-- Covers the common hot-restore pitfalls: generated columns, every
-- partitioning type, foreign keys, unusual keys and table options,
-- MariaDB-specific objects, non-InnoDB engines, routines/triggers/events/
-- views, and names stored encoded on disk.
DROP DATABASE IF EXISTS prt_lab;
DROP DATABASE IF EXISTS prt_lab_ref;
CREATE DATABASE prt_lab_ref CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE prt_lab CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

USE prt_lab_ref;
CREATE TABLE country (code CHAR(2) PRIMARY KEY, name VARCHAR(50)) ENGINE=InnoDB;
INSERT INTO country VALUES ('FR','France'),('ID','Indonesia'),('US','United States');

USE prt_lab;

-- ---- generated columns -----------------------------------------------------
CREATE TABLE gen_all (
  id INT PRIMARY KEY,
  price DECIMAL(10,2), qty INT,
  total DECIMAL(12,2) AS (price * qty) STORED,
  total_tax DECIMAL(12,2) AS (total * 1.2) VIRTUAL,
  doc JSON,
  sku VARCHAR(20) AS (JSON_VALUE(doc,'$.sku')) VIRTUAL,
  tag VARCHAR(20) AS (UPPER(JSON_VALUE(doc,'$.tag'))) STORED,
  KEY k_total (total), KEY k_sku (sku), KEY k_tag_total (tag, total)
) ENGINE=InnoDB;

-- ---- partitioning ------------------------------------------------------------
CREATE TABLE p_range (id INT, d DATE, v VARCHAR(20), PRIMARY KEY (id, d)) ENGINE=InnoDB
  PARTITION BY RANGE (YEAR(d)) (PARTITION p2024 VALUES LESS THAN (2025), PARTITION p2025 VALUES LESS THAN (2026), PARTITION pmax VALUES LESS THAN MAXVALUE);
CREATE TABLE p_list (id INT, region CHAR(2), PRIMARY KEY (id, region)) ENGINE=InnoDB
  PARTITION BY LIST COLUMNS (region) (PARTITION peu VALUES IN ('FR','DE'), PARTITION pasia VALUES IN ('ID','SG'), PARTITION pam VALUES IN ('US','CA'));
CREATE TABLE p_hash (id INT PRIMARY KEY, v VARCHAR(20)) ENGINE=InnoDB PARTITION BY HASH (id) PARTITIONS 4;
CREATE TABLE p_key (id INT, k VARCHAR(20), PRIMARY KEY (id, k)) ENGINE=InnoDB PARTITION BY KEY (k) PARTITIONS 3;
CREATE TABLE p_rcols (a INT, b INT, v VARCHAR(10), PRIMARY KEY (a, b)) ENGINE=InnoDB
  PARTITION BY RANGE COLUMNS (a, b) (PARTITION p0 VALUES LESS THAN (10, 10), PARTITION p1 VALUES LESS THAN (MAXVALUE, MAXVALUE));
CREATE TABLE p_gen (id INT, d DATE, y INT AS (YEAR(d)) VIRTUAL, m INT AS (MONTH(d)) STORED, PRIMARY KEY (id, d), KEY (m)) ENGINE=InnoDB
  PARTITION BY RANGE (YEAR(d)) (PARTITION p1 VALUES LESS THAN (2025), PARTITION p2 VALUES LESS THAN MAXVALUE);

-- ---- foreign keys ----------------------------------------------------------
CREATE TABLE fk_customer (id INT PRIMARY KEY, country CHAR(2),
  CONSTRAINT fk_cust_country FOREIGN KEY (country) REFERENCES prt_lab_ref.country (code)) ENGINE=InnoDB;
CREATE TABLE fk_order (id INT PRIMARY KEY, customer_id INT,
  CONSTRAINT fk_order_customer FOREIGN KEY (customer_id) REFERENCES fk_customer (id) ON DELETE CASCADE) ENGINE=InnoDB;
CREATE TABLE fk_order_line (order_id INT, line_no INT, amount DECIMAL(10,2), PRIMARY KEY (order_id, line_no),
  CONSTRAINT fk_line_order FOREIGN KEY (order_id) REFERENCES fk_order (id) ON DELETE CASCADE) ENGINE=InnoDB;
CREATE TABLE fk_employee (id INT PRIMARY KEY, manager_id INT NULL,
  CONSTRAINT fk_emp_manager FOREIGN KEY (manager_id) REFERENCES fk_employee (id) ON DELETE SET NULL) ENGINE=InnoDB;
CREATE TABLE fk_composite_parent (a INT, b INT, PRIMARY KEY (a, b)) ENGINE=InnoDB;
CREATE TABLE fk_composite_child (id INT PRIMARY KEY, a INT, b INT,
  CONSTRAINT fk_comp FOREIGN KEY (a, b) REFERENCES fk_composite_parent (a, b)) ENGINE=InnoDB;

-- ---- keys ------------------------------------------------------------------------
CREATE TABLE k_fulltext (id INT PRIMARY KEY, body TEXT, FULLTEXT KEY ft_body (body)) ENGINE=InnoDB;
CREATE TABLE k_spatial (id INT PRIMARY KEY, pt POINT NOT NULL, SPATIAL KEY sp_pt (pt)) ENGINE=InnoDB;
CREATE TABLE k_long (id INT PRIMARY KEY, u VARCHAR(768) CHARACTER SET utf8mb4, KEY k_u (u)) ENGINE=InnoDB;
CREATE TABLE k_long_unique (id INT PRIMARY KEY, doc TEXT, UNIQUE KEY uk_doc (doc)) ENGINE=InnoDB;
CREATE TABLE k_misc (id INT PRIMARY KEY, a INT, t TEXT, KEY k_desc (a DESC), KEY k_prefix (t(20))) ENGINE=InnoDB;
-- (No table without a primary key: repman-provisioned servers run with
-- innodb_force_primary_key=ON.)

-- ---- table options -------------------------------------------------------------
CREATE TABLE o_compressed (id INT PRIMARY KEY, v VARCHAR(200)) ENGINE=InnoDB ROW_FORMAT=COMPRESSED KEY_BLOCK_SIZE=8;
CREATE TABLE o_redundant (id INT PRIMARY KEY, v VARCHAR(200)) ENGINE=InnoDB ROW_FORMAT=REDUNDANT;
CREATE TABLE o_compact (id INT PRIMARY KEY, v VARCHAR(200)) ENGINE=InnoDB ROW_FORMAT=COMPACT;
CREATE TABLE o_page_compressed (id INT PRIMARY KEY, v VARCHAR(200)) ENGINE=InnoDB PAGE_COMPRESSED=1;
CREATE TABLE o_autoinc (id INT AUTO_INCREMENT PRIMARY KEY, v VARCHAR(20)) ENGINE=InnoDB AUTO_INCREMENT=1000;

-- ---- MariaDB features ---------------------------------------------------------
CREATE TABLE m_versioned (id INT PRIMARY KEY, v VARCHAR(20)) ENGINE=InnoDB WITH SYSTEM VERSIONING;
CREATE SEQUENCE m_seq START WITH 500 INCREMENT BY 5;
CREATE TABLE m_misc (
  id INT PRIMARY KEY,
  secret VARCHAR(20) INVISIBLE DEFAULT 'hidden',
  qty INT CHECK (qty >= 0),
  created DATETIME DEFAULT CURRENT_TIMESTAMP,
  code VARCHAR(10) DEFAULT (CONCAT('C', FLOOR(RAND()*0))),
  CONSTRAINT chk_code CHECK (code LIKE 'C%')
) ENGINE=InnoDB;

-- ---- other engines -------------------------------------------------------------
CREATE TABLE e_aria (id INT PRIMARY KEY, v VARCHAR(20)) ENGINE=Aria;
CREATE TABLE e_myisam (id INT PRIMARY KEY, v VARCHAR(20), FULLTEXT KEY ft (v)) ENGINE=MyISAM;
CREATE TABLE e_csv (id INT NOT NULL, v VARCHAR(20) NOT NULL) ENGINE=CSV;
CREATE TABLE e_memory (id INT PRIMARY KEY, v VARCHAR(20)) ENGINE=MEMORY;

-- ---- names stored encoded on disk and charsets -------------------------------
CREATE TABLE `n-dash` (id INT PRIMARY KEY, v VARCHAR(20)) ENGINE=InnoDB;
CREATE TABLE `n space` (id INT PRIMARY KEY, v VARCHAR(20)) ENGINE=InnoDB;
CREATE TABLE `n_ünïcödé` (id INT PRIMARY KEY, v VARCHAR(20)) ENGINE=InnoDB;
CREATE TABLE c_emoji (id INT PRIMARY KEY, v VARCHAR(50)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE c_latin1 (id INT PRIMARY KEY, v VARCHAR(50)) ENGINE=InnoDB DEFAULT CHARSET=latin1;
CREATE TABLE c_blob (id INT PRIMARY KEY, b LONGBLOB) ENGINE=InnoDB;
CREATE TABLE big (id INT AUTO_INCREMENT PRIMARY KEY, a INT, b VARCHAR(40), c DATETIME, KEY (a), KEY (b)) ENGINE=InnoDB;

-- ---- code objects --------------------------------------------------------------
CREATE TABLE audit (id INT AUTO_INCREMENT PRIMARY KEY, msg VARCHAR(100), at TIMESTAMP DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB;
DELIMITER ;;
CREATE PROCEDURE sp_log (IN m VARCHAR(100))
BEGIN
  DECLARE n INT DEFAULT 0;
  INSERT INTO audit (msg) VALUES (m);
  SELECT COUNT(*) INTO n FROM audit;
  IF n > 1000000 THEN DELETE FROM audit ORDER BY id LIMIT 1; END IF;
END;;
CREATE FUNCTION f_tax (x DECIMAL(10,2)) RETURNS DECIMAL(10,2) DETERMINISTIC SQL SECURITY INVOKER
BEGIN
  RETURN x * 1.2;
END;;
CREATE TRIGGER trg_order_ai AFTER INSERT ON fk_order FOR EACH ROW CALL sp_log(CONCAT('order ', NEW.id));;
CREATE TRIGGER trg_order_ai2 AFTER INSERT ON fk_order FOR EACH ROW FOLLOWS trg_order_ai INSERT INTO audit (msg) VALUES ('second');;
CREATE TRIGGER trg_gen_bu BEFORE UPDATE ON gen_all FOR EACH ROW SET NEW.qty = GREATEST(NEW.qty, 0);;
DELIMITER ;
CREATE VIEW v_orders AS SELECT o.id, o.customer_id, c.country FROM fk_order o JOIN fk_customer c ON c.id = o.customer_id;
CREATE VIEW v_orders_fr AS SELECT * FROM v_orders WHERE country = 'FR';
CREATE VIEW v_cross_db AS SELECT c.id, r.name FROM fk_customer c JOIN prt_lab_ref.country r ON r.code = c.country;
CREATE SQL SECURITY INVOKER VIEW v_tax AS SELECT id, f_tax(total) AS taxed FROM gen_all;
CREATE EVENT ev_recurring ON SCHEDULE EVERY 1 DAY STARTS '2030-01-01 00:00:00' ON COMPLETION PRESERVE COMMENT 'daily' DO CALL sp_log('tick');
CREATE EVENT ev_once ON SCHEDULE AT '2030-06-01 00:00:00' ON COMPLETION PRESERVE DO CALL sp_log('once');
CREATE EVENT ev_disabled ON SCHEDULE EVERY 1 HOUR STARTS '2030-01-01 00:00:00' DISABLE DO CALL sp_log('never');

-- ---- data ------------------------------------------------------------------------
INSERT INTO gen_all (id, price, qty, doc) VALUES (1, 10.50, 3, '{"sku":"A1","tag":"red"}'), (2, 7.25, 10, '{"sku":"B2","tag":"blue"}');
INSERT INTO p_range VALUES (1,'2024-03-01','a'),(2,'2025-07-01','b'),(3,'2030-01-01','c');
INSERT INTO p_list VALUES (1,'FR'),(2,'ID'),(3,'US'),(4,'DE');
INSERT INTO p_hash VALUES (1,'a'),(2,'b'),(3,'c'),(4,'d'),(5,'e');
INSERT INTO p_key VALUES (1,'x'),(2,'y'),(3,'z');
INSERT INTO p_rcols VALUES (1,1,'lo'),(20,20,'hi');
INSERT INTO p_gen (id, d) VALUES (1,'2024-02-02'),(2,'2026-08-08');
INSERT INTO fk_customer VALUES (1,'FR'),(2,'ID'),(3,'US');
INSERT INTO fk_order VALUES (10,1),(11,1),(12,2);
INSERT INTO fk_order_line VALUES (10,1,5.00),(10,2,7.50),(12,1,3.00);
INSERT INTO fk_employee VALUES (1,NULL),(2,1),(3,2);
INSERT INTO fk_composite_parent VALUES (1,1),(2,2); INSERT INTO fk_composite_child VALUES (1,1,1),(2,2,2);
INSERT INTO k_fulltext VALUES (1,'the quick brown fox'),(2,'jumps over the lazy dog');
INSERT INTO k_spatial VALUES (1, POINT(1,2)), (2, POINT(3,4));
INSERT INTO k_long VALUES (1, REPEAT('x', 700));
INSERT INTO k_long_unique VALUES (1, REPEAT('long unique value ', 50)), (2, 'short');
INSERT INTO k_misc VALUES (1, 5, 'prefix text one'), (2, 9, 'prefix text two');
INSERT INTO o_compressed VALUES (1, REPEAT('c', 150)); INSERT INTO o_redundant VALUES (1,'r'); INSERT INTO o_compact VALUES (1,'c');
INSERT INTO o_page_compressed VALUES (1, REPEAT('p', 150));
INSERT INTO o_autoinc (v) VALUES ('a'),('b'),('c');
INSERT INTO m_versioned VALUES (1,'v1'); UPDATE m_versioned SET v='v2' WHERE id=1; UPDATE m_versioned SET v='v3' WHERE id=1;
INSERT INTO m_misc (id, qty) VALUES (1, 5), (2, 0);
INSERT INTO e_aria VALUES (1,'aria'); INSERT INTO e_myisam VALUES (1,'myisam text'); INSERT INTO e_csv VALUES (1,'csv'); INSERT INTO e_memory VALUES (1,'mem');
INSERT INTO `n-dash` VALUES (1,'dash'); INSERT INTO `n space` VALUES (1,'space'); INSERT INTO `n_ünïcödé` VALUES (1,'unicode');
INSERT INTO c_emoji VALUES (1, 'emoji 😀🚀 ok'); INSERT INTO c_latin1 VALUES (1, 'café');
INSERT INTO c_blob VALUES (1, RANDOM_BYTES(1024)), (2, REPEAT(0x00FF, 50000));
INSERT INTO big (a, b, c) SELECT seq % 1000, MD5(seq), NOW() - INTERVAL seq SECOND FROM seq_1_to_100000;
SELECT NEXTVAL(m_seq), NEXTVAL(m_seq);
