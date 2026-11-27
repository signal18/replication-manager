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
#   argument 1                 the action: take, list, delete, snapback; or, for an external
#                              plugin, a host action: devices, unmount, mount (#2456)
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
#   DEVICE <path> <fstype> <source> <id>   devices: what holds a server path, id its WWN (block)
#                              or its NFS export; UNRESOLVED <path> when it can not be told
#   MOUNT <target> <source>    unmount: a mount point it unmounted, for mount to put back
# A host action reads REPMAN_SNAPSHOT_MOUNTS (mount: the MOUNT records of unmount, one
# "target|source" per line).
# An error is one line on standard error and exit status 1. Nothing here starts or stops the
# database: replication-manager stops it before a snapback and starts it after.

set -u
# no globbing: the words split from findmnt and the server paths are never patterns
set -f
# messages in English: the fuser and sudo errors are read from their text (sudo keeps LC_*)
LC_ALL=C
export LC_ALL

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

# x CMD...: print the command, then run it. Its stdin is /dev/null: under sh -s the script
# itself is on stdin, a command reading it (a prompt) would eat the rest of the script.
x() {
    cmdline "$@" >&3
    $SUDO "$@" </dev/null
}

# q CMD...: run a read-only command without printing it, stdin closed as for x
q() {
    $SUDO "$@" </dev/null
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
    case "$1" in
    *[!.]*) ;;
    *) die "invalid snapshot name '$1': only dots" ;;
    esac
}

# mount_source PATH: the device or dataset mounted at the mount point holding PATH, and its type
mount_source() {
    command -v findmnt >/dev/null 2>&1 || die "findmnt not found on the database host"
    [ -e "$1" ] || die "path $1 does not exist on the database host"
    findmnt -n -o SOURCE,FSTYPE -T "$1" </dev/null | head -n 1
}

# wait_unused MOUNTPOINT...: until no process uses them (a database still flushing after its
# port closed holds its files), at most REPMAN_SNAPSHOT_BUSY_WAIT seconds; then die: nothing
# was changed yet. fuser (psmisc) must run, through sudo for a non-root user: without it the
# snapback is refused, the check is what makes its destructive part safe.
wait_unused() {
    [ $# -gt 0 ] || return 0
    command -v fuser >/dev/null 2>&1 || die "fuser not found on the database host (package psmisc): the volume can not be checked for use, nothing was changed"
    q fuser -V >/dev/null 2>&1 || die "fuser does not run (${SUDO:-as} $(id -un)): add it to the sudoers entries, nothing was changed"
    waited=0
    while :; do
        busy=""
        for m in "$@"; do
            if [ ! -e "$m" ]; then
                echo "$m does not exist on the database host: not checked for use"
                continue
            fi
            # 0: a process uses it; 1: none, or an error: told apart by what it printed
            out=$(q fuser -m "$m" 2>&1)
            rc=$?
            case "$out" in
            *"sudo:"* | *"ermission denied"* | *"not permitted"* | *"not allowed"*) die "fuser -m $m does not run ($out): nothing was changed" ;;
            esac
            if [ "$rc" -eq 0 ]; then
                busy="$m"
                break
            fi
        done
        [ -z "$busy" ] && return 0
        [ "$waited" -ge "${REPMAN_SNAPSHOT_BUSY_WAIT:-120}" ] && die "$busy is still in use after ${waited}s (the database still running?): nothing was changed"
        sleep 2
        waited=$((waited + 2))
    done
}

# no_blank_mounts LINES: a mount point holding a blank is refused, the lists here are words
no_blank_mounts() {
    printf '%s\n' "$1" | grep -q '[[:blank:]]' && die "a mount point of the volume holds a blank ($(printf '%s' "$1" | tr '\n' ',')): not supported, nothing was changed"
    return 0
}

# server_paths: the server's directories (REPMAN_SNAPSHOT_PATHS), one per word
server_paths() {
    printf '%s\n' "$PATHS" | tr ':' ' '
}

##########
# ZFS    #
##########

