#!/bin/sh
# Builds the xtrabackup bundle for the database jobs container.
#
# Runs as root inside the official xtrabackup image chosen with prov-db-docker-xtrabackup-img
# (OpenSVC init container, Kubernetes init container), with the destination volume mounted at
# /bundle and the image reference in XB_IMAGE. It copies xtrabackup, xbstream and socat with the
# shared libraries and the dynamic loader they need, and writes one wrapper per tool that runs the
# tool through the bundled loader: the bundle does not depend on the glibc of the image that
# runs the jobs, and LD_LIBRARY_PATH is never set. Layout of /bundle:
#   current/bin/<tool>      wrappers, the only thing the jobs container puts on its PATH (last)
#   current/libexec/<tool>  the real binaries
#   current/lib/            libraries and the loader
#   current/manifest        image=<reference>, versions, build date
#   status                  "ok <image>" or "failed <reason>"
# The copy is bounded (MAXKB): the destination must have MAXKB + FLOORKB free, the running total is checked
# before each file, and the build never leaves the destination with less than FLOORKB free. The jobs container mounts it read-only at MOUNT. It never fails the init container (the
# database must start without it): a failure is written to the log and to the status file, and leaves no bundle (the
# previous one is removed: it could be the tools of another series).
DST=/bundle
MOUNT="@MOUNT@"
SRC="${XB_IMAGE:-unknown}"
TOOLS="xtrabackup xbstream socat"
MAXKB=262144
# kept out of the copy budget for what is written after the copies: the three wrappers, the manifest and the
# filesystem blocks they take, so that the whole bundle stays within MAXKB
RESERVEKB=1024
# the free space the destination must keep beyond the bundle: on OpenSVC it is the database's own data volume
FLOORKB=131072

fail() {
    echo "xtrabackup bundle: $*" >&2
    printf 'failed %s\n' "$*" >"$DST/status" 2>/dev/null
    # the previous bundle goes too: it is on the PATH of the jobs container, and after an image change it may be the
    # tools of another series. No tools are better than the wrong ones: the missing tool is then reported as missing.
    rm -rf "$DST/.stage" "$DST/current" "$DST/.old" 2>/dev/null
    exit 0
}

[ -d "$DST" ] && [ -w "$DST" ] || fail "$DST is not a writable mount"

# XB_SERIES is the series (major.minor) of the database server, set by repman when the database image tag names one.
# The bundle must be the xtrabackup of that series: xtrabackup 8.0 does not back up an 8.4 server, nor the reverse.
# series_mismatch <xtrabackup version text> prints the reason when the text names another series; it prints nothing,
# and the check passes, when no series is expected or the text names none.
series_mismatch() {
    [ -n "$XB_SERIES" ] || return 0
    have=$(printf '%s' "$1" | sed -n 's/.*based on MySQL server \([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | head -1)
    [ -z "$have" ] || [ "$have" = "$XB_SERIES" ] || printf 'xtrabackup is for the MySQL series %s but the database image is %s' "$have" "$XB_SERIES"
}

# a swap interrupted between "current -> .old" and "stage -> current" leaves no current but the previous bundle in .old:
# put it back before anything is removed
[ -d "$DST/current" ] || { [ -d "$DST/.old" ] && mv "$DST/.old" "$DST/current"; }

# literal comparison (-F): an image reference holds dots, which a regular expression would take for wildcards
if [ -f "$DST/current/manifest" ] && grep -Fqx "image=$SRC" "$DST/current/manifest"; then
    WHY=$(series_mismatch "$(sed -n 's/^xtrabackup=//p' "$DST/current/manifest")")
    [ -z "$WHY" ] || fail "$WHY ($SRC)"
    echo "xtrabackup bundle: $SRC is already in place"
    printf 'ok %s\n' "$SRC" >"$DST/status"
    exit 0
fi

STAGE="$DST/.stage"
rm -rf "$STAGE" "$DST/.old"

# The destination is the database's own volume on OpenSVC: never start a copy the volume cannot hold with
# the bound to spare, and never let the copy grow past the bound before it is checked.
AVAILKB=$(df -Pk "$DST" 2>/dev/null | awk 'NR==2{print $4}')
case "$AVAILKB" in ''|*[!0-9]*) fail "cannot read the free space of $DST" ;; esac
[ "$AVAILKB" -ge $((MAXKB + FLOORKB)) ] || fail "only ${AVAILKB} kB free on $DST, $((MAXKB + FLOORKB)) kB are needed to build the bundle and leave ${FLOORKB} kB free"

