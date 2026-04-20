package opensvc

import (
	apiv3 "github.com/opensvc/om3/v3/daemon/api"
)

// InstanceActionParams combines all parameters from the various PostInstanceAction* parameter structs.
type InstanceActionParams struct {
	Slaves          *apiv3.InQueryAllSlaves       `form:"slaves,omitempty" json:"slaves,omitempty"`
	Master          *apiv3.InQueryMaster          `form:"master,omitempty" json:"master,omitempty"`
	RequesterSid    *apiv3.InQueryRequesterSid    `form:"requester_sid,omitempty" json:"requester_sid,omitempty"`
	Rid             *apiv3.InQueryRid             `form:"rid,omitempty" json:"rid,omitempty"`
	Slave           *apiv3.InQuerySlaves          `form:"slave,omitempty" json:"slave,omitempty"`
	Subset          *apiv3.InQuerySubset          `form:"subset,omitempty" json:"subset,omitempty"`
	Tag             *apiv3.InQueryTag             `form:"tag,omitempty" json:"tag,omitempty"`
	To              *apiv3.InQueryTo              `form:"to,omitempty" json:"to,omitempty"`
	DisableRollback *apiv3.InQueryDisableRollback `form:"disable_rollback,omitempty" json:"disable_rollback,omitempty"`
	Force           *apiv3.InQueryForce           `form:"force,omitempty" json:"force,omitempty"`
	Leader          *apiv3.InQueryLeader          `form:"leader,omitempty" json:"leader,omitempty"`
	StateOnly       *apiv3.InQueryStateOnly       `form:"state_only,omitempty" json:"state_only,omitempty"`
	Confirm         *apiv3.InQueryConfirm         `form:"confirm,omitempty" json:"confirm,omitempty"`
	Cron            *apiv3.InQueryCron            `form:"cron,omitempty" json:"cron,omitempty"`
	Env             *apiv3.InQueryEnvs            `form:"env,omitempty" json:"env,omitempty"`
	MoveTo          *apiv3.InQueryMoveTo          `form:"move-to,omitempty" json:"move-to,omitempty"`
}

func (ap *InstanceActionParams) ToBootParams() *apiv3.PostInstanceActionBootParams {
	return &apiv3.PostInstanceActionBootParams{
		Slaves:       ap.Slaves,
		Master:       ap.Master,
		RequesterSid: ap.RequesterSid,
		Rid:          ap.Rid,
		Slave:        ap.Slave,
		Subset:       ap.Subset,
		Tag:          ap.Tag,
		To:           ap.To,
	}
}

func (ap *InstanceActionParams) ToDeleteParams() *apiv3.PostInstanceActionDeleteParams {
	return &apiv3.PostInstanceActionDeleteParams{
		RequesterSid: ap.RequesterSid,
	}
}

func (ap *InstanceActionParams) ToFreezeParams() *apiv3.PostInstanceActionFreezeParams {
	return &apiv3.PostInstanceActionFreezeParams{
		Slaves:       ap.Slaves,
		Master:       ap.Master,
		Slave:        ap.Slave,
		RequesterSid: ap.RequesterSid,
	}
}

