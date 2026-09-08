// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"sync"
	"time"
)

// ResourceManager is the repman-side, infra-wide resource authority (Epic #1776). It is
// created ONCE by ReplicationManager and injected into every Cluster (SetResourceManager),
// so it lives ABOVE the ServerMonitor and the Cluster -- both recreated on a config
// reload (newServerList), which is exactly what blanks the in-memory reading and
// makes the DBU graph flap. Living here, the reading survives those recreations.
//
// It holds two things, keyed to survive reload:
//   - consumed: the last measured DBUReading per server (task #1777)
//   - capacity: the per-agent physical ceiling, monitored + modifiable (tasks #1778/#1779)
//
// and is the single home onto which the overcommit / reservations / billing layers
// stack later (#1776) -- because only repman sees every server on a given agent and
// can net their consumption. All mutation goes through the internal RWMutex; callers
// never touch the maps directly.
type ResourceManager struct {
	mu sync.RWMutex

	// DB servers -> DBU (workload profile Database). "server" = a ServerMonitor (DB node).
	consumed    map[ResourceKey]*DBUReading // real consumed, per DB server (measured; nil if never pushed)
	plan        map[ResourceKey]*DBUReading // planned/allocated, per DB server -- the client's TECHNICAL CONTRACT (from prov-db-* config)
	serverAgent map[ResourceKey]string      // which agent a DB server runs on -- for the per-agent view

	// Apps -> APU (workload profile Compute). A SEPARATE track: an app (proxy/phpMyAdmin/
	// stateless) is NOT a database. Own key, own maps, own reading type -- never mixed
	// with the DB-server maps above. They only meet at agent capacity.
	appConsumed map[AppKey]*APUReading // real consumed, per app
	appPlan     map[AppKey]*APUReading // planned/allocated, per app -- the app's technical contract
	appAgent    map[AppKey]string      // which agent an app runs on

	// Shared physical side (both DB servers and apps are placed on agents).
	capacity map[string]*AgentCapacity // per agent (node) name
	quotaPct float64                   // resource-manager-infra-quota-pct: share of the metal repman may take

	// Unit ratios per workload profile -- owned by the manager, because it is the
	// point where native resources converge and get projected into units. A database
	// is NOT an app: each profile locks a DIFFERENT resource ratio (see
	// CLOUD18_CREDIT_MODEL.md). DBU/APU/Storage are just workload-classified unit
	// projections over the resources stored here. The ratios are CONFIGURABLE (the
	// product does not hard-lock them) -- the marketplace "lock" is a commercial
	// policy, not a code constant -- so a partner/operator can retune or add profiles.
	ratios map[WorkloadProfile]UnitRatios
}

// WorkloadProfile classifies a consumer so the right unit ratio applies.
type WorkloadProfile string

const (
	ProfileDatabase WorkloadProfile = "database" // MariaDB/MySQL -- strict, coupled, IOPS locked
	ProfileCompute  WorkloadProfile = "compute"  // proxy/phpMyAdmin/stateless -- little disk, no IOPS
	ProfileStorage  WorkloadProfile = "storage"  // backup/S3-like -- disk-dominant (ratios TBD)
)

// UnitRatios is one profile's resource-per-unit lock: how much of each native axis
// equals one unit. A zero axis means "not part of this unit" (e.g. Compute has no
// IOPS lock) -- that axis is excluded from the projection and never binds.
type UnitRatios struct {
	CoresPerUnit  float64
	MemMBPerUnit  float64
	DiskGBPerUnit float64
	IopsPerUnit   float64 // 0 = axis excluded
}

// ResourceKey materializes the two-part identity of a per-server reading: the cluster and
// the server within it. A typed struct, NOT a "cluster/server" string concatenation,
// so a name containing the separator can never collide and the two parts stay typed.
type ResourceKey struct {
	Cluster string
	Server  string
}

