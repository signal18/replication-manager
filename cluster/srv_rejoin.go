// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func (server *ServerMonitor) RejoinLoop() error {
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "rejoin %s to the loop", server.URL)
	child := server.GetSibling()
	if child == nil {
		return errors.New("Could not found sibling slave")
	}
	child.StopSlave()
	child.SetReplicationGTIDSlavePosFromServer(server)
	child.StartSlave()
	return nil
}

// RejoinMaster a server that just show up without slave status
func (server *ServerMonitor) RejoinMaster() error {
	cluster := server.ClusterGroup
	// Re-entrancy guard so this can be spawned async from EVERY call site (operator,
	// armed, and the auto Failed->up edge) and never block the monitor loop — a
	// logical/physical reseed can run for hours or days. If a rejoin for this server
	// is already running, return immediately rather than starting a duplicate.
	if !server.rejoinInProgress.CompareAndSwap(false, true) {
		return nil
	}
	defer server.rejoinInProgress.Store(false)
	// Check if master exists in topology before rejoining.
	defer func() {
		cluster.rejoinCond.Send <- true
	}()

	if cluster.Conf.ActivePassive {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining %s ignored caused by active-passive mode", server.URL)
		return nil
	}

	if cluster.GetTopology() == config.TopoMultiMasterWsrep {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining leader %s ignored caused by wsrep protocol", server.URL)
		return nil
	}

	if cluster.StateMachine.IsInFailover() {
		return nil
	}
	// if cluster.Conf.LogLevel > 2 {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining standalone server %s", server.URL)
	// }
	// Rejoin must write (change master, start slave, flashback): release any
	// minority read-lock freeze still held on this server. Idempotent.
	server.UnfreezeReadLock()
	// Strange here add comment for why
	cluster.canFlashBack = true

	// A reseed armed by a previous rejoin tick is still in flight: the rejoin is
	// NOT finished — its outcome is recorded by reconcileDeferredRejoinReseeds
	// (from observed health, once IsReseeding clears), not here. Do not re-enter or
	// record; hold the one-shot until completion. reseedFromRejoin covers the tiny
	// window after the reseed clears IsReseeding but before finishRejoin lands.
	if server.HasAnyReseedingState() || server.reseedFromRejoin.Load() {
		return nil
	}

	// ONE-SHOT terminator: this event already ended with a recorded result (in
	// crash history). Do nothing until an explicit re-arm (rearmRejoin) copies it
	// back. Makes the Failed->up edge AND the per-tick topology extra-master call
	// idempotent — this replaces the old age cap / re-fetch loop.
	if cluster.rejoinAlreadyAttempted(server.URL) {
		return nil
	}

	// CRASH SOURCE — the ONLY election-specific step. Local crash first; if none
	// involves this server and arbitration is on, fetch the peer's verdict on
	// demand (a single-repman cluster has no peer and just keeps its local crash).
	var peerFetchErr error
	peerFetchTried := false
	if cluster.Conf.Arbitration && cluster.getCrashFromJoiner(server.URL) == nil && cluster.getCrashFromMaster(server.URL) == nil {
		peerFetchTried = true
		if _, peerFetchErr = cluster.fetchMasterFromPeer(); peerFetchErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Rejoin %s: peer verdict unavailable: %s", server.URL, peerFetchErr)
		}
	}

	// ROLE: is this RETURNING server the elected WINNER (a crash names it as
	// ElectedMasterURL and it is not itself a loser)? Then CROWN it, never slave
	// it. The LOSER (that crash's URL) rejoins through its own RejoinMaster call
	// (the topology extra-master path) — on the minority the colocated old master
	// never gets a Failed->up edge of its own.
	if cluster.getCrashFromJoiner(server.URL) == nil {
		if win := cluster.getCrashFromMaster(server.URL); win != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Returning server %s is the peer-elected master — crowning it (loser %s rejoins separately)", server.URL, win.URL)
			cluster.master = server
			server.SetMaster()
			if server.IsReadOnly() && !server.IsRelay {
				server.SetReadWrite()
			}
			cluster.lastmaster = nil
			cluster.backendStateChangeProxies()
			return nil
		}
	}

	// MASTER ADOPTION: the minority nil'd its master pointer; the crash names the
	// winner, so adopt it here and converge with the master!=nil path below.
	if cluster.master == nil {
		if cr := cluster.getCrashFromJoiner(server.URL); cr != nil && cr.ElectedMasterURL != "" {
			if m := cluster.GetServerFromURL(cr.ElectedMasterURL); m != nil {
				cluster.master = m
				m.SetMaster()
			}
		}
	}

	if cluster.master != nil {
		if server.URL != cluster.master.URL {
			cluster.SetState("WARN0022", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0022"], server.URL, cluster.master.URL), ErrFrom: "REJOIN"})
			server.RejoinScript()
			if cluster.Conf.MultiMasterGrouprep {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Group replication rejoin  %s server to PRIMARY ", server.URL)
				server.StartGroupReplication()

			} else {
				if cluster.Conf.FailoverSemiSyncState {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Set semisync replica and disable semisync leader %s", server.URL)
					logs, err := server.SetSemiSyncReplica()
					cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed Set semisync replica and disable semisync  %s, %s", server.URL, err)
				}
				crash := cluster.getCrashFromJoiner(server.URL)
				if crash == nil {
					// No divergence record for this server. Preserve the existing
					// conservative behaviour (SST for a known old master, reseed if
					// armed), but every exit now ENDS the cycle via finishRejoin so it
					// is one-shot and its outcome is visible in history.
					cluster.SetState("ERR00066", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00066"], server.URL, cluster.master.URL), ErrFrom: "REJOIN"})
					if cluster.oldMaster != nil && cluster.oldMaster.URL == server.URL {
						err := server.RejoinMasterSST()
						cluster.finishRejoin(server.URL, rejoinResultOf(err))
						return nil
					}
					if cluster.Conf.Autoseed {
						err := server.ReseedMasterSST()
						cluster.recordOrDeferRejoin(server, err)
						return nil
					}
					// PEER-UNREACHABLE (real-world transient split): arbitration is on and
					// we TRIED to fetch the verdict but the peer did not answer. We must
					// NOT blindly re-slave on current GTID — this server may hold a
					// divergent tail we simply could not learn about. Fence it, record a
					// RETRYABLE result (rejoinAlreadyAttempted ignores peer-unreachable),
					// and try again next tick when the peer recovers.
					if cluster.Conf.Arbitration && peerFetchTried && peerFetchErr != nil {
						logs, roErr := server.SetReadOnly()
						cluster.LogSQL(logs, roErr, server.URL, "Rejoin", config.LvlErr, "Failed to fence %s read-only while peer verdict unavailable: %s", server.URL, roErr)
						cluster.finishRejoin(server.URL, RejoinResultPeerUnreached)
						cluster.backendStateChangeProxies()
						return nil
					}
					// No crash, no old-master anchor, no autoseed: attach read-only
					// under the elected master on current GTID (no divergence record to
					// recover). Strict mode still protects if anything is out of order.
					server.attachAsReadOnlySlave(cluster.master)
					cluster.finishRejoin(server.URL, RejoinResultNoDivergence)
					cluster.backendStateChangeProxies()
					return nil
				} //crash info is available
				if cluster.Conf.AutorejoinBackupBinlog {
					server.freezeThenCaptureLostEvents(crash)
				}

				// OPERATOR-CHOSEN METHOD (GUI delta viewer): if the re-armed crash
				// carries a method it OVERRIDES the automatic flashback/SST cascade for
				// this one attempt. All methods are runnable on any crash — the delta
				// verdict informs, it does not gate.
				if crash.RejoinMethod != "" {
					server.rejoinWithMethod(crash)
					return nil
				}

				err := server.rejoinMasterIncremental(crash)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Failed to autojoin incremental to master %s", server.URL)
					sstErr := server.RejoinMasterSST()
					if sstErr != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "State transfer rejoin failed")
						// Could not clean the tail. Distinguish WHY for the operator:
						//   diverged + not reversible -> not-flashback-able (manual)
						//   otherwise (incl. empty)   -> no method / generic failure
						// An EMPTY delta is never "not-flashback-able" — nothing diverged.
						result := RejoinResultNoMethod
						if crash.DeltaAnalyzed && crash.Diverged() && !crash.DeltaFlashable {
							result = RejoinResultNotFlashback
						}
						// Persist the captured delta archive (crash-bin dir) BEFORE
						// finishing — the failure path must still leave the binlog delta
						// for the viewer. The old code ran saveBinlog unconditionally; the
						// early return here would otherwise skip it (regression 2026-07-15:
						// 10 trx counted but no delta content in the viewer).
						if cluster.Conf.AutorejoinBackupBinlog {
							server.saveBinlog(crash)
						}
						// ALWAYS end attached read-only under the elected master: strict
						// mode protects a divergent tail as SlaveErr — never a floating
						// writable standalone.
						server.attachAsReadOnlySlave(cluster.master)
						cluster.finishRejoin(server.URL, result)
						cluster.backendStateChangeProxies()
						return nil
					}
				}
				if cluster.Conf.AutorejoinBackupBinlog {
					server.saveBinlog(crash)
				}
				// Success: no-divergence if the delta was empty (clean re-slave),
				// otherwise a real recovery of a diverged tail.
				result := RejoinResultSuccess
				if crash.DeltaAnalyzed && !crash.Diverged() {
					result = RejoinResultNoDivergence
				}
				cluster.finishRejoin(server.URL, result)

			}

			// if consul or internal proxy need to adapt read only route to new slaves
			cluster.backendStateChangeProxies()
		}
	} else {
		//no master discovered rediscovering from last seen
		if cluster.lastmaster != nil {
			if cluster.lastmaster.ServerID == server.ServerID {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rediscovering same master from last seen master: %s", server.URL)
				cluster.master = server
				server.SetMaster()
				server.SetReadWrite()
				cluster.lastmaster = nil
			} else {
				if !cluster.Conf.FailRestartUnsafe {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rediscovering not the master from last seen master: %s", server.URL)
					server.rejoinMasterAsSlave()
					// if consul or internal proxy need to adapt read only route to new slaves
					cluster.backendStateChangeProxies()
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rediscovering unsafe possibly electing old leader after cascading failure to flavor availability: %s", server.URL)
					cluster.master = server
				}
			}

		} // we have last seen master

	}
	return nil
}

