package server

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

type CloudUserForm struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Roles    string `json:"roles"`
	Grants   string `json:"grants"`
	Reason   string `json:"reason"`
}

func (repman *ReplicationManager) AcceptSubscription(userform cluster.UserForm, cl *cluster.Cluster) error {
	user := userform.Username
	auser, ok := cl.APIUsers[user]
	if !ok {
		return fmt.Errorf("User %s does not exist ", user)
	}

	if v, ok := auser.Roles[config.RolePending]; !ok || !v {
		return fmt.Errorf("User %s does not have 'pending' role", user)
	}

	grants := strings.Split("db show proxy grant extrole sales-unsubscribe", " ")
	roles := strings.Split("sponsor", " ")
	for grant, v := range auser.Grants {
		if v {
			grants = append(grants, grant)
		}
	}
	userform.Grants = strings.Join(grants, " ")

	for role, v := range auser.Roles {
		if v && role != "pending" {
			roles = append(roles, role)
		}
	}
	userform.Roles = strings.Join(roles, " ")

	// If external sysops different from cloud18 git user
	if cl.Conf.Cloud18ExternalSysOps != "" && cl.Conf.Cloud18ExternalSysOps != cl.Conf.Cloud18GitUser {
		esys := repman.CreateExtSysopsForm(cl.Conf.Cloud18ExternalSysOps)
		if euser, ok := cl.APIUsers[cl.Conf.Cloud18ExternalSysOps]; !ok {
			cl.AddUser(esys, cl.Conf.Cloud18GitUser, false)
		} else {
			esys.Grants = cl.AppendGrants(esys.Grants, &euser)
			esys.Roles = cl.AppendRoles(esys.Roles, &euser)
			cl.UpdateUser(esys, cl.Conf.Cloud18GitUser, false)
		}
	}

	// If external dbops different from cloud18 dbops
	if cl.Conf.Cloud18ExternalDbOps != "" && cl.Conf.Cloud18ExternalDbOps != cl.Conf.Cloud18DbOps {
		edbops := repman.CreateExtDBOpsForm(cl.Conf.Cloud18ExternalDbOps)
		if edbuser, ok := cl.APIUsers[cl.Conf.Cloud18ExternalDbOps]; !ok {
			cl.AddUser(edbops, cl.Conf.Cloud18GitUser, false)
		} else {
			edbops.Grants = cl.AppendGrants(edbops.Grants, &edbuser)
			edbops.Roles = cl.AppendRoles(edbops.Roles, &edbuser)
			cl.UpdateUser(edbops, cl.Conf.Cloud18GitUser, false)
		}
	}

	new_acls := make([]string, 0)

	acls := strings.Split(cl.Conf.APIUsersACLAllowExternal, ",")
	for _, acl := range acls {
		useracl, listgrants, _, listroles := misc.SplitAcls(acl)
		// log.Printf("ACL: %s", acl)
		if useracl == user {
			acl = useracl + ":" + userform.Grants + ":" + cl.Name
			if userform.Roles != "" {
				acl = acl + ":" + userform.Roles
			}
			new_acls = append(new_acls, acl)
		} else {
			old_roles := strings.Split(listroles, " ")
			new_roles := make([]string, 0)
			for _, role := range old_roles {
				if role == "pending" {
					continue
				}
				new_roles = append(new_roles, role)
			}
			acl = useracl + ":" + listgrants + ":" + cl.Name
			if len(new_roles) > 0 {
				acl = acl + ":" + strings.Join(new_roles, " ")
			}
			new_acls = append(new_acls, acl)
		}
		// log.Printf("New ACL: %s", acl)
	}

	cl.Conf.APIUsersACLAllowExternal = strings.Join(new_acls, ",")
	// log.Printf("APIUsersACLAllowExternal: %s", cl.Conf.APIUsersACLAllowExternal)

	cl.LoadAPIUsers()
	cl.SaveAcls()
	cl.Save()

	return nil
}

func (repman *ReplicationManager) CancelSubscription(userform cluster.UserForm, cl *cluster.Cluster) error {
	user := userform.Username
	auser, ok := cl.APIUsers[user]
	if !ok {
		return fmt.Errorf("User %s does not exist ", user)
	}
	grants := make([]string, 0)
	roles := make([]string, 0)
	for grant, v := range auser.Grants {
		if v {
			grants = append(grants, grant)
		}
	}
	userform.Grants = strings.Join(grants, " ")

	for role, v := range auser.Roles {
		if v && role != "pending" {
			roles = append(roles, role)
		}
	}

	userform.Roles = strings.Join(roles, " ")

	if userform.Grants == "" {
		cl.DropUser(userform, true)
	} else {
		cl.UpdateUser(userform, "admin", true)
	}

	return nil
}

