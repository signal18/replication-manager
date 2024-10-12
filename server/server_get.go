// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"os"

	"github.com/signal18/replication-manager/cluster"
	log "github.com/sirupsen/logrus"
)

func (repman *ReplicationManager) getClusterByName(clname string) *cluster.Cluster {
	var c *cluster.Cluster
	repman.Lock()
	c = repman.Clusters[clname]
	repman.Unlock()
	return c
}

func (repman *ReplicationManager) GetExtraConfigDir() string {

	if repman.Conf.WithEmbed == "ON" {
		return repman.OsUser.HomeDir + "/.config/replication-manager"
	}

	return "/home/repman/.config/replication-manager"
}

func (repman *ReplicationManager) GetExtraDataDir() string {
	dirname, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	return dirname + "/replication-manager"
}

// func (repman *ReplicationManager) GenerateKey(conf *config.Config) error {
// 	var err error
// 	_, err = os.Stat(conf.MonitoringKeyPath)
// 	// Check if the file does not exist
// 	if err == nil {
// 		repman.Logrus.Infof("Repman discovered that key is already generated. Using existing key.")
// 		return nil
// 	} else {
// 		if !os.IsNotExist(err) {
// 			repman.Logrus.Infof("Error when checking key for encryption: %v", err)
// 			return err
// 		}

// 		newdir := "/home/repman/.config/replication-manager/etc"
// 		if conf.WithEmbed == "ON" {
// 			newdir = repman.OsUser.HomeDir + "/.config/replication-manager/etc"
// 		}

// 		newpath := newdir + "/.replication-manager.key"

// 		_, err = os.Stat(newpath)
// 		if err == nil {
// 			Logger.Infof("Repman discovered key in alternative path. Using existing key on %s", newpath)
// 			return nil
// 		}

// 		Logger.Infof("Key not found. Generating : %s", conf.MonitoringKeyPath)

// 		if err = misc.TryOpenFile(conf.MonitoringKeyPath, os.O_WRONLY|os.O_CREATE, 0600, true); err != nil && conf.WithEmbed == "OFF" {
// 			newdir := "/home/repman/.config/replication-manager/etc"
// 			newpath := newdir + "/.replication-manager.key"

// 			Logger.Infof("File %s is not accessible. Try using alternative path: %s", conf.MonitoringKeyPath, newpath)

// 			_, err := os.Stat(newpath)
// 			if err == nil {
// 				Logger.Infof("Repman discovered key in alternative path. Using existing key on %s", newpath)
// 				return nil
// 			}

// 			_, err = os.Stat(newdir)
// 			if err != nil {
// 				if !os.IsNotExist(err) {
// 					Logger.Errorf("Can't access %s : %v", newdir, err)
// 					return err
// 				} else {
// 					err = os.MkdirAll(newdir, 0755)
// 					if err != nil {
// 						Logger.Errorf("Can't create directory %s : %v", newdir, err)
// 						return err
// 					}
// 				}
// 			}

// 			if err := misc.TryOpenFile(newpath, os.O_WRONLY|os.O_CREATE, 0600, true); err != nil {
// 				Logger.Errorf("Can't write keys in %s : %v", newdir, err)
// 				return err
// 			}

// 			// New path is writable
// 			conf.MonitoringKeyPath = newpath
// 			Logger.Infof("Path writable. Flag 'monitoring-key-path' set to: %s.", newpath)
// 			Logger.Infof("Generating key on: %s", conf.MonitoringKeyPath)

// 		}

// 		p := crypto.Password{}
// 		var err error
// 		p.Key, err = crypto.Keygen()
// 		if err != nil {
// 			Logger.Errorf("Error when generating key for encryption: %v", err)
// 			return err
// 		}
// 		err = crypto.WriteKey(p.Key, conf.MonitoringKeyPath, false)
// 		if err != nil {
// 			Logger.Errorf("Error when writing key for encryption: %v", err)
// 			return err
// 		}
// 	}

// 	return nil
// }