func (server *ServerMonitor) RejoinPreviousSnapshot() error {
	_, err := server.JobZFSSnapBack()
	return err
}

func (server *ServerMonitor) RejoinMasterSST() error {
	cluster := server.ClusterGroup
	if cluster.Conf.AutorejoinMysqldump {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoin flashback dump restore %s", server.URL)
		err := server.RejoinDirectDump()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "mysqldump flashback restore failed %s", err)
			return errors.New("Dump from master failed")
		}
	} else if cluster.Conf.AutorejoinLogicalBackup {
		err := server.JobFlashbackLogicalBackup(false) // automatic path -- never recorded as validated
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "logical backup flashback restore failed %s", err)
			return errors.New("Restore from logical backup failed")
		}
	} else if cluster.Conf.AutorejoinPhysicalBackup {
		err := server.JobFlashbackPhysicalBackup()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "physical backup flashback restore failed %s", err)
			return errors.New("Restore from physical backup failed")
		}
	} else if cluster.Conf.AutorejoinZFSFlashback {
		server.RejoinPreviousSnapshot()
	} else if cluster.Conf.BackupLoadScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling restore script")
		var out []byte
		out, err := exec.Command(cluster.Conf.BackupLoadScript, misc.Unbracket(server.Host), misc.Unbracket(cluster.master.Host), server.Port, server.GetCluster().GetMaster().Port).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Restore script complete %s", string(out))
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "No SST rejoin method found")
		return errors.New("No SST rejoin flashback method found")
	}

	return nil
}

func (server *ServerMonitor) RejoinScript() error {
	cluster := server.ClusterGroup
	// Call the operator's custom rejoin script.
	if server.GetCluster().Conf.RejoinScript == "" {
		return errors.New("no autorejoin-script configured")
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling rejoin script")
	out, err := exec.Command(cluster.Conf.RejoinScript, server.Host, server.GetCluster().GetMaster().Host, server.Port, server.GetCluster().GetMaster().Port).CombinedOutput()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin script complete:", string(out))
	return err
}

func (server *ServerMonitor) ReseedMasterSST() error {
	cluster := server.ClusterGroup
	server.DelWaitBackupCookie()
	if cluster.Conf.AutorejoinMysqldump {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoin dump restore %s", server.URL)
		err := server.RejoinDirectDump()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "mysqldump restore failed %s", err)
			return errors.New("Dump from master failed")
		}
	} else {
		if cluster.Conf.BackupLoadScript != "" {
			if err := server.JobReseedBackupScript(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Reseed backup script for rejoin on %s failed: %s", server.URL, err)
			}
		} else if cluster.Conf.AutorejoinLogicalBackup {
			err := server.JobReseedLogicalBackup(context.Background(), "default")
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Reseed logical for rejoin on %s failed: %s", server.URL, err)
			}
		} else if cluster.Conf.AutorejoinPhysicalBackup {
			server.JobReseedPhysicalBackup("default")
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "No SST reseed method found")
			return errors.New("No SST reseed method found")
		}
	}

	return nil
}

