package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const sponsorshipStateFilename = "sponsorship-state.json"
const sponsorshipStateTempPattern = "sponsorship-state-*.json.tmp"

// SponsorshipStatus is the authoritative local sponsorship lifecycle status
// for a cluster. It is intentionally distinct from the implicit status
// derived from cl.APIUsers[user].Roles ("pending"/"sponsor"/"unsubscribed"),
// which remains the precondition gate for transitions in this phase.
type SponsorshipStatus string

const (
	SponsorshipStatusNone      SponsorshipStatus = "none"
	SponsorshipStatusRequested SponsorshipStatus = "requested"
	SponsorshipStatusActive    SponsorshipStatus = "active"
	SponsorshipStatusRejected  SponsorshipStatus = "rejected"
	SponsorshipStatusEnded     SponsorshipStatus = "ended"
)

// SponsorshipAuditSnapshot records who performed the last transition and who
// it was about. Subject and actor differ when, e.g., an admin ends a
// sponsor's subscription on their behalf.
type SponsorshipAuditSnapshot struct {
	SubjectUsername string    `json:"subjectUsername"`
	ActorUsername   string    `json:"actorUsername"`
	ActedAt         time.Time `json:"actedAt"`
}

// SponsorshipEventMeta is the deterministic local event metadata needed to
// later build CRM workflow/settlement events (occurred_at + event_key).
// EventKey and OccurredAt are never sent anywhere in this phase.
type SponsorshipEventMeta struct {
	EventType  string    `json:"eventType"`
	OccurredAt time.Time `json:"occurredAt"`
	EventKey   string    `json:"eventKey"`
}