zfs_resolve() {
    command -v zfs >/dev/null 2>&1 || die "zfs not found on the database host"
    # the server's paths are always resolved: a named target is checked against them as a
    # found one is (it must hold every one of them, and nothing else)
    [ -n "$PATHS" ] || die "the server's directories are not known (its variables not read yet): nothing to check the dataset against"
    common=""
    pds=""
    oldifs=$IFS
    IFS=:
    for p in $PATHS; do
        IFS=$oldifs
        [ -n "$p" ] || continue
        src=$(mount_source "$p") || exit 1
        set -- $src
        [ "${2:-}" = "zfs" ] || die "path $p is on ${2:-an unknown filesystem} (${1:-?}), not on ZFS: set backup-snapshot-target or backup-snapshot-plugin"
        ds="${1%%@*}"
        pds="$pds $ds"
        if [ -z "$common" ]; then
            common="$ds"
        else
            # the deepest dataset holding both
            while [ "$ds" != "$common" ] && [ "${ds#"$common"/}" = "$ds" ]; do
                case "$common" in
                */*) common="${common%/*}" ;;
                *) die "the server's files are on several pools ($common, $ds): one snapshot can not hold them" ;;
                esac
            done
        fi
        IFS=:
    done
    IFS=$oldifs
    [ -n "$common" ] || die "no dataset found for $PATHS"
    root="$common"
    how="found"
    if [ -n "$TARGET" ]; then
        x zfs list -H -o name "$TARGET" >/dev/null || die "dataset $TARGET not found"
        for pd in $pds; do
            [ "$pd" = "$TARGET" ] || [ "${pd#"$TARGET"/}" != "$pd" ] || die "backup-snapshot-target $TARGET does not hold $pd, a dataset of the server's files"
        done
        root="$TARGET"
        how="named"
    fi
    # it must hold the server's files and nothing else: its snapback rolls back every dataset
    # under it, so never a pool root, never a tree holding something else
    case "$root" in
    */*) ;;
    *) die "dataset $root ($how) is the root of a pool: never snapshotted. The server's directories must all sit under one dataset below the pool root (name it in backup-snapshot-target if it is not found), or use backup-snapshot-script" ;;
    esac
    tree=$(q zfs list -H -o name -r -t filesystem,volume "$root") || die "can not list the datasets of $root"
    for ds in $tree; do
        zfs_holds_server "$ds" || die "dataset $ds under $root ($how) holds none of the server's files: the snapshot would take it too. The server's directories must sit under a dataset holding only them, or use backup-snapshot-script"
    done
    TARGET="$root"
    printf 'TARGET\t%s\n' "$TARGET"
}

