// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// server.go
package server

import (
	"context"

	goofys "github.com/signal18/replication-manager/goofys/api"
	common "github.com/signal18/replication-manager/goofys/api/common"
	log "github.com/sirupsen/logrus"
)

func (repman *ReplicationManager) UnMountS3() {
	if !repman.Conf.BackupStreaming {
		return
	}
	err := goofys.TryUnmount(repman.Conf.WorkingDir + "/s3")
	if err != nil {
		log.Errorf("Failed to unmount S3 in response to %s: %s", repman.Conf.WorkingDir+"/backups", err)
	} else {
		log.Printf("Successfully unmounting S3 streaming backup")
	}
	return
}

func (repman *ReplicationManager) MountS3() {
	if !repman.Conf.BackupStreaming {
		return
	}
	bucketName := repman.Conf.BackupStreamingBucket
	conf := (&common.S3Config{
		AccessKey: repman.Conf.BackupStreamingAwsAccessKeyId,
		SecretKey: repman.Conf.BackupStreamingAwsAccessSecret,
		Region:    repman.Conf.BackupStreamingRegion,
	}).Init()

	config := common.FlagStorage{
		MountPoint: repman.Conf.WorkingDir + "/s3",
		DirMode:    0755,
		FileMode:   0644,
		Endpoint:   repman.Conf.BackupStreamingEndpoint,
		Backend:    conf,
	}
	log.Infof("Mount S3 to %:s", config.MountPoint)
	/*	s3, err := internal.NewS3("", config, conf)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Unable to connect s3 %v: %v", config.MountPoint, err)
		}
		_, err := s3.CreateBucket(&s3.CreateBucketInput{
			Bucket: &bucket,
		})*/

	_, mp, err := goofys.Mount(context.Background(), bucketName, &config)
	if err != nil {
		log.Errorf("Unable to mount %s: %s", config.MountPoint, err)
	} else {
		mp.Join(context.Background())
	}
	return
}