func (repman *ReplicationManager) EndSubscription(userform cluster.UserForm, cl *cluster.Cluster) error {
	user := userform.Username
	auser, ok := cl.APIUsers[user]

	if !ok {
		return fmt.Errorf("User %s does not exist ", user)
	}

	if v, ok := auser.Roles[config.RoleSponsor]; !ok || !v {
		return fmt.Errorf("User %s does not have 'sponsor' role", user)
	}

	grants := make([]string, 0)
	roles := make([]string, 0)
	for grant, v := range auser.Grants {
		if v {
			grants = append(grants, grant)
		}
	}
	userform.Grants = strings.Join(grants, " ")

	for role, v := range auser.Roles {
		if v && role != "sponsor" {
			roles = append(roles, role)
		}
	}

	// If use has no other roles, remove grants
	if len(roles) == 0 {
		userform.Grants = ""
	}

	roles = append(roles, config.RoleUnsubscribed)

	userform.Roles = strings.Join(roles, " ")

	cl.UpdateUser(userform, "admin", true)

	return nil
}

func (repman *ReplicationManager) BashScriptSalesSubscribe(mycluster *cluster.Cluster, subscriber string) error {
	if repman.Conf.Cloud18SalesSubscriptionScript != "" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling cluster subscription script")
		var out []byte
		out, err := exec.Command(repman.Conf.Cloud18SalesSubscriptionScript, mycluster.Name, subscriber).CombinedOutput()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cluster subscription script complete %s:", string(out))
	}
	return nil
}

func (repman *ReplicationManager) BashScriptSalesSubscriptionValidate(mycluster *cluster.Cluster, subscriber, operator string) error {
	if repman.Conf.Cloud18SalesSubscriptionValidateScript != "" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling cluster subscription script")
		var out []byte
		out, err := exec.Command(repman.Conf.Cloud18SalesSubscriptionValidateScript, mycluster.Name, subscriber, operator).CombinedOutput()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cluster subscription script complete %s:", string(out))
	}
	return nil
}

func (repman *ReplicationManager) BashScriptSalesUnsubscribe(mycluster *cluster.Cluster, subscriber, operator string) error {
	if repman.Conf.Cloud18SalesUnsubscribeScript != "" {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling cluster unsubscription script")
		var out []byte
		out, err := exec.Command(repman.Conf.Cloud18SalesUnsubscribeScript, mycluster.Name, subscriber, operator).CombinedOutput()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cluster unsubscription script complete %s:", string(out))
	}
	return nil
}

func (repman *ReplicationManager) SetCloudPartner() {
	selectedPartner := config.Partner{
		Name:        "Signal18",
		IsDbops:     1,
		IsSysops:    1,
		DbopsEmail:  "dbops-cloud18@signal18.io",
		SysopsEmail: "sysops-cloud18@signal18.io",
	}

	for _, partner := range repman.Partners {
		if repman.Conf.Cloud18GitUser == partner.SysopsEmail {
			selectedPartner = partner
			break
		}
	}

	repman.Partner = selectedPartner
}

func (repman *ReplicationManager) GetPartnerByMail(email string) (config.Partner, bool) {
	for _, partner := range repman.Partners {
		if partner.SysopsEmail == email || partner.DbopsEmail == email {
			return partner, true
		}
	}

	return config.Partner{}, false
}

func (repman *ReplicationManager) BashScriptExternalOpsValidate(mycluster *cluster.Cluster, subscriber, role, operator string) error {
	if repman.Conf.Cloud18SalesExternalOpsValidateScript != "" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling script after external ops validation")
		var out []byte
		out, err := exec.Command(repman.Conf.Cloud18SalesExternalOpsValidateScript, mycluster.Name, subscriber, role, operator).CombinedOutput()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cluster external ops validation script complete %s:", string(out))
	}
	return nil
}

func (repman *ReplicationManager) BashScriptExternalOpsEndPartnership(mycluster *cluster.Cluster, subscriber, role, operator string) error {
	if repman.Conf.Cloud18SalesExternalOpsStopScript != "" {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling script after partnership with external ops end")
		var out []byte
		out, err := exec.Command(repman.Conf.Cloud18SalesExternalOpsStopScript, mycluster.Name, subscriber, role, operator).CombinedOutput()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Partnership end script %s:", string(out))
	}
	return nil
}

func (repman *ReplicationManager) AcceptExternalOps(userform CloudUserForm, cl *cluster.Cluster, delegator string) error {
	var extops cluster.UserForm
	user := userform.Username
	auser, ok := cl.APIUsers[user]
	if !ok {
		return fmt.Errorf("User %s does not exist ", user)
	}

	if userform.Roles == config.RoleExtDBOps {
		if v, ok := auser.Roles[config.RolePendingExtDBOps]; !ok || !v {
			return fmt.Errorf("User %s does not have '%s' role", user, config.RolePendingExtDBOps)
		}

		extops = repman.CreateExtDBOpsForm(user)
		auser.Roles[config.RolePendingExtDBOps] = false
	} else if userform.Roles == config.RoleExtSysOps {
		if v, ok := auser.Roles[config.RolePendingExtSysOps]; !ok || !v {
			return fmt.Errorf("User %s does not have '%s' role", user, config.RolePendingExtSysOps)
		}

		extops = repman.CreateExtSysopsForm(user)
		auser.Roles[config.RolePendingExtSysOps] = false
	} else {
		return fmt.Errorf("Invalid role %s", userform.Roles)
	}

	extops.Grants = cl.AppendGrants(extops.Grants, &auser)
	extops.Roles = cl.AppendRoles(extops.Roles, &auser)
	cl.UpdateUser(extops, delegator, true)

	return nil
}