// AgentCapacity is the per-axis physical ceiling of ONE agent (host). The fleet is
// heterogeneous (NVMe vs SATA, 8 vs 64 cores), so capacity is carried per agent, as
// an editable value per axis -- never a flat global that could not express that.
//
// Each axis is monitored/measured then optionally overridden by an admin:
//   - cores/mem/disk: seeded from orchestrator node stats (task #1778)
//   - iops: seeded from a multi-core sysbench calibration (task #1779), since IOPS is
//     not reliably reported by monitoring
//
// AxisSource records, per axis, whether the live value came from monitoring or an
// admin override, so the GUI/API can show provenance and a reseed never clobbers a
// deliberate override blindly.
type AgentCapacity struct {
	Cores  float64 `json:"cores"`  // physical cores
	MemMB  float64 `json:"memMB"`  // MB
	DiskGB float64 `json:"diskGB"` // GB
	Iops   float64 `json:"iops"`   // calibrated iops

	// AxisSource[axis] is "monitored" or "admin"; axis is one of "cpu","mem","io","disk".
	AxisSource map[string]string `json:"axisSource"`
	Updated    time.Time         `json:"updated"`
}

// NewResourceManager builds an empty authority. Wired once in server.initCluster.
func NewResourceManager() *ResourceManager {
	return &ResourceManager{
		consumed:    make(map[ResourceKey]*DBUReading),
		plan:        make(map[ResourceKey]*DBUReading),
		serverAgent: make(map[ResourceKey]string),
		appConsumed: make(map[AppKey]*APUReading),
		appPlan:     make(map[AppKey]*APUReading),
		appAgent:    make(map[AppKey]string),
		capacity:    make(map[string]*AgentCapacity),
		// Default per-profile ratios (the operator's rules; configurable, not locked).
		// DB from CLOUD18_CREDIT_MODEL.md; Compute mem = 2 GB (NOT the doc's 4 GB): with
		// refund, contractualising 4 GB for a light app/proxy is wasteful -- reserve
		// modest, real consumption + refund handle the rest. Storage TBD (zero = undefined).
		ratios: map[WorkloadProfile]UnitRatios{
			ProfileDatabase: {CoresPerUnit: 1.0, MemMBPerUnit: 4096.0, DiskGBPerUnit: 40.0, IopsPerUnit: 1000.0},
			ProfileCompute:  {CoresPerUnit: 1.0, MemMBPerUnit: 2048.0, DiskGBPerUnit: 10.0, IopsPerUnit: 0.0}, // 2GB, no IOPS
			ProfileStorage:  {},                                                                               // TBD
		},
	}
}

// DBMemMBPerUnit is the Database-profile memory ratio (MB per DBU) -- the single source used
// to size the DBU-aligned container memory cap. Keeps the ratio owned by the ResourceManager.
func (m *ResourceManager) DBMemMBPerUnit() float64 {
	return m.ratios[ProfileDatabase].MemMBPerUnit
}

// SetProfileRatios reconfigures one workload profile's unit ratios -- the operator's
// rule for that profile. The product does not lock these; nothing is contracted or
// billed outside the ratios the operator sets here.
func (m *ResourceManager) SetProfileRatios(profile WorkloadProfile, r UnitRatios) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ratios[profile] = r
}

// Ratios returns a profile's current unit ratios (empty if the profile is undefined,
// e.g. Storage until set).
func (m *ResourceManager) Ratios(profile WorkloadProfile) UnitRatios {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ratios[profile]
}

// ComputeUsedDBU turns the four raw per-axis maxima (native units, as pushed by the
// sensor, or a plan's provisioned allocation) into a DBUReading, using THIS manager's
// ratios: normalise each axis to DBU, pivot = the max, Binding = the argmax. Pure
// over the manager's ratios -- no I/O, no client cost. This is the single conversion
// path; the API handler forwards raw maxima and lets the manager own the ratios.
func (m *ResourceManager) ComputeUsedDBU(start, end time.Time, memMaxBytes int64, cpuMaxCores, ioMaxIops float64, diskMaxBytes int64) DBUReading {
	m.mu.RLock()
	ratios := m.ratios[ProfileDatabase]
	m.mu.RUnlock()
	cpu, mem, io, disk, pivot, binding := ratios.project(memMaxBytes, cpuMaxCores, ioMaxIops, diskMaxBytes)
	return DBUReading{
		WindowStart:  start,
		WindowEnd:    end,
		MemMaxBytes:  memMaxBytes,
		CpuMaxCores:  cpuMaxCores,
		IoMaxIops:    ioMaxIops,
		DiskMaxBytes: diskMaxBytes,
		DbuMem:       mem,
		DbuCpu:       cpu,
		DbuIo:        io,
		DbuDisk:      disk,
		Dbu:          pivot,
		Binding:      binding,
	}
}

