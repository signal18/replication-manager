#!/bin/sh
# replication-manager - volume snapshot helper (#2443)
# Copyright 2017-2026 SIGNAL18 CLOUD SAS
# This source code is licensed under the GNU General Public License, version 3.
#
# Takes, lists, deletes and rolls back (snapback) a snapshot of the volume that holds the files
# of a database server: a ZFS dataset (recursive) or an LVM logical volume. replication-manager
# runs it on the database host (over the on-premise SSH connection, or locally for the localhost
# orchestrator), piped into sh: it is POSIX sh, it needs no file of its own on the host.
#
# Contract (the same for a client's backup-snapshot-script and an external plugin):
#   argument 1                 the action: take, list, delete, snapback
#   REPMAN_SNAPSHOT_TYPE       zfs or lvm
#   REPMAN_SNAPSHOT_TARGET     the volume (pool/dataset, vg/lv); empty: the one holding the paths
#   REPMAN_SNAPSHOT_PATHS      the server's file directories, colon separated (datadir, redo...)
#   REPMAN_SNAPSHOT_NAME       the snapshot of take, delete and snapback
#   REPMAN_SNAPSHOT_LVM_SIZE   the copy-on-write space of a snapshot of a thick LV (20%ORIGIN)
#   REPMAN_SNAPSHOT_SUDO       optional: the prefix of the commands (default sudo -n unless root)
# Standard output, tab separated records, everything else is log text:
#   CMD <command>              each command, printed before it runs: it can be replayed by hand
#   TARGET <volume>            the volume resolved
#   SNAPSHOT <name> <created, unix time> <size, bytes>
#   CHANGED                    a snapback changed the volume (an error after it is half-way)
# An error is one line on standard error and exit status 1. Nothing here starts or stops the
# database: replication-manager stops it before a snapback and starts it after.

set -u

ACTION="${1:-}"
TYPE="${REPMAN_SNAPSHOT_TYPE:-}"
TARGET="${REPMAN_SNAPSHOT_TARGET:-}"
PATHS="${REPMAN_SNAPSHOT_PATHS:-}"
NAME="${REPMAN_SNAPSHOT_NAME:-}"
LVM_SIZE="${REPMAN_SNAPSHOT_LVM_SIZE:-20%ORIGIN}"

TAB=$(printf '\t')
# root runs the commands itself, another user through sudo -n; REPMAN_SNAPSHOT_SUDO, when set,
# is the prefix (empty for none, doas...)
if [ "${REPMAN_SNAPSHOT_SUDO+set}" = "set" ]; then
    SUDO="$REPMAN_SNAPSHOT_SUDO"
elif [ "$(id -u)" -ne 0 ]; then
    SUDO="sudo -n"
else
    SUDO=""
fi
# the records go to the real standard output, also from inside $(...)
exec 3>&1

die() {
    echo "$*" >&2
    exit 1
}

# x CMD...: print the command, then run it
x() {
    cmdline "$@" >&3
    $SUDO "$@"
}

# cmdline CMD...: the CMD record of a command
cmdline() {
    if [ -n "$SUDO" ]; then
        printf 'CMD\t%s %s\n' "$SUDO" "$*"
    else
        printf 'CMD\t%s\n' "$*"
    fi
}

valid_name() {
    case "$1" in
    "" | -* | *[!A-Za-z0-9._-]*) die "invalid snapshot name '$1'" ;;
    esac
}

# mount_source PATH: the device or dataset mounted at the mount point holding PATH, and its type
mount_source() {
    command -v findmnt >/dev/null 2>&1 || die "findmnt not found on the database host"
    [ -e "$1" ] || die "path $1 does not exist on the database host"
    findmnt -n -o SOURCE,FSTYPE -T "$1" | head -n 1
}

##########
# ZFS    #
##########

