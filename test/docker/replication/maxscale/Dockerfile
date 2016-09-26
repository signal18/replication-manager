FROM centos:7
MAINTAINER Guillaume Lefranc <guillaume@mariadb.com>

ARG mdbe_token

COPY maxscale-2.0.1-1.centos.7.x86_64.rpm . 
RUN yum -y localinstall maxscale-2.0.1-1.centos.7.x86_64.rpm

COPY maxscale.cnf /etc/maxscale.cnf

EXPOSE 6603 4442 4006 4008
CMD ["maxscale", "-d", "--log=stdout"] 
