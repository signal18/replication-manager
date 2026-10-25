// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	repmanmcp "github.com/signal18/replication-manager/mcp"
	"github.com/signal18/replication-manager/peer"
)

// Cloud18 for the MCP server (issue #1838): the same registration, subscription
// and marketplace operations as the REST /api/register* and /api/clusters/for-sale
// endpoints, exposed to an assistant. Authorization is the caller's global-admin-show
// grant for actions (AuthorizeMCPGlobal), any authenticated principal for reads.
//
// The GitLab password of a registration started from MCP is generated here and
// never leaves the server: it is stored in the config on success exactly as the
// REST flow does (cloud18-gitlab-password), the person resets it on
// gitlab.signal18.io if they ever need to log in there interactively.

// AuthorizeMCPGlobal reports whether the principal holds a grant on at least one
// cluster, token narrowing and scope applied (a token needs the "*" scope for
// repman-global operations). An empty grant means any authenticated principal.
func (repman *ReplicationManager) AuthorizeMCPGlobal(p *repmanmcp.Principal, grant string) bool {
	if p == nil {
		return false
	}
	if grant == "" {
		return true
	}
	ok := false
	switch p.AuthMethod {
	case "token":
		t, isToken := p.Auth.(*APIToken)
		if !isToken || t == nil || !t.IsGlobal() {
			break
		}
		for _, cl := range repman.Clusters {
			if u, found := cl.GetACLUser(repman.tokenPrincipalFor(t, cl)); found && u.Grants[grant] {
				ok = true
				break
			}
		}
	default:
		for _, cl := range repman.Clusters {
			if u, found := cl.APIUsers[p.User]; found && u.Grants[grant] {
				ok = true
				break
			}
		}
	}
	if !ok {
		repman.logSecurityEvent("mcp_denied", p.User, p.Remote, fmt.Sprintf("MCP %s denied: needs %s (global)", p.String(), grant))
	}
	return ok
}

// Cloud18Status describes the instance's Cloud18 connection.
func (repman *ReplicationManager) Cloud18Status() map[string]any {
	state, message, _ := repman.RegStatus.snapshot()
	return map[string]any{
		"registered":        repman.Conf.Cloud18,
		"uri":               repman.registeredInstanceURI(),
		"domain":            repman.Conf.Cloud18Domain,
		"subDomain":         repman.Conf.Cloud18SubDomain,
		"zone":              repman.Conf.Cloud18SubDomainZone,
		"gitlabUser":        repman.Conf.Cloud18GitUser,
		"subscriptionPlan":  repman.Conf.Cloud18SubscriptionPlan,
		"crmApi":            repman.crmBase(),
		"registrationState": state,
		"registrationMsg":   message,
		"peersDisabled":     repman.Conf.Cloud18DisablePeers,
		"forSaleDisabled":   repman.Conf.Cloud18DisableForSale,
	}
}

// Cloud18RegisterStatus returns the registration state machine.
func (repman *ReplicationManager) Cloud18RegisterStatus() map[string]any {
	state, message, result := repman.RegStatus.snapshot()
	out := map[string]any{"state": state, "message": message}
	if len(result) > 0 {
		out["result"] = json.RawMessage(result)
	}
	return out
}

// errCloud18RegisterNeedsPassword: registration through Cloud18 (#2159) needs the person's Cloud18
// password, which an assistant must never handle.
var errCloud18RegisterNeedsPassword = errors.New("registering to Cloud18 needs your Cloud18 password: use Register in the GUI (Cloud18 settings) or 'replication-manager-cli register --uri <domain.subdomain.zone> --email <you>'; an assistant never handles that password")

// Cloud18Register is not done by the assistant: it says where to register (no CRM, no generated
// password any more).
func (repman *ReplicationManager) Cloud18Register(email, uri string) (map[string]any, error) {
	if repman.Conf.Cloud18 {
		return nil, fmt.Errorf("this instance is already registered as %s", repman.registeredInstanceURI())
	}
	if _, _, _, err := parseCloud18URI(uri); err != nil {
		return nil, err
	}
	return nil, errCloud18RegisterNeedsPassword
}