zfs_resolve() {
    command -v zfs >/dev/null 2>&1 || die "zfs not found on the database host"
    if [ -n "$TARGET" ]; then
        x zfs list -H -o name "$TARGET" >/dev/null || die "dataset $TARGET not found"
        printf 'TARGET\t%s\n' "$TARGET"
        return
    fi
    [ -n "$PATHS" ] || die "no backup-snapshot-target and no server path to find the dataset"
    common=""
    oldifs=$IFS
    IFS=:
    for p in $PATHS; do
        IFS=$oldifs
        [ -n "$p" ] || continue
        src=$(mount_source "$p") || exit 1
        set -- $src
        [ "${2:-}" = "zfs" ] || die "path $p is on ${2:-an unknown filesystem} (${1:-?}), not on ZFS: set backup-snapshot-target or backup-snapshot-plugin"
        ds="${1%%@*}"
        if [ -z "$common" ]; then
            common="$ds"
        else
            # the deepest dataset holding both
            while [ "$ds" != "$common" ] && [ "${ds#"$common"/}" = "$ds" ]; do
                case "$common" in
                */*) common="${common%/*}" ;;
                *) die "the server's files are on several pools ($common, $ds): set backup-snapshot-target" ;;
                esac
            done
        fi
        IFS=:
    done
    IFS=$oldifs
    [ -n "$common" ] || die "no dataset found for $PATHS"
    TARGET="$common"
    printf 'TARGET\t%s\n' "$TARGET"
}

zfs_list() {
    x zfs list -H -p -t snapshot -d 1 -o name,creation,used -s creation "$TARGET" | while IFS="$TAB" read -r n c u; do
        printf 'SNAPSHOT\t%s\t%s\t%s\n' "${n#*@}" "$c" "$u"
    done
}

zfs_has() {
    $SUDO zfs list -H -o name "$1@$NAME" >/dev/null 2>&1
}

zfs_take() {
    x zfs snapshot -r "$TARGET@$NAME" || die "zfs snapshot of $TARGET@$NAME failed"
    x zfs list -H -p -t snapshot -o name,creation,used "$TARGET@$NAME" | while IFS="$TAB" read -r n c u; do
        printf 'SNAPSHOT\t%s\t%s\t%s\n' "${n#*@}" "$c" "$u"
    done
}

zfs_delete() {
    zfs_has "$TARGET" || die "snapshot $TARGET@$NAME not found"
    x zfs destroy -r "$TARGET@$NAME" || die "zfs destroy of $TARGET@$NAME failed"
}

zfs_snapback() {
    zfs_has "$TARGET" || die "snapshot $TARGET@$NAME not found"
    datasets=$($SUDO zfs list -H -o name -r -t filesystem,volume "$TARGET") || die "can not list the datasets of $TARGET"
    # every dataset of the tree must hold the snapshot, or the rollback would be partial
    for ds in $datasets; do
        zfs_has "$ds" || die "dataset $ds has no snapshot $NAME (created after it): nothing was changed"
    done
    printf 'CHANGED\n'
    for ds in $datasets; do
        # -r: the snapshots taken after it are destroyed, zfs can roll back to the latest only
        x zfs rollback -r "$ds@$NAME" || die "zfs rollback of $ds@$NAME failed"
    done
}

##########
# LVM    #
##########

lvm_lv_of() {
    $SUDO lvs --noheadings -o vg_name,lv_name "$1" 2>/dev/null | awk 'NR==1 { print $1 "/" $2 }'
}

lvm_resolve() {
    command -v lvs >/dev/null 2>&1 || die "lvs not found on the database host"
    if [ -n "$TARGET" ]; then
        lv=$(lvm_lv_of "$TARGET")
        [ -n "$lv" ] || die "logical volume $TARGET not found"
        cmdline lvs "$TARGET"
        TARGET="$lv"
        printf 'TARGET\t%s\n' "$TARGET"
        return
    fi
    [ -n "$PATHS" ] || die "no backup-snapshot-target and no server path to find the logical volume"
    found=""
    oldifs=$IFS
    IFS=:
    for p in $PATHS; do
        IFS=$oldifs
        [ -n "$p" ] || continue
        src=$(mount_source "$p") || exit 1
        set -- $src
        lv=$(lvm_lv_of "${1:-}")
        [ -n "$lv" ] || die "path $p is on ${1:-an unknown device}, not on an LVM logical volume: set backup-snapshot-target or backup-snapshot-plugin"
        if [ -z "$found" ]; then
            found="$lv"
        elif [ "$found" != "$lv" ]; then
            die "the server's files are on several logical volumes ($found, $lv): a snapshot of one would not be consistent"
        fi
        IFS=:
    done
    IFS=$oldifs
    [ -n "$found" ] || die "no logical volume found for $PATHS"
    TARGET="$found"
    printf 'TARGET\t%s\n' "$TARGET"
}

# lvm_snapshots: name, origin, time, size of every snapshot of TARGET
lvm_snapshots() {
    vg="${TARGET%%/*}"
    lv="${TARGET#*/}"
    x lvs --noheadings --separator "$TAB" --units b --nosuffix -o lv_name,origin,lv_time,lv_size "$vg" | while IFS="$TAB" read -r n o t s; do
        n=$(echo "$n" | tr -d ' ')
        o=$(echo "$o" | tr -d ' ')
        [ "$o" = "$lv" ] || continue
        c=$(date -d "$t" +%s 2>/dev/null || echo 0)
        printf '%s\t%s\t%s\n' "$n" "$c" "$(echo "$s" | tr -d ' ')"
    done
}