func (server *ServerMonitor) rejoinMasterSync(crash *Crash) error {
	cluster := server.ClusterGroup
	if server.HasGTIDReplication() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Found same or lower GTID %s and new elected master was %s", server.CurrentGtid.Sprint(), crash.FailoverIOGtid.Sprint())
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Found same or lower sequence %s , %s", server.BinaryLogFile, server.BinaryLogPos)
	}
	var err error
	realmaster := cluster.GetMaster()
	if cluster.Conf.MxsBinlogOn || cluster.Conf.MultiTierSlave {
		realmaster = cluster.GetRelayServer()
		if realmaster == nil {
			return fmt.Errorf("No relay for current cluster and Maxscale Binlog Server or multi tier slave is active")
		}
	}

	if realmaster == nil {
		return fmt.Errorf("No master found for rejoin GTID position")
	}

	if server.HasGTIDReplication() || (realmaster.MxsHaveGtid && realmaster.IsMaxscale) {
		logs, err := server.SetReplicationGTIDCurrentPosFromServer(realmaster)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed in GTID rejoin old master in sync %s, %s", server.URL, err)
		if err != nil {
			return err
		}
	} else if cluster.Conf.MxsBinlogOn {
		opt := cluster.GetChangeMasterBaseOptForMxs(server, realmaster)
		opt.Logfile = crash.FailoverMasterLogFile
		opt.Logpos = crash.FailoverMasterLogPos
		logs, err := dbhelper.ChangeMaster(server.Conn, opt, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Change master positional failed in Rejoin old Master in sync to maxscale %s", err)
		if err != nil {
			return err
		}
	} else {
		// not maxscale the new master coordonate are in crash
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Change master to positional in Rejoin old Master")
		opt := cluster.GetChangeMasterBaseOptForSlave(server, realmaster, server.IsDelayed)
		opt.Mode = "POSITIONAL"
		opt.Logfile = crash.NewMasterLogFile
		opt.Logpos = crash.NewMasterLogPos
		logs, err := dbhelper.ChangeMaster(server.Conn, opt, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Change master positional failed in Rejoin old Master in sync %s", err)
		if err != nil {
			return err
		}
	}

	server.StartSlave()
	return err
}

func (server *ServerMonitor) rejoinMasterFlashBack(crash *Crash) error {
	cluster := server.ClusterGroup
	realmaster := cluster.master
	if cluster.Conf.MxsBinlogOn || cluster.Conf.MultiTierSlave {
		realmaster = cluster.GetRelayServer()
	}

	if _, err := os.Stat(cluster.GetMysqlBinlogPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlBinlogPath())
		return err
	}
	if _, err := os.Stat(cluster.GetMysqlclientPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlclientPath())
		return err
	}

	binlogArgs := make([]string, 0)
	binlogArgs = append(binlogArgs, "--flashback", "--to-last-log", cluster.Conf.WorkingDir+"/"+cluster.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile)
	binlogCmd := exec.Command(cluster.GetMysqlBinlogPath(), misc.RemoveEmptyString(binlogArgs)...)

	cliParams := make([]string, 0)
	cliParams = append(cliParams, "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--user="+cluster.GetDbUser(), "--password="+cluster.GetDbPass())
	cliParams = append(cliParams, server.GetSSLClientParam("client")...)
	clientCmd := exec.Command(cluster.GetMysqlclientPath(), misc.RemoveEmptyString(cliParams)...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "FlashBack: %s %s", cluster.GetMysqlBinlogPath(), strings.ReplaceAll(strings.Join(binlogCmd.Args, " "), cluster.GetRplPass(), "XXXX"))

	var err error
	clientCmd.Stdin, err = binlogCmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Error opening pipe: %s", err)
		return err
	}
	if err := binlogCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Failed mysqlbinlog command: %s at %s", err, strings.ReplaceAll(binlogCmd.Path, cluster.GetRplPass(), "XXXX"))
		return err
	}
	if err := clientCmd.Run(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Error starting client: %s at %s", err, strings.ReplaceAll(clientCmd.Path, cluster.GetRplPass(), "XXXX"))
		return err
	}
	logs, err := dbhelper.SetGTIDSlavePos(server.Conn, crash.FailoverIOGtid.Sprint())
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlInfo, "SET GLOBAL gtid_slave_pos = \"%s\"", crash.FailoverIOGtid.Sprint())
	if err != nil {
		return err
	}
	var err2 error
	if server.MxsHaveGtid || !server.IsMaxscale {
		logs, err2 = server.SetReplicationGTIDSlavePosFromServer(realmaster)
	} else {
		logs, err2 = server.SetReplicationFromMaxscaleServer(realmaster)
	}
	cluster.LogSQL(logs, err2, server.URL, "Rejoin", config.LvlInfo, "Failed SetReplicationGTIDSlavePosFromServer on %s: %s", server.URL, err2)
	if err2 != nil {
		return err2
	}
	logs, err = server.StartSlave()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlInfo, "Failed stop slave on %s: %s", server.URL, err)

	return nil
}

// resolveLiveDumpSource picks which live server to mysqldump from for a
// direct-dump rejoin: the operator-designated backup replica when one
// exists, else the master. Routed through RestoreSelector + ResolveRestore
// over liveDumpCatalog (issue #1589's ask to unify RejoinDirectDump onto the
// same selector/catalog mechanism as the other reseed paths), using the
// PresetReseedSpareMaster preset RESTORE_SELECTOR.md documents for exactly
// this case: {Origin:"master", Repo:"live", Safety:["preservenetwork"]}.
//
// Origin is reset to "any" here: OriginMaster's only meaning
// (originMatch: e.Server == ctx.MasterURL) would rank the literal master
// entry ABOVE the backup-replica entry — the opposite of what "spare the
// master" needs, and it's checked before Safety in prefCmp. Origin has no
// concept of "the designated backup replica" (same gap the old inline
// comment here used to flag), so for this live-only, two-candidate catalog
// it's left neutral and Safety does the actual work: preservemasterload is
// added alongside the preset's own preservenetwork so safetyScore's
// isLiveFromMaster check (restore_selector.go) can tell the two live
// candidates apart and rank the non-master one first — reproducing the
// prior hardcoded "prefer the backup replica, else master" behavior through
// the selector instead of as a bespoke pick.
//
// Known limitation, deliberately NOT fixed here: unlike
// rejoinMasterSync/rejoinMasterFlashBack, this does not consult
// GetRelayServer() for Maxscale-binlog/multi-tier topologies.
// RejoinDirectDump computes a relay-aware realmaster separately for its
// pre-dump CHANGE MASTER step, but that value is dropped before this pick —
// a pre-existing asymmetry, preserved exactly, not introduced or corrected
// by this migration.
func (cluster *Cluster) resolveLiveDumpSource() *ServerMonitor {
	sel := PresetReseedSpareMaster()
	sel.Origin = OriginAny
	sel.Safety = append(sel.Safety, "preservemasterload")

	var ctx ResolveContext
	if cluster.master != nil {
		ctx.MasterURL = cluster.master.URL
	}

	if pick := ResolveRestore(cluster.liveDumpCatalog(), sel, ctx); pick != nil {
		if s := cluster.GetServerFromURL(pick.Server); s != nil {
			return s
		}
	}
	return cluster.master
}