// project normalises native maxima into THIS profile's units, per axis, and returns
// the pivot (max) and the binding axis. An axis whose ratio is 0 is EXCLUDED from the
// projection and can never bind (e.g. Compute has no IOPS lock). Tie order cpu > mem >
// io > disk (cpu wins ties), matching the original DBU semantics.
func (r UnitRatios) project(memMaxBytes int64, cpuMaxCores, ioMaxIops float64, diskMaxBytes int64) (cpu, mem, io, disk, pivot float64, binding string) {
	cpu = unitDiv(cpuMaxCores, r.CoresPerUnit)
	mem = unitDiv(float64(memMaxBytes)/(1024*1024), r.MemMBPerUnit)
	io = unitDiv(ioMaxIops, r.IopsPerUnit)
	disk = unitDiv(float64(diskMaxBytes)/(1024*1024*1024), r.DiskGBPerUnit)

	pivot, binding = -1, ""
	if r.CoresPerUnit > 0 && cpu > pivot {
		pivot, binding = cpu, "cpu"
	}
	if r.MemMBPerUnit > 0 && mem > pivot {
		pivot, binding = mem, "mem"
	}
	if r.IopsPerUnit > 0 && io > pivot {
		pivot, binding = io, "io"
	}
	if r.DiskGBPerUnit > 0 && disk > pivot {
		pivot, binding = disk, "disk"
	}
	if pivot < 0 { // no axis defined for this profile (e.g. Storage until set)
		pivot = 0
	}
	return
}

// unitDiv divides x by ratio, returning 0 when the ratio is <= 0 (axis excluded).
func unitDiv(x, ratio float64) float64 {
	if ratio <= 0 {
		return 0
	}
	return x / ratio
}

// ============================================================================
// APP track -> APU. SEPARATE from the DB-server track above. An app (proxy /
// phpMyAdmin / stateless) is NOT a database: its own key (AppKey), its own maps,
// its own reading type (APUReading). Same shared normalisation math, different
// profile ratios. The two tracks only meet at agent capacity.
// ============================================================================

// AppKey identifies an app within a cluster -- kept distinct from ResourceKey.
type AppKey struct {
	Cluster string
	App     string
}

// APUReading is one period's consumed-APU picture for an app (workload profile
// Compute: cpu/mem bound, little disk, NO IOPS). Same multi-axis shape as a
// DBUReading but a DISTINCT type with apu-named fields, so DB (DBU) and app (APU)
// never get mixed on the wire or in the GUI.
type APUReading struct {
	WindowStart time.Time `json:"windowStart"`
	WindowEnd   time.Time `json:"windowEnd"`

	MemMaxBytes  int64   `json:"memMaxBytes"`
	CpuMaxCores  float64 `json:"cpuMaxCores"`
	DiskMaxBytes int64   `json:"diskMaxBytes"`

	ApuCpu  float64 `json:"apuCpu"`
	ApuMem  float64 `json:"apuMem"`
	ApuDisk float64 `json:"apuDisk"`

	Apu     float64 `json:"apu"`
	Binding string  `json:"binding"` // "cpu" | "mem" | "disk" (no io -- Compute has no IOPS lock)
}

// ComputeUsedAPU projects an app's native maxima into APU using the Compute profile
// ratios (no IOPS axis). Reuses the shared normalisation -- same math, different
// ratios, distinct output type.
func (m *ResourceManager) ComputeUsedAPU(start, end time.Time, memMaxBytes int64, cpuMaxCores float64, diskMaxBytes int64) APUReading {
	m.mu.RLock()
	ratios := m.ratios[ProfileCompute]
	m.mu.RUnlock()
	cpu, mem, _, disk, pivot, binding := ratios.project(memMaxBytes, cpuMaxCores, 0, diskMaxBytes)
	return APUReading{
		WindowStart:  start,
		WindowEnd:    end,
		MemMaxBytes:  memMaxBytes,
		CpuMaxCores:  cpuMaxCores,
		DiskMaxBytes: diskMaxBytes,
		ApuCpu:       cpu,
		ApuMem:       mem,
		ApuDisk:      disk,
		Apu:          pivot,
		Binding:      binding,
	}
}

