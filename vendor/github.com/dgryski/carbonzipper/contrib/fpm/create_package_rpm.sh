#!/usr/bin/env bash
VERSION_GIT=$(git describe --abbrev=4 --always --tags | rev | sed 's/-/./' | rev) 
VERSION=$(cut -d'-' -f 1 <<< ${VERSION_GIT})
RELEASE=$(cut -d'-' -f 2 <<< ${VERSION_GIT})
if [[ "${VERSION}" == "${RELEASE}" ]]; then
       RELEASE="1"
else
       REL_VERSION=$(cut -d'.' -f 1 <<< ${RELEASE})
       REL_COMMIT=$(cut -d'.' -f 2 <<< ${RELEASE})
       RELEASE="$((REL_VERSION+1)).${REL_COMMIT}"
fi
TMPDIR=$(mktemp -d)
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

MAJOR_DISTRO_VERSION=$(lsb_release -s -r | cut -c 1)

make || die 1 "Can't build package"
make DESTDIR="${TMPDIR}" install || die 1 "Can't install package"
mkdir -p "${TMPDIR}"/etc/sysconfig/
cp ./contrib/common/${NAME}.env "${TMPDIR}"/etc/sysconfig/${NAME}
if [[ "${MAJOR_DISTRO_VERSION}" -le 6 ]]; then
	mkdir -p "${TMPDIR}"/init.d
	cp ./contrib/rhel/${NAME}.init "${TMPDIR}"/etc/init.d/${NAME}
else
	mkdir -p "${TMPDIR}"/etc/systemd/system/
	cp ./contrib/rhel/${NAME}.service "${TMPDIR}"/etc/systemd/system/
fi


fpm -s dir -t rpm -n ${NAME} -v ${VERSION} -C ${TMPDIR} \
    --iteration ${RELEASE} \
    -p ${NAME}_VERSION-ITERATION_ARCH.rpm \
    --after-install contrib/fpm/systemd-reload.sh \
    --description "carbonserver proxy for graphite-web and carbonapi" \
    --license MIT \
    --url "https://github.com/go-graphite/" \
    "${@}" \
    etc usr/bin usr/share || die "Can't create package!"

die 0 "Success"