func (server *ServerMonitor) RejoinDirectDump() error {
	cluster := server.ClusterGroup
	var err3 error
	var logs string

	if server.HasAnyReseedingState() {
		return fmt.Errorf("Server is in reseeding state by %s", server.IsReseeding)
	}

	tool := "direct"

	server.SetInReseedBackup(tool)

	if _, err := os.Stat(cluster.GetMysqlDumpPath()); os.IsNotExist(err) {
		if server.HasReseedingState(tool) {
			server.SetInReseedBackup("")
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlDumpPath())
		return err
	}

	if _, err := os.Stat(cluster.GetMysqlclientPath()); os.IsNotExist(err) {
		if server.HasReseedingState(tool) {
			server.SetInReseedBackup("")
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlclientPath())
		return err
	}

	realmaster := cluster.master
	if cluster.Conf.MxsBinlogOn || cluster.Conf.MultiTierSlave {
		realmaster = cluster.GetRelayServer()
	}

	if realmaster == nil {
		if server.HasReseedingState(tool) {
			server.SetInReseedBackup("")
		}
		return errors.New("No master defined exiting rejoin direct dump ")
	}
	// done change master just to set the host and port before dump
	if server.MxsHaveGtid || !server.IsMaxscale {
		logs, err3 = server.SetReplicationGTIDSlavePosFromServer(realmaster)
		cluster.LogSQL(logs, err3, server.URL, "Rejoin", config.LvlInfo, "Failed SetReplicationGTIDSlavePosFromServer on %s: %s", server.URL, err3)

	} else {
		opt := cluster.GetChangeMasterBaseOptForMxs(server, realmaster)
		opt.Logfile = realmaster.FailoverMasterLogFile
		opt.Logpos = realmaster.FailoverMasterLogPos

		logs, err3 = dbhelper.ChangeMaster(server.Conn, opt, server.DBVersion)
		cluster.LogSQL(logs, err3, server.URL, "Rejoin", config.LvlErr, "Failed change master maxscale on %s: %s", server.URL, err3)
	}
	if err3 != nil {
		if server.HasReseedingState(tool) {
			server.SetInReseedBackup("")
		}
		return err3
	}
	// dump here
	go cluster.JobRejoinMysqldumpFromSource(cluster.resolveLiveDumpSource(), server)
	return nil
}

func (server *ServerMonitor) rejoinMasterIncremental(crash *Crash) error {
	cluster := server.ClusterGroup
	if server.GetCluster().GetConf().AutorejoinForceRestore {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cancel incremental rejoin server %s caused by force backup restore  ", server.URL)
		return errors.New("autorejoin-force-restore is on can't just rejoin from current pos")
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoin master incremental %s", server.URL)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Crash info %s", crash)
	server.Refresh()
	if cluster.Conf.ReadOnly && !server.IsIgnoredReadonly() {
		logs, err := server.SetReadOnly()
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to set read only on server %s, %s ", server.URL, err)
	}

	if crash.FailoverIOGtid != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoined GTID sequence  %d from server id %d", server.CurrentGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()), server.GetUniversalGtidServerID())
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Crash Saved GTID sequence %d from server id %d", crash.FailoverIOGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()), server.GetUniversalGtidServerID())
	}
	if !server.isReplicationAheadOfMasterElection(crash) || cluster.Conf.MxsBinlogOn {
		server.rejoinMasterSync(crash)
		return nil
	} else {
		// don't try flashback on old style replication that are ahead jump to SST
		if !server.HasGTIDReplication() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Incremental canceled caused by old style replication")
			return errors.New("Incremental canceled caused by old style replication")
		}
	}
	if crash.FailoverIOGtid != nil {
		// cluster.master.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)) == 0
		// lookup in crash recorded is the current master
		if crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)) == 0 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cascading failover, consider we cannot flashback")
			cluster.canFlashBack = false
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Found server ID in rejoining ID %s and crash FailoverIOGtid %s Master %s", server.ServerID, crash.FailoverIOGtid.Sprint(), cluster.master.URL)
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Old server GTID for flashback not found")
	}
	if crash.FailoverIOGtid != nil && cluster.canFlashBack && cluster.Conf.AutorejoinFlashback && cluster.Conf.AutorejoinBackupBinlog {
		err := server.rejoinMasterFlashBack(crash)
		if err == nil {
			return nil
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Flashback rejoin failed: %s", err)
		return errors.New("Flashback failed")
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "No flashback rejoin can flashback %t, autorejoin-flashback %t autorejoin-backup-binlog %t", cluster.canFlashBack, cluster.Conf.AutorejoinFlashback, cluster.Conf.AutorejoinBackupBinlog)
		return errors.New("Flashback disabled")
	}

}

// rejoinResultOf maps an SST/reseed error to a rejoin result code for finishRejoin.
func rejoinResultOf(err error) string {
	if err != nil {
		return RejoinResultNoMethod
	}
	return RejoinResultSuccess
}

// recordOrDeferRejoin records the rejoin outcome NOW, or defers it when an async
// reseed is still in flight.
//
// EVERY rejoin reseed method (RejoinDirectDump → JobRejoinMysqldumpFromSource,
// ProcessReseedPhysical → WaitAndSendSST/SSTRunSender, ProcessReseedLogical) arms a
// DETACHED goroutine and returns nil before the restore finishes — so err here is
// NOT the outcome. If a reseed is in flight (HasAnyReseedingState), we set
// reseedFromRejoin and defer: reconcileDeferredRejoinReseeds records finishRejoin
// from OBSERVED health once the reseed completes (IsReseeding clears). Only when the
// arm itself failed (err != nil, no reseed in flight) do we record here immediately.
// Never a retry — one-shot either way; RejoinMaster holds the one-shot while
// reseedFromRejoin is set.
func (cluster *Cluster) recordOrDeferRejoin(server *ServerMonitor, err error) {
	if err == nil && server.HasAnyReseedingState() {
		server.reseedFromRejoin.Store(true)
		server.rejoinReseedStart.Store(time.Now().UnixNano())
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Rejoin of %s: async reseed in flight — outcome recorded at reseed completion", server.URL)
		return
	}
	cluster.finishRejoin(server.URL, rejoinResultOf(err))
}

