#!/usr/bin/env bash
VERSION=$(git describe --abbrev=4 --always --tags)
TMPDIR=$(mktemp -d)

DISTRO=$(lsb_release -i -s)
RELEASE=$(lsb_release -r -s)
NAME="carbonzipper"

die() {
    if [[ $1 -eq 0 ]]; then
        rm -rf "${TMPDIR}"
    else
        echo "Temporary data stored at '${TMPDIR}'"
    fi
    echo "$2"
    exit $1
}

make || die 1 "Can't build package"
make DESTDIR="${TMPDIR}" install || die 1 "Can't install package"

# Determine if we are building for Ubuntu <15.04 and need to provide upstart script
is_upstart=0
if [[ "${DISTRO}" == "Ubuntu" ]]; then
	egrep -v -q '^(8|1[01234])\.' <<< ${RELEASE}
	is_upstart=$?
fi

if [[ ${is_upstart} -eq 0 ]]; then
       mkdir -p "${TMPDIR}"/etc/systemd/system/
       mkdir -p "${TMPDIR}"/etc/default/
       cp ./contrib/deb/${NAME}.service "${TMPDIR}"/etc/systemd/system/
       cp ./contrib/common/${NAME}.env "${TMPDIR}"/etc/default/${NAME}
else
       mkdir -p "${TMPDIR}"/etc/init/
       cp ./contrib/deb/${NAME}.conf "${TMPDIR}"/etc/init/
fi

fpm -s dir -t deb -n ${NAME} -v ${VERSION} -C ${TMPDIR} \
    -p ${NAME}_VERSION_ARCH.deb \
    --no-deb-systemd-restart-after-upgrade \
    --after-install contrib/fpm/systemd-reload.sh \
    --description "carbonserver proxy for graphite-web and carbonapi" \
    --license MIT \
    --url "https://github.com/go-graphite/" \
    "${@}" \
    etc usr/bin usr/share || die 1 "Can't create package!"

die 0 "Success"
