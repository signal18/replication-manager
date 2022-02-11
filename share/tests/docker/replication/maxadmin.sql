CREATE USER 'maxadmin'@'%' IDENTIFIED BY 'maxadmin';
GRANT SELECT ON mysql.user TO 'maxadmin'@'%';
GRANT SELECT ON mysql.db TO 'maxadmin'@'%';
GRANT SELECT ON mysql.tables_priv TO 'maxadmin'@'%';
GRANT SHOW DATABASES, REPLICATION CLIENT ON *.* TO 'maxadmin'@'%';
GRANT ALL ON maxscale_schema.* TO 'maxadmin'@'%';