// rejoinWithMethod runs the OPERATOR-CHOSEN recovery method for a re-armed crash,
// persists the delta archive, ends attached read-only, and records the outcome via
// finishRejoin (one-shot as always). Every method is runnable on any crash.
func (server *ServerMonitor) rejoinWithMethod(crash *Crash) {
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Operator rejoin of %s via method %q", server.URL, crash.RejoinMethod)
	var err error
	switch crash.RejoinMethod {
	case RejoinMethodFlashback:
		err = server.rejoinMasterFlashBack(crash)
	case RejoinMethodLogicalDump:
		err = server.RejoinDirectDump()
	case RejoinMethodLogicalBkp:
		err = server.JobFlashbackLogicalBackup(true) // manual, operator-chosen -- eligible for validated-selector recording
	case RejoinMethodPhysicalBkp:
		err = server.JobFlashbackPhysicalBackup()
	case RejoinMethodIgnoreForce:
		// Discard the divergent tail (operator accepts the data loss): RESET MASTER wipes
		// this server's own binlog/GTID history, then re-slave with the SAME full-config
		// CHANGE MASTER the auto path / flashback use (SetReplicationGTIDSlavePosFromServer
		// carries SSL, delay, channel) — RESET MASTER alone does not re-attach.
		logs, e := server.ResetMaster()
		cluster.LogSQL(logs, e, server.URL, "Rejoin", config.LvlErr, "ignore-delta-force: RESET MASTER on %s failed: %s", server.URL, e)
		if e == nil {
			logs, e = server.SetReplicationGTIDSlavePosFromServer(cluster.master)
			cluster.LogSQL(logs, e, server.URL, "Rejoin", config.LvlErr, "ignore-delta-force: CHANGE MASTER on %s failed: %s", server.URL, e)
		}
		if e == nil {
			logs, e = server.StartSlave()
			cluster.LogSQL(logs, e, server.URL, "Rejoin", config.LvlInfo, "ignore-delta-force: START SLAVE on %s: %s", server.URL, e)
		}
		err = e
	case RejoinMethodResetReslave:
		// EXACTLY the server-menu repair, combined: reset-master (node.ResetMaster) then
		// start-slave (node.StartSlave). StartSlave RESUMES the replication already
		// configured on this server, so multi-source / named channels / MASTER_DELAY are
		// preserved. Do NOT re-CHANGE MASTER via attachAsReadOnlySlave below — that would
		// flatten a complex topology to one default channel. End here like the menu.
		logs, rmErr := server.ResetMaster()
		cluster.LogSQL(logs, rmErr, server.URL, "Rejoin", config.LvlErr, "reset-master-reslave: RESET MASTER on %s failed: %s", server.URL, rmErr)
		logs, ssErr := server.StartSlave()
		cluster.LogSQL(logs, ssErr, server.URL, "Rejoin", config.LvlErr, "reset-master-reslave: START SLAVE on %s failed: %s", server.URL, ssErr)
		err = rmErr
		if err == nil {
			err = ssErr
		}
		cluster.finishRejoin(server.URL, rejoinResultOf(err))
		cluster.backendStateChangeProxies()
		return
	case RejoinMethodBootstrapFTWRL:
		// Re-bootstrap the WHOLE master-slave topology (FTWRL on the master before
		// RESET MASTER). Unlike the single-server methods, BootstrapReplication rebuilds
		// the entire topology and re-slaves THIS server itself — so it must NOT be
		// followed by the single-server attachAsReadOnlySlave below (that second CHANGE
		// MASTER fights the setup the bootstrap just built, which is why the menu path
		// worked and this one did not). End here, like the menu action.
		err = cluster.BootstrapReplication(true, true)
		cluster.finishRejoin(server.URL, rejoinResultOf(err))
		cluster.backendStateChangeProxies()
		return
	case RejoinMethodScript:
		// Run the operator's custom autorejoin-script; behaviour is up to the script.
		err = server.RejoinScript()
	default:
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Unknown rejoin method %q for %s", crash.RejoinMethod, server.URL)
		err = errors.New("unknown rejoin method")
	}
	if cluster.Conf.AutorejoinBackupBinlog {
		server.saveBinlog(crash)
	}
	// Mirror the auto-rejoin path exactly: on SUCCESS the method has already re-slaved
	// this server itself (its own full-config CHANGE MASTER + START SLAVE), so add NO
	// second attach — that was the duplicate. Only on FAILURE fall back to
	// attachAsReadOnlySlave so a failed rejoin never leaves a floating writable
	// standalone (strict mode then protects a divergent tail as SlaveErr).
	result := RejoinResultSuccess
	if crash.DeltaAnalyzed && !crash.Diverged() {
		result = RejoinResultNoDivergence
	}
	if err != nil {
		result = RejoinResultNoMethod
		if crash.DeltaAnalyzed && crash.Diverged() && !crash.DeltaFlashable {
			result = RejoinResultNotFlashback
		}
		server.attachAsReadOnlySlave(cluster.master)
	}
	cluster.finishRejoin(server.URL, result)
	cluster.backendStateChangeProxies()
}

// attachAsReadOnlySlave ends a rejoin by CHANGE MASTER to the elected master and
// starting replication, read-only — ALWAYS, even for a diverged / not-flashback-able
// tail. A rejoin must never leave a writable, unattached standalone. GTID strict
// mode is the protection (Stephane): a divergent old master goes SlaveErr (the
// out-of-order sequence is refused, no corruption) instead of drifting as a second
// writable master; the operator then resolves the SlaveErr via the manual-repair
// state (WARN0186) or an explicit re-arm with a chosen method.
func (server *ServerMonitor) attachAsReadOnlySlave(master *ServerMonitor) {
	cluster := server.ClusterGroup
	if master == nil || master.URL == server.URL {
		return
	}
	logs, err := server.SetReadOnly()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to fence %s read-only before attach: %s", server.URL, err)
	logs, err = server.SetReplicationGTIDCurrentPosFromServer(master)
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed CHANGE MASTER of %s to %s: %s", server.URL, master.URL, err)
	logs, err = server.StartSlave()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlInfo, "Started replication of %s under %s (strict mode protects a divergent tail as SlaveErr): %s", server.URL, master.URL, err)
}