# zfs_holds_server DS: DS is a dataset of the server: one holding a server path, above one
# (between it and the common parent) or under one, or mounted inside a server path
zfs_holds_server() {
    for pd in $pds; do
        [ "$1" = "$pd" ] && return 0
        [ "${pd#"$1"/}" != "$pd" ] && return 0
        [ "${1#"$pd"/}" != "$1" ] && return 0
    done
    mnt=$(findmnt -n -o TARGET --source "$1" </dev/null 2>/dev/null | head -n 1)
    [ -n "$mnt" ] || return 1
    oldifs2=$IFS
    IFS=:
    for p in $PATHS; do
        case "$mnt" in
        "$p" | "$p"/*) IFS=$oldifs2; return 0 ;;
        esac
    done
    IFS=$oldifs2
    return 1
}

zfs_list() {
    out=$(x zfs list -H -p -t snapshot -d 1 -o name,creation,used -s creation "$TARGET") || die "zfs list of the snapshots of $TARGET failed"
    printf '%s\n' "$out" | while IFS="$TAB" read -r n c u; do
        if [ -n "$n" ]; then printf 'SNAPSHOT\t%s\t%s\t%s\n' "${n#*@}" "$c" "$u"; fi
    done
}

zfs_has() {
    q zfs list -H -o name "$1@$NAME" >/dev/null 2>&1
}

zfs_take() {
    x zfs snapshot -r "$TARGET@$NAME" || die "zfs snapshot of $TARGET@$NAME failed"
    out=$(x zfs list -H -p -t snapshot -o name,creation,used "$TARGET@$NAME") || die "snapshot $TARGET@$NAME taken, its read-back failed"
    printf '%s\n' "$out" | while IFS="$TAB" read -r n c u; do
        if [ -n "$n" ]; then printf 'SNAPSHOT\t%s\t%s\t%s\n' "${n#*@}" "$c" "$u"; fi
    done
}

zfs_delete() {
    zfs_has "$TARGET" || die "snapshot $TARGET@$NAME not found"
    x zfs destroy -r "$TARGET@$NAME" || die "zfs destroy of $TARGET@$NAME failed"
}

zfs_snapback() {
    zfs_has "$TARGET" || die "snapshot $TARGET@$NAME not found"
    datasets=$(q zfs list -H -o name -r -t filesystem,volume "$TARGET") || die "can not list the datasets of $TARGET"
    # every dataset of the tree must hold the snapshot, or the rollback would be partial
    for ds in $datasets; do
        zfs_has "$ds" || die "dataset $ds has no snapshot $NAME (created after it): nothing was changed"
    done
    # a clone of a snapshot taken after it: zfs rollback -r refuses, after CHANGED
    for ds in $datasets; do
        clones=$(q zfs list -H -t snapshot -d 1 -s createtxg -o name,clones "$ds" | awk -F '\t' -v s="$ds@$NAME" 'found && $2 != "-" && $2 != "" { print $1 } $1 == s { found = 1 }')
        [ -z "$clones" ] || die "snapshot(s) taken after $NAME have clones ($clones): zfs can not roll back over them, nothing was changed"
    done
    # no process on any of their mount points: a busy dataset would fail its rollback half-way
    mnts=""
    for ds in $datasets; do
        dsm=$(findmnt -n -l -o TARGET --source "$ds" </dev/null 2>/dev/null)
        no_blank_mounts "$dsm"
        mnts="$mnts $(printf '%s\n' "$dsm" | tr '\n' ' ')"
    done
    # and the server's own directories: what the database holds open, whatever findmnt resolves
    # (a zvol under the tree has no mount point: not seen here)
    # shellcheck disable=SC2086
    wait_unused $mnts $(server_paths)
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
    q lvs --noheadings -o vg_name,lv_name "$1" 2>/dev/null | awk 'NR==1 { print $1 "/" $2 }'
}

lvm_resolve() {
    command -v lvs >/dev/null 2>&1 || die "lvs not found on the database host"
    # the server's paths are always resolved: a named volume must hold every one of them
    [ -n "$PATHS" ] || die "the server's directories are not known (its variables not read yet): nothing to check the logical volume against"
    found=""
    oldifs=$IFS
    IFS=:
    for p in $PATHS; do
        IFS=$oldifs
        [ -n "$p" ] || continue
        src=$(mount_source "$p") || exit 1
        set -- $src
        lv=$(lvm_lv_of "${1:-}")
        [ -n "$lv" ] || die "path $p is on ${1:-an unknown device}, not on an LVM logical volume: set backup-snapshot-plugin"
        if [ -z "$found" ]; then
            found="$lv"
        elif [ "$found" != "$lv" ]; then
            die "the server's files are on several logical volumes ($found, $lv): a snapshot of one would not be consistent"
        fi
        IFS=:
    done
    IFS=$oldifs
    [ -n "$found" ] || die "no logical volume found for $PATHS"
    if [ -n "$TARGET" ]; then
        lv=$(lvm_lv_of "$TARGET")
        cmdline lvs "$TARGET"
        [ -n "$lv" ] || die "logical volume $TARGET not found"
        [ "$lv" = "$found" ] || die "backup-snapshot-target $TARGET is not the logical volume of the server's files ($found)"
    fi
    TARGET="$found"
    printf 'TARGET\t%s\n' "$TARGET"
}

# lvm_snapshots: name, origin, time, size of every snapshot of TARGET
lvm_snapshots() {
    vg="${TARGET%%/*}"
    lv="${TARGET#*/}"
    out=$(x lvs --noheadings --separator '|' --units b --nosuffix -o lv_name,origin,lv_time,lv_size "$vg") || die "lvs of $vg failed"
    printf '%s\n' "$out" | while IFS='|' read -r n o t s; do
        n=$(echo "$n" | tr -d ' ')
        o=$(echo "$o" | tr -d ' ')
        [ "$o" = "$lv" ] || continue
        c=$(date -d "$t" +%s 2>/dev/null || echo 0)
        printf '%s\t%s\t%s\n' "$n" "$c" "$(echo "$s" | tr -d ' ')"
    done
}

lvm_list() {
    snaps=$(lvm_snapshots) || exit 1
    printf '%s\n' "$snaps" | sort -t "$TAB" -k 2,2n | while IFS="$TAB" read -r n c s; do
        if [ -n "$n" ]; then printf 'SNAPSHOT\t%s\t%s\t%s\n' "$n" "$c" "$s"; fi
    done
}

lvm_has() {
    # lvs not running (a sudoers entry missing) is said as such, not as a missing snapshot
    q lvs "$TARGET" >/dev/null 2>&1 || die "lvs $TARGET does not run (sudoers?)"
    snaps=$(lvm_snapshots 3>/dev/null) || exit 1
    printf '%s\n' "$snaps" | cut -f 1 | grep -Fxq -- "$NAME"
}