// SetAppConsumed records an app's latest measured APU reading.
func (m *ResourceManager) SetAppConsumed(k AppKey, r *APUReading) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appConsumed[k] = r
}

// GetAppConsumed returns an app's last APU reading, or nil if never pushed.
func (m *ResourceManager) GetAppConsumed(k AppKey) *APUReading {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.appConsumed[k]
}

// SetAppPlan records an app's planned/allocated APU (its technical contract).
func (m *ResourceManager) SetAppPlan(k AppKey, r *APUReading) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appPlan[k] = r
}

// GetAppPlan returns an app's planned/allocated APU, or nil if not set.
func (m *ResourceManager) GetAppPlan(k AppKey) *APUReading {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.appPlan[k]
}

// SetAppAgent records which agent an app runs on, for the per-agent view.
func (m *ResourceManager) SetAppAgent(k AppKey, agent string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appAgent[k] = agent
}

// ============================================================================
// Economic substrate: delta (contract - real) and agent slack (the pool). Raw
// technical quantities only -- the commercial layer (epic) applies the partial
// refund % on positive delta and the overage premium on negative delta; the
// physical cap on overage (Σ real <= agent ceiling) is technical and lives here.
// ============================================================================

// DeltaByCluster is contract - real for a cluster, per axis + global. Positive =
// headroom: the client is over-contracted (paying for unused -> could shrink, net of
// the partial-refund loss) and the headroom also absorbs bursts while the non-instant
// dynamic resize catches up. Negative = overage: real > contract, compensated from the
// agent pool and/or billed at a premium, capped by agent capacity. Binding is left
// empty (a delta is a difference, not a pivot).
func (m *ResourceManager) DeltaByCluster(clusterName string) DBUAggregate {
	plan := m.PlanByCluster(clusterName)
	real := m.ConsumedByCluster(clusterName)
	return DBUAggregate{
		DbuCpu:  plan.DbuCpu - real.DbuCpu,
		DbuMem:  plan.DbuMem - real.DbuMem,
		DbuIo:   plan.DbuIo - real.DbuIo,
		DbuDisk: plan.DbuDisk - real.DbuDisk,
		Dbu:     plan.Dbu - real.Dbu,
		Servers: real.Servers,
	}
}

// AgentSlackDBU is the agent's remaining pool: usable ceiling (metal × cap%) minus the
// real consumed on that agent. Positive = room to place/burst (this is the slack that
// funds over-consumers' bursts); <= 0 = the agent is full, no more overage can be
// absorbed there (the hard physical cap). ok=false when the agent has no capacity yet.
func (m *ResourceManager) AgentSlackDBU(agent string) (float64, bool) {
	ceiling, ok := m.UsableCeilingDBU(agent)
	if !ok {
		return 0, false
	}
	real := m.ConsumedByAgent(agent)
	return ceiling - real.Dbu, true
}

// DBUAggregate is a sum of consumed readings -- the two notions of "real consumed
// DBU": per cluster, and per agent. It is broken down per axis (so it can be netted
// against the per-axis agent capacity) plus a global pivot. Dbu is the max of the
// summed axes -- the axis that saturates first (Binding) -- i.e. how full the scope
// is in DBU terms, not a naive sum of per-server pivots (which could double-count
// across axes).
type DBUAggregate struct {
	DbuCpu  float64 `json:"dbuCpu"`
	DbuMem  float64 `json:"dbuMem"`
	DbuIo   float64 `json:"dbuIo"`
	DbuDisk float64 `json:"dbuDisk"`
	Dbu     float64 `json:"dbu"`     // global: max of the summed axes (the binding)
	Binding string  `json:"binding"` // "cpu" | "mem" | "io" | "disk"
	Servers int     `json:"servers"` // how many servers contributed a reading
}

