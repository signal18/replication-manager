mariadb-msm(1) -- MariaDB multisource replication monitor
=========================================================

## NAME

mariadb-msm -- MariaDB multisource replication monitor

## SYNOPSIS

`mariadb-msm [OPTIONS]`

## DESCRIPTION

**mariadb-msm** is a tiny monitoring program for MariaDB multisource replication.
It monitors each multisource replication channel and sends email alerts if one of the replication channels stops.

## EXAMPLES

`mariadb-msm -email remotedba@mariadb.com -user root:password -interval 5 -socket /var/lib/mysql/mysql.sock`

Monitor continuously localhost at 5 minute intervals and send email alerts in case of errors.

`mariadb-msm -host 192.168.0.1:3306 -user root:password -verbose`

Print multisource replication delay and errors for remote host 192.168.0.1 and exit.

## OPTIONS

  * -email `<email>`

    Destination email address for alerts

  * -from `<email>`

    Sender name and email

  * -host `<address>`

    MariaDB host IP and port (optional), specified in the host:[port] format

  * -interval `<seconds>`

    Optional monitoring interval in seconds

  * -socket `<path>`

  Path of MariaDB unix socket

  * -user

    User for MariaDB login, specified in the [user]:[password] format

  * -verbose
   
    Print detailed execution info

  * -version

    Return softawre version

## SYSTEM REQUIREMENTS

`mariadb-msm` is a self-contained binary, which means that no dependencies are needed at the operating system level.
On the MariaDB side, slaves need to use multi-source replication, whether it is GTID or positional.

## BUGS

Check https://github.com/tanji/mariadb-tools/issues for a list of issues.

## AUTHOR

Guillaume Lefranc <guillaume@mariadb.com>

## COPYRIGHT

THIS PROGRAM IS PROVIDED “AS IS” AND WITHOUT ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, WITHOUT LIMITATION, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE.

This program is free software; you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, version 2; OR the Perl Artistic License. On UNIX and similar systems, you can issue `man perlgpl` or `man perlartistic` to read these licenses.

You should have received a copy of the GNU General Public License along with this program; if not, write to the Free Software Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA 02111-1307 USA.

## VERSION

**mariadb-msm** 0.1.3