lvm_take() {
    vg="${TARGET%%/*}"
    seg=$(q lvs --noheadings -o segtype "$TARGET" | tr -d ' ')
    if [ "$seg" = "thin" ]; then
        x lvcreate -s -n "$NAME" "$TARGET" || die "lvcreate of thin snapshot $vg/$NAME failed"
    else
        case "$LVM_SIZE" in
        *%*) x lvcreate -s -l "$LVM_SIZE" -n "$NAME" "$TARGET" || die "lvcreate of snapshot $vg/$NAME failed" ;;
        *) x lvcreate -s -L "$LVM_SIZE" -n "$NAME" "$TARGET" || die "lvcreate of snapshot $vg/$NAME failed" ;;
        esac
    fi
    snaps=$(lvm_snapshots 3>/dev/null) || die "snapshot $NAME taken, its read-back failed"
    printf '%s\n' "$snaps" | while IFS="$TAB" read -r n c s; do
        if [ "$n" = "$NAME" ]; then printf 'SNAPSHOT\t%s\t%s\t%s\n' "$n" "$c" "$s"; fi
    done
}

lvm_delete() {
    lvm_has || die "snapshot $NAME of $TARGET not found"
    x lvremove -y "${TARGET%%/*}/$NAME" || die "lvremove of ${TARGET%%/*}/$NAME failed"
}

lvm_snapback() {
    lvm_has || die "snapshot $NAME of $TARGET not found"
    dev=$(q lvs --noheadings -o lv_path "$TARGET" | tr -d ' ')
    [ -n "$dev" ] || die "no device path for $TARGET"
    # the mount points of the volume, deepest first: unmounted before the merge, mounted after
    mnts=$(findmnt -n -l -o TARGET --source "$dev" </dev/null 2>/dev/null | sort -r)
    # an unmounted volume, or a mount findmnt does not tie to the device (an alias): never go on
    # to deactivate a volume that may still be mounted
    [ -n "$mnts" ] || die "no mount point found for $dev (not mounted, or findmnt does not know it): nothing was changed"
    no_blank_mounts "$mnts"
    # shellcheck disable=SC2086
    wait_unused $mnts $(server_paths)
    pairs=""
    for m in $mnts; do
        pairs="$pairs $m|$dev"
    done
    # shellcheck disable=SC2086
    unmount_pairs $pairs
    seg=$(q lvs --noheadings -o segtype "$TARGET" | tr -d ' ')
    printf 'CHANGED\n'
    if [ "$seg" = "thin" ]; then
        # a thin merge into an active origin waits for its next activation
        x lvchange -an "$TARGET" || die "lvchange -an $TARGET failed"
        x lvconvert --merge "${TARGET%%/*}/$NAME" || die "lvconvert --merge of ${TARGET%%/*}/$NAME failed"
        x lvchange -ay "$TARGET" || die "lvchange -ay $TARGET failed"
    else
        # a copy-on-write merge into an origin not open (unmounted) starts at once, no
        # deactivation (some LVM releases refuse it on an origin with an active snapshot)
        x lvconvert --merge "${TARGET%%/*}/$NAME" || die "lvconvert --merge of ${TARGET%%/*}/$NAME failed"
    fi
    for m in $(echo "$mnts" | sort); do
        x mount "$m" 2>/dev/null || x mount "$dev" "$m" || die "mount $m failed"
    done
}

# unmount_pairs TARGET|SOURCE...: unmounts each, in the order given (deepest first). A failure
# puts back what this run unmounted (parents first) and dies with nothing changed; when one can
# not be put back, CHANGED: the volume is half unmounted. Shared by the LVM snapback and the
# unmount host action.
unmount_pairs() {
    done_pairs=""
    for pr in "$@"; do
        m="${pr%%|*}"
        if ! x umount "$m"; then
            # one word per line, sorted on the mount point alone: parents first ('/' sorts
            # before '|', a whole-pair sort would put a child before its parent)
            for dp in $(printf '%s\n' $done_pairs | sort -t '|' -k 1,1); do
                d="${dp%%|*}"
                if ! { x mount "$d" 2>/dev/null || x mount "${dp#*|}" "$d"; }; then
                    printf 'CHANGED\n'
                    die "umount $m failed, and $d could not be mounted again: the volume is half unmounted"
                fi
            done
            die "umount $m failed (still in use?): nothing was changed"
        fi
        done_pairs="$done_pairs $pr"
    done
}

##########
# Host   #
##########

# The host actions of an external plugin (#2456): the plugin runs on the replication-manager
# host and drives a storage system; what only the database host knows is asked here.