// --- consumed (per server) --------------------------------------------------

// SetConsumed records a server's latest reading. Written from
// ServerMonitor.SetDBUConsumed (the sensor push callback).
func (m *ResourceManager) SetConsumed(k ResourceKey, r *DBUReading) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.consumed[k] = r
}

// GetConsumed returns a server's last reading, or nil if it never pushed (server off,
// or not configured to send metrics). nil stays nil -- never a fabricated value.
// Read from ServerMonitor.RestoreDBUConsumed after a ServerMonitor recreation.
func (m *ResourceManager) GetConsumed(k ResourceKey) *DBUReading {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.consumed[k]
}

// SetServerAgent records which agent (node) a server runs on, so the per-agent view
// can net the servers co-located on the same metal. Wired by the physical monitoring
// (task #1778) as it discovers each server's placement.
func (m *ResourceManager) SetServerAgent(k ResourceKey, agent string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.serverAgent[k] = agent
}

// ConsumedByCluster is notion #1: the real consumed DBU summed over the cluster's
// servers, broken down per axis + global.
func (m *ResourceManager) ConsumedByCluster(clusterName string) DBUAggregate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var readings []*DBUReading
	for k, r := range m.consumed {
		if k.Cluster == clusterName && r != nil {
			readings = append(readings, r)
		}
	}
	return sumReadings(readings)
}

// ConsumedByAgent is notion #2: the real consumed DBU summed over the servers running
// on one agent (across all clusters), broken down per axis + global -- this is what
// gets netted against the agent's per-axis physical capacity for overcommit.
func (m *ResourceManager) ConsumedByAgent(agent string) DBUAggregate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var readings []*DBUReading
	for k, r := range m.consumed {
		if m.serverAgent[k] == agent && r != nil {
			readings = append(readings, r)
		}
	}
	return sumReadings(readings)
}

// --- plan / allocated (the client's technical contract) ---------------------

// SetPlan records a server's PLANNED (allocated) DBU -- the client's technical
// contract, derived from its prov-db-* config (service plan). Unlike consumed it is
// always known (no sensor). Build the reading with ComputeUsedDBU over the
// provisioned cores/mem/disk/iops so the normalisation stays in one place (T2).
func (m *ResourceManager) SetPlan(k ResourceKey, r *DBUReading) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plan[k] = r
}

// GetPlan returns a server's planned/allocated DBU (its technical contract), or nil
// if not yet set.
func (m *ResourceManager) GetPlan(k ResourceKey) *DBUReading {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.plan[k]
}

// PlanByCluster is the cluster's technical contract: the planned DBU summed over its
// servers, per axis + global.
func (m *ResourceManager) PlanByCluster(clusterName string) DBUAggregate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var readings []*DBUReading
	for k, r := range m.plan {
		if k.Cluster == clusterName && r != nil {
			readings = append(readings, r)
		}
	}
	return sumReadings(readings)
}

// sumReadings adds per-axis DBU across readings; the global pivot is the max of the
// summed axes (the axis that saturates first). Caller holds the lock.
func sumReadings(readings []*DBUReading) DBUAggregate {
	var a DBUAggregate
	for _, r := range readings {
		a.DbuCpu += r.DbuCpu
		a.DbuMem += r.DbuMem
		a.DbuIo += r.DbuIo
		a.DbuDisk += r.DbuDisk
		a.Servers++
	}
	a.Dbu, a.Binding = a.DbuCpu, "cpu"
	if a.DbuMem > a.Dbu {
		a.Dbu, a.Binding = a.DbuMem, "mem"
	}
	if a.DbuIo > a.Dbu {
		a.Dbu, a.Binding = a.DbuIo, "io"
	}
	if a.DbuDisk > a.Dbu {
		a.Dbu, a.Binding = a.DbuDisk, "disk"
	}
	return a
}

// --- capacity (per agent) ---------------------------------------------------

// SetAgentCapacity stores/updates an agent's per-axis physical ceiling.
func (m *ResourceManager) SetAgentCapacity(agent string, c *AgentCapacity) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.capacity[agent] = c
}

