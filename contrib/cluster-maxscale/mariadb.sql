GRANT REPLICATION SLAVE ON *.* TO 'repl';

CREATE USER 'maxscale' IDENTIFIED BY 'pass';
GRANT SELECT ON mysql.user TO 'maxscale';
GRANT SELECT ON mysql.db TO 'maxscale';
GRANT SELECT ON mysql.tables_priv TO 'maxscale';
GRANT SHOW DATABASES ON *.* TO 'maxscale';
GRANT REPLICATION CLIENT ON *.* TO 'maxscale';

FLUSH PRIVILEGES;

RESET MASTER;