// SponsorshipState is the per-cluster authoritative sponsorship record,
// durably persisted to sponsorship-state.json. See
// doc/implementation/server/CRM_SPONSORSHIP_USAGE_PREPARATION_PLAN.md.
type SponsorshipState struct {
	// ClusterRef is cluster.Name in this phase. Stable, rename-independent
	// ref minting is a later phase.
	ClusterRef string                   `json:"clusterRef"`
	Status     SponsorshipStatus        `json:"status"`
	Audit      SponsorshipAuditSnapshot `json:"audit"`

	// BillingOwnerRef and SponsorshipCycleRef are optional caches of
	// CRM-minted refs. They stay empty until a later phase resolves them.
	BillingOwnerRef     string `json:"billingOwnerRef,omitempty"`
	SponsorshipCycleRef string `json:"sponsorshipCycleRef,omitempty"`

	LastWorkflowEvent SponsorshipEventMeta `json:"lastWorkflowEvent"`
	// LastBillingEvent stays zero-value until app billing events are added.
	LastBillingEvent SponsorshipEventMeta `json:"lastBillingEvent"`

	// PricingMode is a snapshot of config.Cloud18MarketplacePricingMode at
	// transition time, kept for audit purposes.
	PricingMode string    `json:"pricingMode"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func sponsorshipStatePath(workingDir string) string {
	return workingDir + "/" + sponsorshipStateFilename
}

// LoadSponsorshipState reads sponsorship-state.json without mutating
// in-memory state. A missing file (first run, or a cluster that existed
// before this phase) is not an error: it returns SponsorshipStatusNone.
func (cluster *Cluster) LoadSponsorshipState() (SponsorshipState, error) {
	data, err := os.ReadFile(sponsorshipStatePath(cluster.WorkingDir))
	if err != nil {
		if os.IsNotExist(err) {
			return SponsorshipState{Status: SponsorshipStatusNone}, nil
		}
		return SponsorshipState{}, err
	}
	var st SponsorshipState
	if err := json.Unmarshal(data, &st); err != nil {
		return SponsorshipState{}, fmt.Errorf("sponsorship-state.json parse error: %w", err)
	}
	return st, nil
}

// saveSponsorshipStateLocked writes st atomically. Caller must hold
// sponsorshipStateMu.
func (cluster *Cluster) saveSponsorshipStateLocked(st SponsorshipState) error {
	payload, err := json.MarshalIndent(st, "", "\t")
	if err != nil {
		return err
	}
	return writeFileAtomic(sponsorshipStatePath(cluster.WorkingDir), payload, sponsorshipStateTempPattern)
}

// RestoreSponsorshipState loads sponsorship-state.json into memory. Call
// once during InitFromConf, before any sponsorship/runtime reconciliation.
func (cluster *Cluster) RestoreSponsorshipState() error {
	st, err := cluster.LoadSponsorshipState()
	if err != nil {
		st = SponsorshipState{Status: SponsorshipStatusNone}
	}
	cluster.sponsorshipStateMu.Lock()
	cluster.sponsorshipState = st
	cluster.sponsorshipStateMu.Unlock()
	return err
}

// GetSponsorshipState returns the current in-memory authoritative
// sponsorship state.
func (cluster *Cluster) GetSponsorshipState() SponsorshipState {
	cluster.sponsorshipStateMu.Lock()
	defer cluster.sponsorshipStateMu.Unlock()
	return cluster.sponsorshipState
}

// transitionSponsorship durably persists the new status before committing it
// to memory: a failed write leaves the in-memory state unchanged, so callers
// never observe or report success before the authoritative write completes.
func (cluster *Cluster) transitionSponsorship(newStatus SponsorshipStatus, eventType, subject, actor string) error {
	now := time.Now()
	cluster.sponsorshipStateMu.Lock()
	defer cluster.sponsorshipStateMu.Unlock()

	st := cluster.sponsorshipState
	st.ClusterRef = cluster.Name
	st.Status = newStatus
	st.Audit = SponsorshipAuditSnapshot{SubjectUsername: subject, ActorUsername: actor, ActedAt: now}
	st.PricingMode = cluster.Conf.Cloud18MarketplacePricingMode
	st.LastWorkflowEvent = SponsorshipEventMeta{
		EventType:  eventType,
		OccurredAt: now,
		EventKey:   fmt.Sprintf("%s|%s|%d", cluster.Name, eventType, now.UnixNano()),
	}
	st.UpdatedAt = now

	if err := cluster.saveSponsorshipStateLocked(st); err != nil {
		return fmt.Errorf("failed to persist sponsorship state: %w", err)
	}
	cluster.sponsorshipState = st
	return nil
}

// SetSponsorshipRequested records a sponsorship request (subscribe).
func (cluster *Cluster) SetSponsorshipRequested(subject, actor string) error {
	return cluster.transitionSponsorship(SponsorshipStatusRequested, "sponsorship_requested", subject, actor)
}

// SetSponsorshipActive records a sponsorship acceptance.
func (cluster *Cluster) SetSponsorshipActive(subject, actor string) error {
	return cluster.transitionSponsorship(SponsorshipStatusActive, "sponsorship_approved", subject, actor)
}

// SetSponsorshipRejected records a sponsorship rejection (terminal, no
// separate "cleared pending" status).
func (cluster *Cluster) SetSponsorshipRejected(subject, actor string) error {
	return cluster.transitionSponsorship(SponsorshipStatusRejected, "sponsorship_rejected", subject, actor)
}

// SetSponsorshipEnded records the end of an active sponsorship.
func (cluster *Cluster) SetSponsorshipEnded(subject, actor string) error {
	return cluster.transitionSponsorship(SponsorshipStatusEnded, "sponsorship_ended", subject, actor)
}

// applySponsorshipMirror copies the safe subset of sp into clsave for the
// clusterstate.json mirror. livePricingMode is the cluster's current
// Cloud18MarketplacePricingMode, used instead of sp.PricingMode (a
// transition-time snapshot) because pricing mode can change independent of
// sponsorship transitions. Deliberately excluded from the mirror:
// BillingOwnerRef, the full SponsorshipAuditSnapshot, and both EventKeys.
func applySponsorshipMirror(clsave *ClusterState, sp SponsorshipState, livePricingMode string) {
	clsave.SponsorshipStatus = sp.Status
	clsave.SponsorshipClusterRef = sp.ClusterRef
	clsave.SponsorshipCycleRef = sp.SponsorshipCycleRef
	clsave.SponsorshipPricingMode = livePricingMode
	clsave.SponsorshipLastEventType = sp.LastWorkflowEvent.EventType
	clsave.SponsorshipLastEventTime = sp.LastWorkflowEvent.OccurredAt
	clsave.SponsorshipLastBillingEventType = sp.LastBillingEvent.EventType
	clsave.SponsorshipLastBillingEventTime = sp.LastBillingEvent.OccurredAt
}
