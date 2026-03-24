// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains additions to types.go for schema graph / ERD support.
// Drop these declarations into types.go (or keep in a separate file in the
// same package dbhelper).  They depend only on standard library packages
// already imported by types.go.

package dbhelper

// RelationSource identifies how a table link was discovered.
type RelationSource string

const (
	RelationForeignKey      RelationSource = "foreign_key"
	RelationColumnNameMatch RelationSource = "column_name_match"
	RelationWorkloadQuery   RelationSource = "workload_query"
)

// Cardinality expresses the multiplicity of a relation.
type Cardinality string

const (
	CardinalityOneToOne   Cardinality = "1-1"
	CardinalityOneToMany  Cardinality = "1-N"
	CardinalityManyToMany Cardinality = "N-N"
)

// TableLink is a single directed edge stored on the Table that owns it.
// Each table carries two slices:
//
//	TableParents — edges where this table is the child (it references another)
//	TableChildren — edges where this table is the parent (others reference it)
//
// Both slices use the same struct so the frontend can render arrows in either
// direction without a secondary API call.
type TableLink struct {
	// Which table is on the other side of this edge.
	LinkedSchema string `json:"linked_schema"`
	LinkedTable  string `json:"linked_table"`

	// Columns on THIS table's side of the edge.
	LocalColumns []string `json:"local_columns"`
	// Columns on the OTHER table's side of the edge.
	RemoteColumns []string `json:"remote_columns"`

	// Constraint name (FK) or generated key (implicit match).
	RelationName string `json:"relation_name"`

	// How the link was discovered.
	Source RelationSource `json:"relation_source"`

	// Cardinality as seen from the perspective of this table.
	// On TableParents  edges: "1-N" means one parent row → many child rows.
	// On TableChildren edges: same cardinality, read from the other direction.
	Cardinality Cardinality `json:"cardinality"`

	// Join weight as a percentage of total workload JOIN operations observed
	// between these two tables. 0 until a workload pass fills it in.
	JoinWeightPct float64 `json:"join_weight_pct,omitempty"`
}