func (server *ServerMonitor) rejoinMasterAsSlave() error {
	cluster := server.ClusterGroup
	realmaster := cluster.lastmaster
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining old master server %s to saved master %s", server.URL, realmaster.URL)
	logs, err := server.SetReadOnly()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to set read only on server %s, %s ", server.URL, err)
	if err == nil {
		logs, err = server.SetReplicationGTIDCurrentPosFromServer(realmaster)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to autojoin indirect master server %s, stopping slave as a precaution %s ", server.URL, err)
		if err == nil {
			logs, err = server.StartSlave()
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to stop slave on erver %s, %s ", server.URL, err)
		} else {

			return err
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Rejoin master as slave can't set read only %s", err)
		return err
	}
	return nil
}

func (server *ServerMonitor) rejoinSlaveChangePassword(ss *dbhelper.SlaveStatus) error {
	cluster := server.ClusterGroup
	logs, err := dbhelper.ChangeReplicationPassword(server.Conn, dbhelper.ChangeMasterOpt{
		User:     cluster.GetRplUser(),
		Password: cluster.GetRplPass(),
		Channel:  cluster.Conf.MasterConn,
	}, server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Change master for password rotation : %s", err)
	if err != nil {
		return err
	}

	return nil
}

func (server *ServerMonitor) rejoinSlave(ss dbhelper.SlaveStatus) error {
	// Test if slave not connected to current master
	cluster := server.ClusterGroup
	defer func() {
		cluster.rejoinCond.Send <- true
	}()

	if cluster.GetTopology() == config.TopoMultiMasterRing || cluster.GetTopology() == config.TopoMultiMasterWsrep {
		if cluster.GetTopology() == config.TopoMultiMasterRing {
			server.RejoinLoop()
		}
		if cluster.GetTopology() == config.TopoMultiMasterWsrep {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining replica %s ignored caused by wsrep protocol", server.URL)
		}
		return nil

	}
	mycurrentmaster, _ := cluster.GetMasterFromReplication(server)
	if mycurrentmaster == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master found from replication")
		return errors.New("No master found from replication")
	}
	if cluster.master != nil && cluster.master.Id != server.Id {
		if cluster.master.URL == mycurrentmaster.URL {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cancel rejoin, found same leader already from replication %s	", mycurrentmaster.URL)
			return errors.New("Same master found from replication")
		}
		//Found slave to rejoin
		cluster.SetState("ERR00067", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00067"], server.URL, server.PrevState, ss.SlaveIORunning.String, cluster.master.URL), ErrFrom: "REJOIN"})
		if cluster.master.IsDown() && !cluster.Conf.FailRestartUnsafe {
			server.HaveNoMasterOnStart = true
		}
		if !mycurrentmaster.IsMaxscale && !cluster.Conf.MultiTierSlave && cluster.Conf.ReplicationNoRelay {

			if server.HasGTIDReplication() {
				crash := cluster.getCrashFromMaster(cluster.master.URL)
				if crash == nil {
					cluster.SetState("ERR00065", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00065"], server.URL, cluster.master.URL), ErrFrom: "REJOIN"})
					return errors.New("No Crash info on current master")
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Crash info on current master %s", crash)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Found slave to rejoin %s slave was previously in state %s replication io thread  %s, pointing currently to %s", server.URL, server.PrevState, ss.SlaveIORunning, cluster.master.URL)

				realmaster := cluster.master
				// A SLAVE IS ALWAY BEHIND MASTER
				//		slave_gtid := server.CurrentGtid.GetSeqServerIdNos(uint64(server.GetReplicationServerID()))
				//		master_gtid := crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.GetReplicationServerID()))
				//	if slave_gtid < master_gtid {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining slave %s via GTID", server.URL)
				logs, err := server.StopSlave()
				cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to stop slave server %s, stopping slave as a precaution %s", server.URL, err)
				if err == nil {
					logs, err := server.SetReplicationGTIDSlavePosFromServer(realmaster)
					cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to autojoin indirect slave server %s, stopping slave as a precaution %s", server.URL, err)
					if err == nil {
						logs, err := server.StartSlave()
						cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to start  slave server %s, stopping slave as a precaution %s", server.URL, err)
					}
				}
			} else {
				if mycurrentmaster.State != stateFailed && mycurrentmaster.IsRelay {
					// No GTID compatible solution stop relay master wait apply relay and move to real master
					logs, err := mycurrentmaster.StopSlave()
					cluster.LogSQL(logs, err, mycurrentmaster.URL, "Rejoin", config.LvlErr, "Failed to stop slave on relay server  %s: %s", mycurrentmaster.URL, err)
					if err == nil {
						logs, err2 := dbhelper.MasterPosWait(server.Conn, server.DBVersion, mycurrentmaster.BinaryLogFile, mycurrentmaster.BinaryLogPos, 3600, cluster.Conf.MasterConn)
						cluster.LogSQL(logs, err2, server.URL, "Rejoin", config.LvlErr, "Failed positional rejoin wait pos %s %s", server.URL, err2)
						if err2 == nil {
							myparentss, _ := mycurrentmaster.GetSlaveStatus(mycurrentmaster.ReplicationSourceName)

							logs, err := server.StopSlave()
							cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to stop slave on server %s: %s", server.URL, err)
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Doing Positional switch of slave %s", server.URL)
							opt := cluster.GetChangeMasterBaseOptForSlave(server, cluster.master, server.IsDelayed)
							opt.Mode = "POSITIONAL"
							opt.Logfile = myparentss.MasterLogFile.String
							opt.Logpos = myparentss.ReadMasterLogPos.String
							logs, changeMasterErr := dbhelper.ChangeMaster(server.Conn, opt, server.DBVersion)

							cluster.LogSQL(logs, changeMasterErr, server.URL, "Rejoin", config.LvlErr, "Rejoin Failed doing Positional switch of slave %s: %s", server.URL, changeMasterErr)

						}
						logs, err = server.StartSlave()
						cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to start slave on %s: %s", server.URL, err)

					}
					mycurrentmaster.StartSlave()
					cluster.LogSQL(logs, err, mycurrentmaster.URL, "Rejoin", config.LvlErr, "Failed to start slave on %s: %s", mycurrentmaster.URL, err)

					if server.IsMaintenance {
						server.SwitchMaintenance()
					}
					// if consul or internal proxy need to adapt read only route to new slaves
					cluster.backendStateChangeProxies()

				} else {
					//Adding state waiting for old master to rejoin in positional mode
					// this state prevent crash info to be removed
					cluster.SetState("ERR00049", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00049"], ErrFrom: "TOPO"})
				}
			}
		}
	}
	// In case of state change, reintroduce the server in the slave list
	if server.PrevState == stateFailed || server.PrevState == stateUnconn || server.PrevState == stateSuspect {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Set stateSlave from rejoin slave %s", server.URL)
		server.SetState(stateSlave)
		server.FailCount = 0
		if server.PrevState != stateSuspect {
			cluster.slaves = append(cluster.slaves, server)
		}
		if cluster.Conf.ReadOnly {
			logs, err := dbhelper.SetReadOnly(server.Conn, true)
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to set read only on server %s, %s ", server.URL, err)
			if err != nil {

				return err
			}
		}
	}

	return nil
}

func (server *ServerMonitor) isReplicationAheadOfMasterElection(crash *Crash) bool {
	cluster := server.ClusterGroup
	if server.UsedGtidAtElection(crash) {

		// CurrentGtid fetch from show global variables GTID_CURRENT_POS
		// FailoverIOGtid is fetch at failover from show slave status of the new master
		// If server-id can't be found in FailoverIOGtid can state cascading master failover
		if crash.FailoverIOGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()) == 0 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cascading failover, found empty GTID, forcing full state transfer")
			return true
		}
		if server.CurrentGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()) > crash.FailoverIOGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining node seq %d, master seq %d", server.CurrentGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()), crash.FailoverIOGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()))
			return true
		}
		return false
	} else {
		/*ss, errss := server.GetSlaveStatus(server.ReplicationSourceName)
		if errss != nil {
		 return	false
		}*/
		valid, logs, err := dbhelper.HaveExtraEvents(server.Conn, crash.FailoverMasterLogFile, crash.FailoverMasterLogPos)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlDbg, "Failed to  get extra bin log events server %s, %s ", server.URL, err)
		if err != nil {
			return false
		}
		if valid {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "No extra events after  file %s, pos %d is equal ", crash.FailoverMasterLogFile, crash.FailoverMasterLogPos)
			return true
		}
		return false
	}
}