// GetAgentCapacity returns an agent's capacity, or nil if not yet observed/declared.
func (m *ResourceManager) GetAgentCapacity(agent string) *AgentCapacity {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.capacity[agent]
}

// SetQuotaPct sets the share of the metal repman is allowed to take (0 < pct <= 100),
// from resource-manager-infra-quota-pct. It is a global policy, not per-agent physics.
func (m *ResourceManager) SetQuotaPct(pct float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.quotaPct = pct
}

// capacityDBU converts an agent's raw per-axis ceiling into DBU using THIS manager's
// ratios, and returns the binding axis -- the metal's scarcest resource in DBU terms.
// Caller holds the lock (the ratios are set once at construction).
func (m *ResourceManager) capacityDBU(c *AgentCapacity) (dbu float64, binding string) {
	ratios := m.ratios[ProfileDatabase]
	dbuCores := unitDiv(c.Cores, ratios.CoresPerUnit)
	dbuMem := unitDiv(c.MemMB, ratios.MemMBPerUnit)
	dbuDisk := unitDiv(c.DiskGB, ratios.DiskGBPerUnit)
	dbuIo := unitDiv(c.Iops, ratios.IopsPerUnit)

	dbu, binding = dbuCores, "cpu"
	if dbuMem < dbu {
		dbu, binding = dbuMem, "mem"
	}
	if dbuIo < dbu {
		dbu, binding = dbuIo, "io"
	}
	if dbuDisk < dbu {
		dbu, binding = dbuDisk, "disk"
	}
	return dbu, binding
}

// UsableCeilingDBU is the agent's DBU ceiling AFTER the repman quota (metal x cap%)
// -- the real bound placement and burst work against, so repman never starves the
// client's non-repman workloads on the same agent. cap% <= 0 means "unset": the full
// metal is returned (no quota applied yet).
func (m *ResourceManager) UsableCeilingDBU(agent string) (float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := m.capacity[agent]
	if c == nil {
		return 0, false
	}
	metal, _ := m.capacityDBU(c)
	if m.quotaPct <= 0 {
		return metal, true
	}
	return metal * m.quotaPct / 100.0, true
}

// QuotaPct returns the configured share of the metal repman may allocate (0 = unset,
// meaning the full metal is usable). From resource-manager-infra-quota-pct.
func (m *ResourceManager) QuotaPct() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.quotaPct
}

// CapacityDBUView projects a raw infra capacity (native units, already assembled by the
// caller from summed agents + config overrides) into DBU per axis, and returns the
// BINDING axis = the SCARCEST one (min) -- capacity is bounded by its smallest axis,
// unlike consumed which pivots on the largest. An axis with ratio 0 or value 0 is
// excluded from the binding. Exported so the global GUI can pass an infra-wide total,
// not just a stored per-agent AgentCapacity.
func (m *ResourceManager) CapacityDBUView(c AgentCapacity) (cpu, mem, io, disk, binding float64, bindingAxis string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := m.ratios[ProfileDatabase]
	cpu = unitDiv(c.Cores, r.CoresPerUnit)
	mem = unitDiv(c.MemMB, r.MemMBPerUnit)
	io = unitDiv(c.Iops, r.IopsPerUnit)
	disk = unitDiv(c.DiskGB, r.DiskGBPerUnit)
	binding = -1
	consider := func(v, ratio float64, axis string) {
		if ratio > 0 && v > 0 && (binding < 0 || v < binding) {
			binding, bindingAxis = v, axis
		}
	}
	consider(cpu, r.CoresPerUnit, "cpu")
	consider(mem, r.MemMBPerUnit, "mem")
	consider(io, r.IopsPerUnit, "io")
	consider(disk, r.DiskGBPerUnit, "disk")
	if binding < 0 {
		binding = 0
	}
	return
}

// ConsumedInfra sums the real consumed DBU across EVERY server the manager knows (all
// clusters) -- the infra-wide "how full are we" in DBU, per axis + the binding pivot.
func (m *ResourceManager) ConsumedInfra() DBUAggregate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	readings := make([]*DBUReading, 0, len(m.consumed))
	for _, r := range m.consumed {
		readings = append(readings, r)
	}
	return sumReadings(readings)
}