mkdir -p "$STAGE/bin" "$STAGE/libexec" "$STAGE/lib" || fail "cannot create $STAGE"

USEDKB=0
# take <source> <destination>: copies one file once the running total, with it, is within the bound
take() {
    kb=$(du -Lk "$1" 2>/dev/null | cut -f1)
    case "$kb" in ''|*[!0-9]*) fail "cannot size $1" ;; esac
    USEDKB=$((USEDKB + kb))
    [ "$USEDKB" -le $((MAXKB - RESERVEKB)) ] || fail "the bundle would be over the ${MAXKB} kB bound at $1"
    cp -L "$1" "$2" || fail "cannot copy $1"
}

LOADER=""
for t in $TOOLS; do
    p=$(command -v "$t") || fail "$t is not in the image $SRC"
    take "$p" "$STAGE/libexec/$t"
    deps=$(ldd "$p" 2>&1)
    # a library that ldd cannot find would only show later, when the tool fails to run: say it now, and which one
    case "$deps" in
    *"not found"*) fail "$t needs a library that is not in the image $SRC: $(printf '%s\n' "$deps" | grep 'not found' | head -1 | tr -d '\t')" ;;
    esac
    for l in $(printf '%s\n' "$deps" | awk '/=> \//{print $3} /^[ \t]*\//{print $1}'); do
        [ -e "$STAGE/lib/$(basename "$l")" ] && continue
        take "$l" "$STAGE/lib/"
        case "$(basename "$l")" in ld-linux*|ld-musl*) LOADER=$(basename "$l") ;; esac
    done
done
[ -n "$LOADER" ] || fail "no dynamic loader among the libraries of $SRC (statically linked image?)"

for t in $TOOLS; do
    cat >"$STAGE/bin/$t" <<WRAP || fail "cannot write the wrapper of $t"
#!/bin/sh
exec $MOUNT/current/lib/$LOADER --library-path $MOUNT/current/lib $MOUNT/current/libexec/$t "\$@"
WRAP
    chmod 755 "$STAGE/bin/$t" || fail "cannot make the wrapper of $t executable"
done

# every tool must run through the bundled loader before the bundle replaces the previous one
run() { t=$1; shift; "$STAGE/lib/$LOADER" --library-path "$STAGE/lib" "$STAGE/libexec/$t" "$@" 2>&1; }
# socat first: dbjobs_new.sh cannot run, nor reach repman, nor receive its own upgrade without it
run socat -V >/dev/null || fail "socat does not run from the bundle: the jobs script cannot run without it"
XBV=$(run xtrabackup --version) || fail "xtrabackup does not run from the bundle: $XBV"
# keep only the version, not the temporary path the binary was run from
XBV=$(echo "$XBV" | tail -1 | sed 's#^.*xtrabackup version#xtrabackup version#')
WHY=$(series_mismatch "$XBV")
[ -z "$WHY" ] || fail "$WHY ($SRC): set prov-db-docker-xtrabackup-img to the xtrabackup image of the server series"
run xbstream --version >/dev/null || fail "xbstream does not run from the bundle"

KB=$(du -sk "$STAGE" | cut -f1)
[ "$KB" -le "$MAXKB" ] || fail "the bundle is ${KB} kB, over the ${MAXKB} kB bound"

{
    echo "image=$SRC"
    echo "xtrabackup=$XBV"
    echo "loader=$LOADER"
    echo "size_kb=$KB"
    echo "built=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} >"$STAGE/manifest" || fail "cannot write the manifest"
chmod -R a+rX "$STAGE" || fail "cannot set the permissions of the bundle"
# the manifest is what marks a bundle as installed: it must be there, and the whole bundle, with it, within the bound
[ -s "$STAGE/manifest" ] || fail "the manifest is empty"
KB=$(du -sk "$STAGE" | cut -f1)
[ "$KB" -le "$MAXKB" ] || fail "the bundle is ${KB} kB with its manifest, over the ${MAXKB} kB bound"

[ -d "$DST/current" ] && mv "$DST/current" "$DST/.old"
mv "$STAGE" "$DST/current" || { [ -d "$DST/.old" ] && mv "$DST/.old" "$DST/current"; fail "cannot put the bundle in place"; }
rm -rf "$DST/.old"
printf 'ok %s\n' "$SRC" >"$DST/status" || echo "xtrabackup bundle: installed, but the status file cannot be written" >&2
echo "xtrabackup bundle: $SRC installed (${KB} kB), $XBV"
exit 0