func (server *ServerMonitor) saveBinlog(crash *Crash) error {
	cluster := server.ClusterGroup
	// Reuse the crash's OWN archive dir (created when the crash became known —
	// option B) so the binlog lands in the same dir as its crash.json; only mint a
	// new one for a crash that has none yet (legacy path).
	backupdir := crash.ArchiveDir
	if backupdir == "" {
		backupdir = cluster.Conf.WorkingDir + "/" + cluster.Name + "/crash-bin-" + time.Now().Format("20060102150405")
		crash.ArchiveDir = backupdir
	}
	staging := cluster.Conf.WorkingDir + "/" + cluster.Name + "-server" + strconv.FormatUint(uint64(server.ServerID), 10) + "-" + crash.FailoverMasterLogFile
	if _, err := os.Stat(staging); err != nil {
		// Nothing was captured (backupBinlog failed or produced no file):
		// do not create an empty archive directory that looks like a backup.
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Rejoin old Master %s: no captured binlog to archive (%s): %s", crash.URL, staging, err)
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoin old Master %s , backing up lost event to %s", crash.URL, backupdir)
	if err := os.MkdirAll(backupdir, 0777); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Could not create crash archive directory %s: %s", backupdir, err)
		return err
	}
	archived := backupdir + "/" + cluster.Name + "-server" + strconv.FormatUint(uint64(server.ServerID), 10) + "-" + crash.FailoverMasterLogFile
	if err := os.Rename(staging, archived); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Could not archive captured lost events %s: %s", staging, err)
		return err
	}
	crash.DeltaArchive = archived
	// Render the review material next to the archive: what happened and,
	// when reversible, the exact undo (lost-events viewer serves both).
	server.decodeLostEvents(crash, archived)
	// Write the FULL crash metadata INTO the archive dir: the crash-bin dir is the
	// single source of truth for a real crash (event + delta + rejoin outcome).
	// The history is derived by scanning these dirs (LoadFailoverHistory), so one
	// archive = one record — no duplicate failover.<ts>.json, survives restart,
	// pruned as a unit with its binlog.
	if err := crash.Save(backupdir + "/crash.json"); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Could not write crash metadata %s/crash.json: %s", backupdir, err)
	}
	server.purgeCrashBinArchives(3)
	return nil
}

// purgeCrashBinArchives bounds the disk used by crash-bin archives: keep the
// <keep> most recent crash-bin-* directories of this cluster, delete the rest
// (the timestamped name sorts chronologically). Pruning is logged so the
// retention is visible, unlike the former silent recursive wipe.
func (server *ServerMonitor) purgeCrashBinArchives(keep int) {
	cluster := server.ClusterGroup
	clusterdir := cluster.Conf.WorkingDir + "/" + cluster.Name
	entries, err := os.ReadDir(clusterdir)
	if err != nil {
		return
	}
	var archives []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "crash-bin-") {
			archives = append(archives, e.Name())
		}
	}
	if len(archives) <= keep {
		return
	}
	sort.Strings(archives)
	for _, name := range archives[:len(archives)-keep] {
		if err := os.RemoveAll(clusterdir + "/" + name); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Could not prune crash archive %s: %s", name, err)
			continue
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Pruned crash archive %s (keeping the %d most recent)", name, keep)
	}
}
// freezeThenCaptureLostEvents captures the diverged old master's binlog tail
// AFTER stopping it from growing. This is the fix for the empty-capture bug:
// the previous code captured from crash.FailoverMasterLogPos while the old
// master was STILL being written. During a split the proxy keeps routing
// writes to whatever it thinks is master — it connects SUPER, so read_only
// does not stop it, and the traffic marker fires every tick — so the divergent
// tail kept growing PAST the one-shot snapshot and the capture came back empty
// (proven on belair: anchor correct, moment ~35s too early). FLUSH TABLES WITH
// READ LOCK is the only thing that blocks even SUPER; take it, confirm the
// binlog position has stopped advancing, THEN capture the whole delta, then
// release so rejoinMasterIncremental can write.
func (server *ServerMonitor) freezeThenCaptureLostEvents(crash *Crash) error {
	cluster := server.ClusterGroup
	froze := false
	if err := server.FreezeWithReadLock(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "WARNING", "Could not freeze %s before lost-events capture (%s) — capturing live, the divergent tail may still be growing", server.URL, err)
	} else {
		froze = true
		server.waitBinlogSettle()
	}
	err := server.backupBinlog(crash)
	if froze {
		server.UnfreezeReadLock()
	}
	return err
}

// waitBinlogSettle blocks until the server's binlog position stops advancing
// (two consecutive identical SHOW MASTER STATUS reads) or a short timeout. It
// is called right after FreezeWithReadLock: FTWRL already waits for in-flight
// writes and blocks new commits, so the position is expected to be stable
// immediately — this poll just CONFIRMS it (and logs the settled anchor) before
// the capture, honoring "confirm its GTID stops advancing, then capture".
func (server *ServerMonitor) waitBinlogSettle() {
	cluster := server.ClusterGroup
	var lastFile string
	var lastPos uint
	stable := 0
	for i := 0; i < 10; i++ {
		ms, _, err := dbhelper.GetMasterStatus(server.Conn, server.DBVersion)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "WARNING", "Could not read master status of %s while waiting for binlog to settle: %s", server.URL, err)
			return
		}
		if ms.File == lastFile && ms.Position == lastPos {
			stable++
			if stable >= 1 {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Binlog of %s settled at %s:%d under read-lock freeze — capturing lost events", server.URL, ms.File, ms.Position)
				return
			}
		} else {
			stable = 0
			lastFile = ms.File
			lastPos = ms.Position
		}
		time.Sleep(300 * time.Millisecond)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "WARNING", "Binlog of %s still advancing after freeze wait (last %s:%d) — capturing anyway", server.URL, lastFile, lastPos)
}

