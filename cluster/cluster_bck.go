// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/signal18/replication-manager/utils/state"
)

type Backup struct {
	Id       string   `json:"id"`
	ShortId  string   `json:"short_id"`
	Time     string   `json:"time"`
	Tree     string   `json:"tree"`
	Paths    []string `json:"paths"`
	Hostname string   `json:"hostname"`
	Username string   `json:"username"`
	UID      int64    `json:"uid"`
	GID      int64    `json:"gid"`
}

func (cluster *Cluster) ResticPurgeRepo() error {
	if cluster.Conf.BackupRestic {
		//		var stdout, stderr []byte
		var stdoutBuf, stderrBuf bytes.Buffer
		var errStdout, errStderr error
		resticcmd := exec.Command(cluster.Conf.BackupResticBinaryPath, "prune", "--keep-last", "10", "--keep-hourly", strconv.Itoa(cluster.Conf.BackupKeepHourly), "--keep-daily", strconv.Itoa(cluster.Conf.BackupKeepDaily), "--keep-weekly", strconv.Itoa(cluster.Conf.BackupKeepWeekly), "--keep-monthly", strconv.Itoa(cluster.Conf.BackupKeepMonthly), "--keep-yearly", strconv.Itoa(cluster.Conf.BackupKeepYearly))
		stdoutIn, _ := resticcmd.StdoutPipe()
		stderrIn, _ := resticcmd.StderrPipe()
		stdout := io.MultiWriter(os.Stdout, &stdoutBuf)
		stderr := io.MultiWriter(os.Stderr, &stderrBuf)

		newEnv := append(os.Environ(), "AWS_ACCESS_KEY_ID="+cluster.Conf.BackupResticAwsAccessKeyId)
		newEnv = append(newEnv, "AWS_SECRET_ACCESS_KEY="+cluster.Conf.BackupResticAwsAccessSecret)
		newEnv = append(newEnv, "RESTIC_REPOSITORY="+cluster.Conf.BackupResticRepository)
		newEnv = append(newEnv, "RESTIC_PASSWORD="+cluster.Conf.BackupResticPassword)
		resticcmd.Env = newEnv
		if err := resticcmd.Start(); err != nil {
			cluster.LogPrintf(LvlErr, "Failed restic command : %s %s", resticcmd.Path, err)
			return err
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			_, errStdout = io.Copy(stdout, stdoutIn)
			wg.Done()
		}()

		_, errStderr = io.Copy(stderr, stderrIn)
		wg.Wait()

		err := resticcmd.Wait()
		if err != nil {
			cluster.sme.AddState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err, string(stdoutBuf.Bytes()), string(stderrBuf.Bytes())), ErrFrom: "CHECK"})
			return err
		}
		if errStdout != nil || errStderr != nil {
			return errors.New("failed to capture stdout or stderr\n")
		}
	}
	return nil
}

func (cluster *Cluster) ResticInitRepo() error {
	if cluster.Conf.BackupRestic {
		//		var stdout, stderr []byte
		var stdoutBuf, stderrBuf bytes.Buffer
		var errStdout, errStderr error
		resticcmd := exec.Command(cluster.Conf.BackupResticBinaryPath, "init")
		stdoutIn, _ := resticcmd.StdoutPipe()
		stderrIn, _ := resticcmd.StderrPipe()
		stdout := io.MultiWriter(os.Stdout, &stdoutBuf)
		stderr := io.MultiWriter(os.Stderr, &stderrBuf)

		newEnv := append(os.Environ(), "AWS_ACCESS_KEY_ID="+cluster.Conf.BackupResticAwsAccessKeyId)
		newEnv = append(newEnv, "AWS_SECRET_ACCESS_KEY="+cluster.Conf.BackupResticAwsAccessSecret)
		newEnv = append(newEnv, "RESTIC_REPOSITORY="+cluster.Conf.BackupResticRepository)
		newEnv = append(newEnv, "RESTIC_PASSWORD="+cluster.Conf.BackupResticPassword)
		resticcmd.Env = newEnv
		if err := resticcmd.Start(); err != nil {
			cluster.LogPrintf(LvlErr, "Failed restic command : %s %s", resticcmd.Path, err)
			return err
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			_, errStdout = io.Copy(stdout, stdoutIn)
			wg.Done()
		}()

		_, errStderr = io.Copy(stderr, stderrIn)
		wg.Wait()

		err := resticcmd.Wait()
		if err != nil {
			cluster.sme.AddState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err, string(stdoutBuf.Bytes()), string(stderrBuf.Bytes())), ErrFrom: "CHECK"})
		}
		if errStdout != nil || errStderr != nil {
			return errors.New("failed to capture stdout or stderr\n")
		}
	}
	return nil
}

func (cluster *Cluster) ResticFetchRepo() error {
	if cluster.Conf.BackupRestic {
		//		var stdout, stderr []byte
		var stdoutBuf, stderrBuf bytes.Buffer
		var errStdout, errStderr error
		resticcmd := exec.Command(cluster.Conf.BackupResticBinaryPath, "snapshots", "--json")
		stdoutIn, _ := resticcmd.StdoutPipe()
		stderrIn, _ := resticcmd.StderrPipe()
		stdout := io.MultiWriter(os.Stdout, &stdoutBuf)
		stderr := io.MultiWriter(os.Stderr, &stderrBuf)

		newEnv := append(os.Environ(), "AWS_ACCESS_KEY_ID="+cluster.Conf.BackupResticAwsAccessKeyId)
		newEnv = append(newEnv, "AWS_SECRET_ACCESS_KEY="+cluster.Conf.BackupResticAwsAccessSecret)
		newEnv = append(newEnv, "RESTIC_REPOSITORY="+cluster.Conf.BackupResticRepository)
		newEnv = append(newEnv, "RESTIC_PASSWORD="+cluster.Conf.BackupResticPassword)
		resticcmd.Env = newEnv
		if err := resticcmd.Start(); err != nil {
			cluster.LogPrintf(LvlErr, "Failed restic command : %s %s", resticcmd.Path, err)
			return err
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			_, errStdout = io.Copy(stdout, stdoutIn)
			wg.Done()
		}()

		_, errStderr = io.Copy(stderr, stderrIn)
		wg.Wait()

		err := resticcmd.Wait()
		if err != nil {
			cluster.sme.AddState("WARN0093", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0093"], err, string(stdoutBuf.Bytes()), string(stderrBuf.Bytes())), ErrFrom: "CHECK"})
			cluster.ResticInitRepo()
			return err
		}
		if errStdout != nil || errStderr != nil {
			return errors.New("failed to capture stdout or stderr\n")
		}

		var repo []Backup
		err = json.Unmarshal(stdoutBuf.Bytes(), &repo)
		if err != nil {
			cluster.LogPrintf(LvlInfo, "Error unmaeshal backups %s", err)
			return err
		}
		var filterRepo []Backup
		for _, bck := range repo {
			if strings.Contains(bck.Paths[0], cluster.Name) {
				filterRepo = append(filterRepo, bck)
			}
		}
		cluster.Backups = filterRepo
	}

	return nil
}