lvm_list() {
    lvm_snapshots | sort -t "$TAB" -k 2,2n | while IFS="$TAB" read -r n c s; do
        printf 'SNAPSHOT\t%s\t%s\t%s\n' "$n" "$c" "$s"
    done
}

lvm_has() {
    lvm_snapshots 3>/dev/null | cut -f 1 | grep -qx "$NAME"
}

lvm_take() {
    vg="${TARGET%%/*}"
    seg=$($SUDO lvs --noheadings -o segtype "$TARGET" | tr -d ' ')
    if [ "$seg" = "thin" ]; then
        x lvcreate -s -n "$NAME" "$TARGET" || die "lvcreate of thin snapshot $vg/$NAME failed"
    else
        case "$LVM_SIZE" in
        *%*) x lvcreate -s -l "$LVM_SIZE" -n "$NAME" "$TARGET" || die "lvcreate of snapshot $vg/$NAME failed" ;;
        *) x lvcreate -s -L "$LVM_SIZE" -n "$NAME" "$TARGET" || die "lvcreate of snapshot $vg/$NAME failed" ;;
        esac
    fi
    lvm_snapshots 3>/dev/null | while IFS="$TAB" read -r n c s; do
        [ "$n" = "$NAME" ] && printf 'SNAPSHOT\t%s\t%s\t%s\n' "$n" "$c" "$s"
    done
}

lvm_delete() {
    lvm_has || die "snapshot $NAME of $TARGET not found"
    x lvremove -y "${TARGET%%/*}/$NAME" || die "lvremove of ${TARGET%%/*}/$NAME failed"
}

lvm_snapback() {
    lvm_has || die "snapshot $NAME of $TARGET not found"
    dev=$($SUDO lvs --noheadings -o lv_path "$TARGET" | tr -d ' ')
    [ -n "$dev" ] || die "no device path for $TARGET"
    # the mount points of the volume, deepest first: unmounted before the merge, mounted after
    mnts=$(findmnt -n -l -o TARGET --source "$dev" 2>/dev/null | sort -r)
    for m in $mnts; do
        x umount "$m" || die "umount $m failed (still in use?): nothing was changed"
    done
    printf 'CHANGED\n'
    # an inactive origin: a thin merge and a copy-on-write merge both start at its activation
    x lvchange -an "$TARGET" || die "lvchange -an $TARGET failed"
    x lvconvert --merge "${TARGET%%/*}/$NAME" || die "lvconvert --merge of ${TARGET%%/*}/$NAME failed"
    x lvchange -ay "$TARGET" || die "lvchange -ay $TARGET failed"
    for m in $(echo "$mnts" | sort); do
        x mount "$m" 2>/dev/null || x mount "$dev" "$m" || die "mount $m failed"
    done
}

##########
# Main   #
##########

case "$TYPE" in
zfs | lvm) ;;
*) die "unknown snapshot type '$TYPE': zfs or lvm" ;;
esac

case "$ACTION" in
take | delete | snapback) valid_name "$NAME" ;;
list) ;;
*) die "unknown action '$ACTION': take, list, delete or snapback" ;;
esac

"${TYPE}_resolve"
"${TYPE}_${ACTION}"