# host_devices: for each server path, what holds it: DEVICE records, the WWN of every disk
# under its mount source (a multipath device, a partition, an LVM volume on several LUNs), or
# the NFS export; UNRESOLVED when neither (a ZFS dataset, an overlay, no lsblk).
host_devices() {
    command -v findmnt >/dev/null 2>&1 || die "findmnt not found on the database host"
    oldifs=$IFS
    IFS=:
    for p in $PATHS; do
        IFS=$oldifs
        [ -n "$p" ] || continue
        src=$(mount_source "$p") || exit 1
        set -- $src
        source="${1:-}"
        fstype="${2:-}"
        case "$fstype" in
        nfs | nfs4)
            printf 'DEVICE\t%s\t%s\t%s\t%s\n' "$p" "$fstype" "$source" "$source"
            IFS=:
            continue ;;
        esac
        wwns=""
        case "$source" in
        /dev/*)
            if command -v lsblk >/dev/null 2>&1; then
                wwns=$(lsblk -s -n -r -o WWN,TYPE "$source" </dev/null 2>/dev/null | awk '$2 == "disk" && $1 != "" { print $1 }' | sort -u)
            fi ;;
        esac
        if [ -z "$wwns" ]; then
            printf 'UNRESOLVED\t%s\n' "$p"
        else
            for w in $wwns; do
                printf 'DEVICE\t%s\t%s\t%s\t%s\n' "$p" "$fstype" "$source" "$w"
            done
        fi
        IFS=:
    done
    IFS=$oldifs
}

# system_mount MOUNTPOINT: a mount point of the system, never unmounted for a snapback
system_mount() {
    case "$1" in
    / | /usr | /var | /etc | /boot | /home | /opt | /tmp | /root | /proc | /sys | /dev | /run | /srv | /mnt | /media) return 0 ;;
    esac
    return 1
}

# host_unmount: unmounts the file systems holding the server paths (and those mounted under
# them), deepest first, once no process uses them; MOUNT records for host_mount. A system mount
# point (the server's files not on a file system of their own) is refused: nothing changed.
host_unmount() {
    command -v findmnt >/dev/null 2>&1 || die "findmnt not found on the database host"
    tops=""
    for p in $(server_paths); do
        t=$(findmnt -n -o TARGET -T "$p" </dev/null | head -n 1)
        [ -n "$t" ] || die "no mount point found for $p: nothing was changed"
        system_mount "$t" && die "$p is on $t, a system file system: its storage is not the server's own, nothing is unmounted (set backup-snapshot-unmount off if the plugin handles it): nothing was changed"
        tops="$tops
$t"
    done
    # the mount points and the ones under them, deepest first, with their source
    lines=""
    for t in $(printf '%s\n' "$tops" | sort -u); do
        lines="$lines
$(findmnt -n -l -o TARGET,SOURCE -R "$t" </dev/null)"
    done
    # deepest first: sorted on the mount point alone, reversed
    lines=$(printf '%s\n' "$lines" | awk 'NF' | sort -u -r -k 1,1)
    [ -n "$lines" ] || die "no mount point found for the server's files: nothing was changed"
    targets=$(printf '%s\n' "$lines" | awk '{ print $1 }')
    no_blank_mounts "$targets"
    # shellcheck disable=SC2086
    wait_unused $targets $(server_paths)
    pairs=$(printf '%s\n' "$lines" | awk '{ print $1 "|" $2 }')
    # shellcheck disable=SC2086
    unmount_pairs $pairs
    for pr in $pairs; do
        printf 'MOUNT\t%s\t%s\n' "${pr%%|*}" "${pr#*|}"
    done
}

# host_mount: puts back what host_unmount unmounted (REPMAN_SNAPSHOT_MOUNTS), parents first,
# after the block devices' buffers are dropped: the storage below them was restored.
host_mount() {
    mounts="${REPMAN_SNAPSHOT_MOUNTS:-}"
    [ -n "$mounts" ] || die "nothing to mount (REPMAN_SNAPSHOT_MOUNTS empty)"
    for pr in $(printf '%s\n' "$mounts" | sort -u -t '|' -k 1,1); do
        case "${pr#*|}" in
        /dev/*) x blockdev --flushbufs "${pr#*|}" || echo "blockdev --flushbufs ${pr#*|} failed: mounted anyway" ;;
        esac
    done
    # parents first: sorted on the mount point alone
    for pr in $(printf '%s\n' "$mounts" | sort -u -t '|' -k 1,1); do
        d="${pr%%|*}"
        x mount "$d" 2>/dev/null || x mount "${pr#*|}" "$d" || die "mount $d failed: the server's files are not mounted"
    done
}

##########
# Main   #
##########

# a host action of an external plugin: whatever the type
case "$ACTION" in
devices | unmount | mount)
    "host_${ACTION}"
    exit 0 ;;
esac

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
# an action that did not die succeeded: a loop's last test never becomes the exit status
exit 0