func (repman *ReplicationManager) CancelExternalOps(userform CloudUserForm, cl *cluster.Cluster) error {
	var extops cluster.UserForm = cluster.UserForm{
		Username: userform.Username,
		Roles:    userform.Roles,
		Grants:   userform.Grants,
	}
	user := userform.Username
	cancelrole := userform.Roles

	auser, ok := cl.APIUsers[user]
	if !ok {
		return fmt.Errorf("User %s does not exist ", user)
	}
	grants := make([]string, 0)
	roles := make([]string, 0)
	for grant, v := range auser.Grants {
		if v {
			grants = append(grants, grant)
		}
	}
	extops.Grants = strings.Join(grants, " ")

	for role, v := range auser.Roles {
		if v && role != cancelrole {
			roles = append(roles, role)
		}
	}

	extops.Roles = strings.Join(roles, " ")

	if extops.Grants == "" {
		cl.DropUser(extops, true)
	} else {
		cl.UpdateUser(extops, "admin", true)
	}

	return nil
}

func (repman *ReplicationManager) EndExternalOps(userform CloudUserForm, cl *cluster.Cluster) error {
	var extops cluster.UserForm = cluster.UserForm{
		Username: userform.Username,
		Roles:    userform.Roles,
		Grants:   userform.Grants,
	}
	user := userform.Username
	endrole := userform.Roles

	auser, ok := cl.APIUsers[user]

	if !ok {
		return fmt.Errorf("User %s does not exist ", user)
	}

	if v, ok := auser.Roles[endrole]; !ok || !v {
		return fmt.Errorf("User %s does not have '%s' role", user, endrole)
	}

	grants := make([]string, 0)
	roles := make([]string, 0)
	for grant, v := range auser.Grants {
		if v {
			grants = append(grants, grant)
		}
	}
	extops.Grants = strings.Join(grants, " ")

	for role, v := range auser.Roles {
		if v && role != endrole {
			roles = append(roles, role)
		}
	}

	// If use has no other roles, remove grants
	if len(roles) == 0 {
		extops.Grants = ""
	}

	if endrole == config.RoleExtDBOps {
		roles = append(roles, config.RoleUnsubscribedExtDBOps)
	} else if endrole == config.RoleExtSysOps {
		roles = append(roles, config.RoleUnsubscribedExtSysOps)
	} else {
		return fmt.Errorf("Invalid role %s", endrole)
	}

	extops.Roles = strings.Join(roles, " ")

	cl.UpdateUser(extops, "admin", true)

	return nil
}

func (repman *ReplicationManager) RegisterExternalOps(userform CloudUserForm, mycluster *cluster.Cluster, delegator string) error {
	extops := cluster.UserForm{
		Username: userform.Username,
	}

	auser, exists := mycluster.APIUsers[userform.Username]

	if userform.Roles == config.RoleExtDBOps {
		if mycluster.Conf.Cloud18ExternalDbOps != "" {
			return fmt.Errorf("External DBOps already registered")
		}
		extops.Roles = config.RolePendingExtDBOps

		if exists {
			extops.Grants = mycluster.AppendGrants(extops.Grants, &auser)
			extops.Roles = mycluster.AppendRoles(extops.Roles, &auser)
			err := mycluster.UpdateUser(extops, delegator, true)
			if err != nil {
				return err
			}
		} else {
			err := mycluster.AddUser(extops, delegator, true)
			if err != nil {
				return err
			}
		}

		mycluster.Conf.Cloud18ExternalDbOps = userform.Username
		mycluster.Conf.Cloud18ExternalDbOpsStatus = config.ExternalPending
	} else if userform.Roles == config.RoleExtSysOps {
		if mycluster.Conf.Cloud18ExternalSysOps != "" {
			return fmt.Errorf("External SysOps already registered")
		}
		extops.Roles = config.RolePendingExtSysOps

		if exists {
			extops.Grants = mycluster.AppendGrants(extops.Grants, &auser)
			extops.Roles = mycluster.AppendRoles(extops.Roles, &auser)
			mycluster.UpdateUser(extops, delegator, true)
			err := mycluster.UpdateUser(extops, delegator, true)
			if err != nil {
				return err
			}
		} else {
			err := mycluster.AddUser(extops, delegator, true)
			if err != nil {
				return err
			}
		}

		mycluster.Conf.Cloud18ExternalSysOps = userform.Username
		mycluster.Conf.Cloud18ExternalSysOpsStatus = config.ExternalPending
	} else {
		return fmt.Errorf("Invalid role %s", userform.Roles)
	}

	return nil
}