func (ap *InstanceActionParams) ToProvisionParams() *apiv3.PostInstanceActionProvisionParams {
	return &apiv3.PostInstanceActionProvisionParams{
		Slaves:          ap.Slaves,
		DisableRollback: ap.DisableRollback,
		Force:           ap.Force,
		Leader:          ap.Leader,
		Master:          ap.Master,
		RequesterSid:    ap.RequesterSid,
		Rid:             ap.Rid,
		Slave:           ap.Slave,
		StateOnly:       ap.StateOnly,
		Subset:          ap.Subset,
		Tag:             ap.Tag,
		To:              ap.To,
	}
}
func (ap *InstanceActionParams) ToPRStartParams() *apiv3.PostInstanceActionPRStartParams {
	return &apiv3.PostInstanceActionPRStartParams{
		Slaves:          ap.Slaves,
		DisableRollback: ap.DisableRollback,
		Force:           ap.Force,
		Master:          ap.Master,
		RequesterSid:    ap.RequesterSid,
		Rid:             ap.Rid,
		Slave:           ap.Slave,
		Subset:          ap.Subset,
		Tag:             ap.Tag,
		To:              ap.To,
	}
}
func (ap *InstanceActionParams) ToPRStopParams() *apiv3.PostInstanceActionPRStopParams {
	return &apiv3.PostInstanceActionPRStopParams{
		Slaves:          ap.Slaves,
		DisableRollback: ap.DisableRollback,
		Force:           ap.Force,
		Master:          ap.Master,
		RequesterSid:    ap.RequesterSid,
		Rid:             ap.Rid,
		Slave:           ap.Slave,
		Subset:          ap.Subset,
		Tag:             ap.Tag,
		To:              ap.To,
	}
}
func (ap *InstanceActionParams) ToPushResourceInfoParams() *apiv3.PostInstanceActionPushResourceInfoParams {
	return &apiv3.PostInstanceActionPushResourceInfoParams{
		RequesterSid: ap.RequesterSid,
	}
}
func (ap *InstanceActionParams) ToStartParams() *apiv3.PostInstanceActionStartParams {
	return &apiv3.PostInstanceActionStartParams{
		Slaves:          ap.Slaves,
		DisableRollback: ap.DisableRollback,
		Force:           ap.Force,
		Master:          ap.Master,
		RequesterSid:    ap.RequesterSid,
		Rid:             ap.Rid,
		Slave:           ap.Slave,
		Subset:          ap.Subset,
		Tag:             ap.Tag,
		To:              ap.To,
	}
}

func (ap *InstanceActionParams) ToRestartParams() *apiv3.PostInstanceActionRestartParams {
	return &apiv3.PostInstanceActionRestartParams{
		Slaves:          ap.Slaves,
		DisableRollback: ap.DisableRollback,
		Force:           ap.Force,
		Master:          ap.Master,
		RequesterSid:    ap.RequesterSid,
		Rid:             ap.Rid,
		Slave:           ap.Slave,
		Subset:          ap.Subset,
		Tag:             ap.Tag,
		To:              ap.To,
	}
}

func (ap *InstanceActionParams) ToStopParams() *apiv3.PostInstanceActionStopParams {
	return &apiv3.PostInstanceActionStopParams{
		Slaves:       ap.Slaves,
		Force:        ap.Force,
		Master:       ap.Master,
		MoveTo:       ap.MoveTo,
		RequesterSid: ap.RequesterSid,
		Rid:          ap.Rid,
		Slave:        ap.Slave,
		Subset:       ap.Subset,
		Tag:          ap.Tag,
		To:           ap.To,
	}
}
func (ap *InstanceActionParams) ToSyncIngestParams() *apiv3.PostInstanceActionSyncIngestParams {
	return &apiv3.PostInstanceActionSyncIngestParams{
		RequesterSid: ap.RequesterSid,
		Rid:          ap.Rid,
		Subset:       ap.Subset,
		Tag:          ap.Tag,
	}
}
func (ap *InstanceActionParams) ToUnfreezeParams() *apiv3.PostInstanceActionUnfreezeParams {
	return &apiv3.PostInstanceActionUnfreezeParams{
		Slaves:       ap.Slaves,
		Master:       ap.Master,
		RequesterSid: ap.RequesterSid,
		Slave:        ap.Slave,
	}
}
func (ap *InstanceActionParams) ToUnprovisionParams() *apiv3.PostInstanceActionUnprovisionParams {
	return &apiv3.PostInstanceActionUnprovisionParams{
		Slaves:       ap.Slaves,
		Force:        ap.Force,
		Leader:       ap.Leader,
		Master:       ap.Master,
		RequesterSid: ap.RequesterSid,
		Rid:          ap.Rid,
		StateOnly:    ap.StateOnly,
		Slave:        ap.Slave,
		Subset:       ap.Subset,
		Tag:          ap.Tag,
		To:           ap.To,
	}
}
