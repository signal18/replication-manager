package cluster

import (
	"slices"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// The alert email goes to mail-to and to the users holding alert-mail-receiver; a user without
// it, or whose name is not an address, gets nothing (issue #2313).
func TestAlertRecipientsByGrant(t *testing.T) {
	c := newTestClusterForUsers(t)
	c.Conf.MailTo = "ops@example.com"
	c.APIUsers = map[string]APIUser{
		"alice@example.com": {User: "alice@example.com", Grants: map[string]bool{config.GrantAlertMailReceiver: true}},
		"bob@example.com":   {User: "bob@example.com", Grants: map[string]bool{config.GrantClusterSwitchover: true}},
		"admin":             {User: "admin", Grants: map[string]bool{config.GrantAlertMailReceiver: true}},
	}
	if got := c.GetAlertRecipients(AlertRecipient{To: c.Conf.MailTo, All: true}); got != "alice@example.com,ops@example.com" {
		t.Fatalf("recipients %q", got)
	}
	c.Conf.MailTo = ""
	if got := c.GetAlertRecipients(AlertRecipient{To: c.Conf.MailTo, All: true}); got != "alice@example.com" {
		t.Fatalf("a grant holder receives without mail-to: %q", got)
	}
}

// On Cloud18 an owner or ops address of the config that is a user of the cluster is decided by
// its grant; one that is not a user keeps receiving. A role flag still names its address.
func TestAlertRecipientsCloud18ConfigAddresses(t *testing.T) {
	c := newTestClusterForUsers(t)
	c.Conf.Cloud18 = true
	c.Conf.Cloud18GitUser = "owner@example.com"
	c.Conf.Cloud18DbOps = "dbops@example.com"
	c.APIUsers = map[string]APIUser{
		"dbops@example.com":   {User: "dbops@example.com", Grants: map[string]bool{}},
		"sponsor@example.com": {User: "sponsor@example.com", Grants: map[string]bool{}, Roles: map[string]bool{config.RoleSponsor: true}},
	}
	if got := c.GetAlertRecipients(AlertRecipient{All: true}); got != "owner@example.com" {
		t.Fatalf("alert recipients %q: the DbOps and the sponsor are users without the grant", got)
	}
	if got := c.GetAlertRecipients(AlertRecipient{DbOps: true, Sponsor: true}); got != "dbops@example.com,sponsor@example.com" {
		t.Fatalf("a message to the roles goes to them: %q", got)
	}
}

// Upgrade: a Cloud18 user who got the alerts through its role gets the grant at load; a removal
// is saved and survives the reloads; admin and a user without such a role do not get it.
func TestAlertMailReceiverUpgrade(t *testing.T) {
	c := newTestClusterForUsers(t)
	c.Conf.Cloud18 = true
	c.Conf.APIUsersACLAllow = "admin:cluster db proxy prov global grant show sale extrole terminal app token alert"
	c.Conf.Secrets.Set("api-credentials-external", config.Secret{Value: "sponsor@example.com:pw,viewer@example.com:pw"})
	c.Conf.APIUsersACLAllowExternal = "sponsor@example.com:db show proxy:c1:sponsor,viewer@example.com:show:c1"
	c.LoadAPIUsers()

	if !c.APIUsers["sponsor@example.com"].Grants[config.GrantAlertMailReceiver] {
		t.Fatal("the sponsor received the alerts by its role: it must get the grant")
	}
	if c.APIUsers["viewer@example.com"].Grants[config.GrantAlertMailReceiver] {
		t.Fatal("a user without an alerted role must not get the grant")
	}
	if !c.APIUsers["admin"].Grants[config.GrantAlertMailReceiver] {
		t.Fatal("admin's default ACL holds the alert family, to delegate it")
	}
	if got := c.GetAlertRecipients(AlertRecipient{All: true}); got != "sponsor@example.com" {
		t.Fatalf("admin is not an address: it never receives (%q)", got)
	}

	c.SaveAcls()
	allow, _ := c.SaveUserAcls("sponsor@example.com")
	if !slices.Contains(strings.Fields(allow), "alert") {
		t.Fatalf("the upgraded grant must be saved: %q", allow)
	}

	if err := c.UpdateUser(UserForm{Username: "sponsor@example.com", Roles: "sponsor", Grants: "db show proxy"}, "admin", false); err != nil {
		t.Fatal(err)
	}
	c.LoadAPIUsers() // the update's reload (reloadACL), without the config manager
	c.SaveAcls()
	if c.APIUsers["sponsor@example.com"].Grants[config.GrantAlertMailReceiver] {
		t.Fatal("a removal must hold after the update's reload")
	}
	c.LoadAPIUsers()
	if c.APIUsers["sponsor@example.com"].Grants[config.GrantAlertMailReceiver] {
		t.Fatal("a removal must hold after another reload")
	}

	c.Conf.Cloud18 = false
	c.Conf.Secrets.Set("api-credentials-external", config.Secret{Value: "sponsor@example.com:pw,other@example.com:pw"})
	c.Conf.APIUsersACLAllowExternal = "other@example.com:db:c1:sponsor"
	c.Conf.APIUsersACLDiscardExternal = ""
	c.LoadAPIUsers()
	if c.APIUsers["other@example.com"].Grants[config.GrantAlertMailReceiver] {
		t.Fatal("outside Cloud18 no role received the alerts: no upgrade")
	}
}

// A user added without the grant keeps it off at the reloads, even with an alerted role.
func TestAlertMailReceiverAddUserWithout(t *testing.T) {
	c := newTestClusterForUsers(t)
	c.Conf.Cloud18 = true
	c.LoadAPIUsers()
	if err := c.AddUser(UserForm{Username: "sponsor@example.com", Roles: "sponsor", Grants: "db show", Password: "pw"}, "admin", false); err != nil {
		t.Fatal(err)
	}
	c.LoadAPIUsers()
	if c.APIUsers["sponsor@example.com"].Grants[config.GrantAlertMailReceiver] {
		t.Fatal("added without the grant: no alert email")
	}
	if err := c.AddUser(UserForm{Username: "ops@example.com", Grants: "show alert", Password: "pw"}, "admin", false); err != nil {
		t.Fatal(err)
	}
	c.LoadAPIUsers()
	if !c.APIUsers["ops@example.com"].Grants[config.GrantAlertMailReceiver] {
		t.Fatal("added with the alert family: receives")
	}
}

// The role defaults that received the alerts include the grant.
func TestAlertMailReceiverRoleDefaults(t *testing.T) {
	for _, role := range []string{config.RoleSysOps, config.RoleDBOps, config.RoleExtSysOps, config.RoleExtDBOps, config.RoleSponsor} {
		if !slices.Contains(strings.Fields(config.GetDefaultGrants(role)), "alert") {
			t.Errorf("%s default grants %q lack the alert family", role, config.GetDefaultGrants(role))
		}
	}
	if slices.Contains(strings.Fields(config.GetDefaultGrants(config.RoleVisitor)), "alert") {
		t.Error("a visitor must not receive the alerts by default")
	}
}
