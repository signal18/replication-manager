// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
package cluster

import (
	"math"
	"time"
)

// BAUReading is the per-cluster REMOTE backup archive: what restic holds off the cluster on
// an S3 or SFTP repository (restic stats --mode raw-data, after deduplication), counted in
// BAU, Backup Archive Units. One BAU is the same disk quantity as one BKU (20 GB, the
// Storage profile ratio) and nothing else. The remote archive has NO plan: it is tracked and
// billed on usage, ceil(units), never warned about and never blocked.
//
// Priced only when the remote storage is Signal18 or partner infrastructure: the price is the
// instance's cloud18-marketplace-bau-price (server scope, 0 = not priced) and a cluster whose
// remote repository is the client's own storage (cloud18-marketplace-bau-client-storage) is
// tracked but never priced.
//
// Two roles (Stéphane 2026-09-28): CONSUMER = what this cluster holds on a remote archive
// (Bytes/Units); PRODUCER = the volumes this cluster's storage apps (app-s3-provider, e.g.
// minio on the SATA archive pool) allocate to host an archive for others: allocated volume
// size × copies, rounded up per app (ProducerBytes/ProducerUnits). Same unit, same price.
type BAUReading struct {
	Bytes         int64     `json:"bytes"`         // consumer: restic repository raw-data size on S3/SFTP
	Units         float64   `json:"units"`         // Bytes / (BAU disk)
	ProducerBytes int64     `json:"producerBytes"` // producer: Σ allocated volume × copies of the storage apps
	ProducerUnits int       `json:"producerUnits"` // Σ per app ceil(volume × copies / BAU disk)
	BilledUnits   int       `json:"billedUnits"`   // ceil(Units) + ProducerUnits
	Priced        bool      `json:"priced"`        // false when the client brought its own remote storage or no price is set
	UnitPrice     float64   `json:"unitPrice"`     // cloud18-marketplace-bau-price, Eur per BAU per month (0 when not priced)
	MonthlyCost   float64   `json:"monthlyCost"`   // BilledUnits × UnitPrice
	UnitBytes     int64     `json:"unitBytes"`     // bytes per BAU, from the Storage profile ratio
	UpdatedAt     time.Time `json:"updatedAt"`
}

// bauUnitPrice is the price one BAU costs this cluster per month: the instance price unless
// the cluster's remote repository is the client's own storage, then 0 (tracked, not priced).
func (cluster *Cluster) bauUnitPrice() float64 {
	if cluster.Conf.Cloud18MarketplaceBAUClientStorage {
		return 0
	}
	return cluster.Conf.Cloud18MarketplaceBAUPrice
}

// computeBAU builds the remote archive reading from the consumer bytes and the producer
// volumes (bytes and per-app rounded units).
func computeBAU(bytes, unitBytes int64, producerBytes int64, producerUnits int, unitPrice float64, now time.Time) *BAUReading {
	a := &BAUReading{Bytes: bytes, UnitBytes: unitBytes, ProducerBytes: producerBytes, ProducerUnits: producerUnits, UnitPrice: unitPrice, Priced: unitPrice > 0, UpdatedAt: now}
	if unitBytes > 0 {
		a.Units = float64(bytes) / float64(unitBytes)
	}
	if a.ProducerUnits < 0 {
		a.ProducerUnits = 0
	}
	a.BilledUnits = int(math.Ceil(a.Units)) + a.ProducerUnits
	if a.BilledUnits < 0 {
		a.BilledUnits = 0
	}
	a.MonthlyCost = float64(a.BilledUnits) * unitPrice
	return a
}