// Cloud18RegisterConfirm: there is no confirm step any more (#2159).
func (repman *ReplicationManager) Cloud18RegisterConfirm(email, uri string) (map[string]any, error) {
	return nil, errCloud18RegisterNeedsPassword
}

// Cloud18Unregister requests the unregistration (declarative, #2159): pushed for the back office,
// then Cloud18 stops on the instance.
func (repman *ReplicationManager) Cloud18Unregister() (map[string]any, error) {
	out, err := repman.cloud18RequestUnregister()
	if err != nil {
		return nil, err
	}
	return toAnyMap(out), nil
}

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Cloud18SubscriptionPlans lists the instance plans offered by the CRM.
func (repman *ReplicationManager) Cloud18SubscriptionPlans() (json.RawMessage, error) {
	status, body, err := crmGetPlans(repman.crmBase())
	if err != nil {
		return nil, fmt.Errorf("CRM unreachable: %s", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("CRM answered HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}
	return json.RawMessage(rawOrNull(body)), nil
}

// Cloud18Subscription returns the requested plan and the back office's answer (#2159).
func (repman *ReplicationManager) Cloud18Subscription() (json.RawMessage, error) {
	if !repman.Conf.Cloud18 {
		return nil, errCloud18NotRegistered
	}
	repman.readCloud18PullState()
	b, err := json.Marshal(repman.cloud18SubscriptionView())
	return json.RawMessage(b), err
}

// Cloud18ChangeSubscription requests a plan (declarative, #2159): the back office applies it.
func (repman *ReplicationManager) Cloud18ChangeSubscription(plan string) (map[string]any, error) {
	out, err := repman.cloud18RequestPlan(plan)
	if err != nil {
		return nil, err
	}
	return toAnyMap(out), nil
}

// Cloud18ClustersForSale returns the marketplace catalogue as the REST endpoint does.
func (repman *ReplicationManager) Cloud18ClustersForSale() ([]*peer.PeerCluster, error) {
	if repman.PeerManager == nil {
		return nil, errors.New("peer manager not started (not registered to Cloud18?)")
	}
	plan := repman.Conf.Cloud18SubscriptionPlan
	if repman.Conf.Cloud18DisableForSale && plan != "" && plan != "free" {
		return []*peer.PeerCluster{}, nil
	}
	raw, err := repman.PeerManager.GetSaleClustersJSON()
	if err != nil {
		return nil, err
	}
	var list []*peer.PeerCluster
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// Cloud18Infrastructures groups the clusters for sale by their api-public-url.
func (repman *ReplicationManager) Cloud18Infrastructures() ([]repmanmcp.Cloud18Infrastructure, error) {
	list, err := repman.Cloud18ClustersForSale()
	if err != nil {
		return nil, err
	}
	byURL := map[string]*repmanmcp.Cloud18Infrastructure{}
	for _, c := range list {
		if c == nil || c.ApiPublicUrl == "" {
			continue
		}
		infra, ok := byURL[c.ApiPublicUrl]
		if !ok {
			infra = &repmanmcp.Cloud18Infrastructure{ApiPublicUrl: c.ApiPublicUrl}
			byURL[c.ApiPublicUrl] = infra
		}
		infra.Clusters++
		infra.ClusterNames = append(infra.ClusterNames, c.ClusterName)
		infra.Orchestrators = appendUnique(infra.Orchestrators, c.ProvOrchestrator)
		infra.ServicePlans = appendUnique(infra.ServicePlans, c.ProvServicePlan)
	}
	out := make([]repmanmcp.Cloud18Infrastructure, 0, len(byURL))
	for _, infra := range byURL {
		sort.Strings(infra.ClusterNames)
		out = append(out, *infra)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ApiPublicUrl < out[j].ApiPublicUrl })
	return out, nil
}

func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func rawOrNull(b []byte) []byte {
	if len(b) == 0 || !json.Valid(b) {
		q, _ := json.Marshal(strings.TrimSpace(string(b)))
		return q
	}
	return b
}