func (server *ServerMonitor) backupBinlog(crash *Crash) error {
	cluster := server.ClusterGroup
	if _, err := os.Stat(cluster.GetMysqlBinlogPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "mysqlbinlog does not exist %s check binary path", cluster.GetMysqlBinlogPath())
		return err
	}
	if _, err := os.Stat(cluster.Conf.WorkingDir); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "WorkingDir does not exist %s check param working-directory", cluster.Conf.WorkingDir)
		return err
	}
	var cmdrun *exec.Cmd
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Backup ahead binlog events of previously failed server %s", server.URL)
	// Clean stale STAGING captures in the working-dir root only. A bootstrap /
	// RESET MASTER restarts the binlog sequence, so an old-generation staging
	// file carries the SAME name as the new capture and must never reach
	// flashback (60a8516ee). The archived copies under <cluster>/crash-bin-*
	// keep that same filename too, but are never replayed automatically — they
	// must SURVIVE: the previous recursive filepath.Walk here silently
	// destroyed every past crash archive on each new capture.
	if entries, errDir := os.ReadDir(cluster.Conf.WorkingDir); errDir == nil {
		prefix := cluster.Name + "-server" + strconv.FormatUint(uint64(server.ServerID), 10) + "-"
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
				os.Remove(cluster.Conf.WorkingDir + "/" + e.Name())
			}
		}
	}

	var params []string = make([]string, 0)
	if server.DBVersion.IsMySQLOrPerconaGreater84() {
		params = append(params, "--connection-server-id=10000")
	} else {
		params = append(params, "--stop-never-slave-server-id=10000")
	}
	params = append(params, "--read-from-remote-server", "--raw", "--user="+cluster.GetRplUser(), "--password="+cluster.GetRplPass(), "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--result-file="+cluster.Conf.WorkingDir+"/"+cluster.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-", "--start-position="+crash.FailoverMasterLogPos)
	params = append(params, server.GetSSLClientParam("client-binlog")...)
	params = append(params, crash.FailoverMasterLogFile)
	cmdrun = exec.Command(cluster.GetMysqlBinlogPath(), misc.RemoveEmptyString(params)...)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Backup %s %s", cluster.GetMysqlBinlogPath(), strings.ReplaceAll(strings.Join(cmdrun.Args, " "), cluster.GetRplPass(), "XXXX"))

	cmdErrPipe, _ := cmdrun.StderrPipe()
	cmdOutPipe, _ := cmdrun.StdoutPipe()

	if err := cmdrun.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Failed mysqlbinlog command: %s at %s", err, strings.ReplaceAll(cmdrun.String(), cluster.GetDbPass(), "XXXX"))
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		server.copyLogs(cmdErrPipe, config.ConstLogModTask, config.LvlErr)
	}()

	go func() {
		defer wg.Done()
		server.copyLogs(cmdOutPipe, config.ConstLogModTask, config.LvlDbg)
	}()

	wg.Wait()

	if err := cmdrun.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Failed to backup binlogs of %s,%s", server.URL, err.Error())
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s %s", cluster.GetMysqlBinlogPath(), strings.ReplaceAll(strings.Join(cmdrun.Args, " "), cluster.GetRplPass(), "XXXX"))
		cluster.SetState("WARN0182", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0182"], server.URL, err.Error()), ErrFrom: "REJOIN", ServerUrl: server.URL})
		cluster.canFlashBack = false
		return err
	}
	// Validate the artifact by content, not exit code: a capture holding no
	// events past the headers (format description + Start_encryption) is
	// ~300 bytes — it means the old master had nothing after the failover
	// anchor (a legitimate empty delta), whereas a missing file is a failure.
	staging := cluster.Conf.WorkingDir + "/" + cluster.Name + "-server" + strconv.FormatUint(uint64(server.ServerID), 10) + "-" + crash.FailoverMasterLogFile
	const binlogHeadersMaxSize = 512
	if st, errStat := os.Stat(staging); errStat != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Lost events capture produced no file %s: %s", staging, errStat)
		cluster.SetState("WARN0182", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0182"], server.URL, errStat.Error()), ErrFrom: "REJOIN", ServerUrl: server.URL})
		cluster.canFlashBack = false
		return errStat
	} else if st.Size() <= binlogHeadersMaxSize {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Lost events capture of %s is empty — no events after failover position %s:%s (nothing to flash back)", server.URL, crash.FailoverMasterLogFile, crash.FailoverMasterLogPos)
	}
	// The verdict on the capture decides the recovery path (WARN0184/0185,
	// flashback gate, viewer) — analyze while the staging file is at hand.
	server.analyzeLostEvents(crash, staging)
	// The delta verdict is computed HERE, at rejoin — after failover already
	// wrote failover.<ts>.json without it. Save the crash back to that same
	// durable record (the latest failover file) so the verdict survives the
	// purge of the volatile cluster.Crashes copy.
	if p := GetLastFailoverFile(cluster.WorkingDir); p != "" {
		crash.Save(p)
	}
	return nil
}

func (cluster *Cluster) RejoinClone(source *ServerMonitor, dest *ServerMonitor) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoining via master clone ")
	if dest.DBVersion.IsMySQL() && dest.DBVersion.Major >= 8 {
		if !dest.HasInstallPlugin("CLONE") {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Installing Clone plugin")
			dest.InstallPlugin("CLONE")
		}
		dest.ExecQueryNoBinLog("set global clone_valid_donor_list = '"+source.Host+":"+source.Port+"'", 5*time.Second)
		dest.ExecQueryNoBinLog("CLONE INSTANCE FROM "+dest.User+"@"+source.Host+":"+source.Port+" identified by '"+dest.Pass+"'", 5*time.Second)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Start slave after dump")
		dest.SetReplicationGTIDSlavePosFromServer(source)
		dest.StartSlave()
	} else {
		return errors.New("Version does not support cloning Master")
	}
	return nil
}

func (cluster *Cluster) RejoinFixRelay(slave *ServerMonitor, relay *ServerMonitor) error {
	if cluster.GetTopology() == config.TopoMultiMasterRing || cluster.GetTopology() == config.TopoMultiMasterWsrep {
		return nil
	}
	cluster.SetState("ERR00045", state.State{ErrType: "WARNING", ErrDesc: clusterError["ERR00045"], ErrFrom: "TOPO"})

	if slave.GetReplicationDelay() > cluster.Conf.FailMaxDelay {
		cluster.SetState("ERR00046", state.State{ErrType: "WARNING", ErrDesc: clusterError["ERR00046"], ErrFrom: "TOPO"})
		return nil
	} else {
		ss, err := slave.GetSlaveStatus(slave.ReplicationSourceName)
		if err == nil {
			slave.rejoinSlave(*ss)
		}
	}

	return nil
}

// UseGtid check is replication use gtid
func (server *ServerMonitor) UsedGtidAtElection(crash *Crash) bool {
	cluster := server.ClusterGroup
	/*
		ss, errss := server.GetSlaveStatus(server.ReplicationSourceName)
		if errss != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Failed to check if server was using GTID %s", errss)
			return false
		}


		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Rejoin server using GTID %s", ss.UsingGtid.String)
	*/
	if crash.FailoverIOGtid == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin server cannot find a saved master election GTID")
		return false
	}
	if len(crash.FailoverIOGtid.GetSeqNos()) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin server found a crash GTID greater than 0 ")
		return true
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin server can not found a GTID greater than 0 ")
	return false

}
