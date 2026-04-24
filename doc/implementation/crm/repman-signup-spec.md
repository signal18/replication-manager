# Replication Manager Register vs Signup Specification

Date: 2026-04-24
Owner: Replication Manager / Cloud18 integration
Related service: crm-api
Status: Draft implementation spec

## 1. Purpose

Replication Manager needs to keep two different flows separate:

1. **Register**: an administrator registers a Replication Manager instance to Signal18 Cloud18 so the instance can receive support, plugin access, GitLab configuration backup, subscription plan management, and other instance-level services.
2. **Signup**: an end user signs up or logs in through Signal18 SSO to enter an already-running Replication Manager instance.

These flows must use separate handlers, separate UI entry points, separate authorization rules, and separate tests.

## 2. Vocabulary

| Term | Meaning |
|---|---|
| Register | Admin-level instance registration. This connects one Replication Manager instance to Cloud18/CRM/GitLab. |
| Signup | End-user onboarding/login into an existing Replication Manager instance through Signal18 SSO. |
| Instance admin | The local Replication Manager admin user who manages global settings and registers the instance. |
| Instance user | A user who enters an existing Replication Manager instance. This can be an enterprise user, external bastion user, partner customer, or internal company user. |
| CRM | crm-api. Handles account/subscription/credit state. CRM does not handle Mattermost API. |
| GitLab account | Signal18 GitLab account used for SSO identity and/or Cloud18 integration depending on flow. |
| Mattermost | Mattermost API integration is outside CRM. It should be owned by Replication Manager, BO tooling, or a dedicated integration service. |

## 3. Current state in Replication Manager

The existing code already implements the **register** flow for admins.

Current backend behavior:

- `POST /api/register` is admin-only.
- It validates `email`, `password`, and `uri`.
- It splits `uri` into `domain.subdomain.zone`.
- It proxies to CRM `/api/register`.
- It sets a pending registration state.
- It starts a background polling loop that calls CRM `/api/register/confirm`.
- On confirmation success, it applies Cloud18 connection settings and runs the Cloud18/GitLab connect flow.

Current subscription behavior:

- `GET /api/register/subscription/plans` proxies CRM `/api/subscription/plans`.
- `GET /api/register/subscription` proxies CRM `/api/subscription` using the stored GitLab token.
- `POST /api/register/subscription` proxies CRM `/api/subscription` and persists the selected local `cloud18-subscription-plan` after CRM success.
- The current local valid plans are `free`, `support`, `support-services`, and `partner`.
- The `developer` plan is not yet accepted by backend validation.

Current frontend behavior:

- Cloud18 register UI lives in Admin / Global Settings / Cloud settings.
- The register modal collects email, GitLab password, domain, subdomain, and zone.
- The UI polls registration status while waiting for email confirmation.
- The subscription plan modal fetches plans from CRM, with a local fallback list.
- The fallback list currently contains `free`, `support`, `support-services`, and `partner`.
- The `developer` plan is not yet in the fallback list or help table.

## 4. Non-goals

- Do not merge signup into `/api/register`.
- Do not make CRM responsible for Mattermost API calls.
- Do not disable registration confirmation emails when adding the developer plan.
- Do not hardcode Cloud18 credit amounts inside Replication Manager.
- Do not give signup users admin/global registration privileges by default.

## 5. Flow A: Register existing admin instance flow

### 5.1 Purpose

The register flow is for the admin user who manages and registers a Replication Manager instance to get Cloud18 support, plugins, GitLab config backup, and instance subscription features.

### 5.2 Existing routes to keep

| Method | Route | Owner | Purpose |
|---|---|---|---|
| `POST` | `/api/register` | Replication Manager | Start admin instance registration through CRM. |
| `GET` | `/api/register/status` | Replication Manager | Poll current registration state. |
| `POST` | `/api/register/confirm` | Replication Manager | Manually complete confirmation and Cloud18 connect. |
| `POST` | `/api/register/unregister` | Replication Manager | Unregister instance and unlock URI fields. |
| `GET` | `/api/register/subscription/plans` | Replication Manager | Proxy CRM subscription plan catalog. |
| `GET` | `/api/register/subscription` | Replication Manager | Query current CRM subscription for registered instance. |
| `POST` | `/api/register/subscription` | Replication Manager | Change registered instance subscription plan. |

### 5.3 Register authorization

- All `/api/register/*` routes remain admin-only.
- Existing JWT admin check should remain required.
- Signup users must not be allowed to call admin register routes unless they are also local admins.

### 5.4 Register payload

Current admin registration payload from frontend to Replication Manager:

```json
{
  "email": "admin@example.com",
  "password": "secret-password",
  "uri": "company.datacenter.zone"
}
```

Replication Manager then sends CRM register payload:

```json
{
  "email": "admin@example.com",
  "password": "secret-password",
  "domain": "company",
  "subdomain": "datacenter",
  "zone": "zone"
}
```

### 5.5 Register success behavior

On successful CRM confirmation:

- Store Cloud18 domain, subdomain, zone, GitLab user, and encrypted GitLab password.
- Set `cloud18=true`.
- Run `InitGitConfig`.
- Update `RegStatus` to complete.

### 5.6 Register stays separate from signup

The admin register flow is **not** the flow for end users entering an existing instance.

Register creates or links the instance-level Cloud18/GitLab/CRM context. Signup creates or links a user identity for access to an existing instance.

## 6. Flow B: New signup flow for users entering existing instances

### 6.1 Purpose

The signup flow is for users who want to enter an already-running Replication Manager instance through Signal18 SSO.

This is new logic and should use a separate handler from register.

### 6.2 Business cases

#### 6.2.1 Enterprise bastion use case

An enterprise customer wants to expose a Replication Manager instance as a bastion server to:

- external users
- other users inside the same company

The instance owner/admin controls which users can access which clusters or resources.

#### 6.2.2 Partner resale use case

A partner wants to sell or expose database clusters to external customers who come to a partner-managed Replication Manager instance.

The partner/customer context must be preserved so access, billing, and credits can be handled correctly by CRM and the local instance.

### 6.3 Proposed signup routes

| Method | Route | Auth | Purpose |
|---|---|---|---|
| `POST` | `/api/signup` | Public or pre-auth, depending on instance policy | Start user signup through CRM/GitLab SSO. |
| `POST` | `/api/signup/confirm` | Public or pre-auth | Confirm user signup if CRM/GitLab requires confirmation. |
| `POST` | `/api/signup/login` | Public | Login existing SSO user into this RM instance. |
| `GET` | `/api/signup/status` | Signup session token or correlation id | Optional status polling for async signup confirmation. |

Route names can be adjusted, but they must remain separate from `/api/register/*`.

### 6.4 Signup request fields

Required fields:

- `email`
- `password`
- `username`

Optional fields:

- `telephone`
- `company`

Display/onboarding field:

- `gift_logo_text`

Instance context fields sent by Replication Manager, not trusted from the browser:

- `instance_uri`
- `instance_domain`
- `instance_subdomain`
- `instance_zone`
- `cloud18_subscription_plan`
- `signup_context`: `enterprise`, `partner`, or `direct`
- `partner_id` when applicable
- `enterprise_id` when applicable

Example frontend to Replication Manager payload:

```json
{
  "email": "user@example.com",
  "password": "secret-password",
  "username": "jdoe",
  "telephone": "+33123456789",
  "company": "Example Corp",
  "gift_logo_text": "Welcome to Cloud18"
}
```

Example Replication Manager to CRM payload:

```json
{
  "email": "user@example.com",
  "password": "secret-password",
  "username": "jdoe",
  "telephone": "+33123456789",
  "company": "Example Corp",
  "gift_logo_text": "Welcome to Cloud18",
  "instance_uri": "company.datacenter.zone",
  "instance_domain": "company",
  "instance_subdomain": "datacenter",
  "instance_zone": "zone",
  "signup_context": "enterprise",
  "partner_id": null,
  "enterprise_id": "company"
}
```

### 6.5 Signup CRM responsibility

CRM should:

- create or link the GitLab/SSO user account
- store user/account context
- associate the user with enterprise/partner/customer context
- calculate and grant free signup credits
- return credit grant information to Replication Manager
- ensure credit grant idempotency

CRM should not:

- call Mattermost API
- make local Replication Manager authorization decisions
- directly modify local RM users or cluster ACLs

### 6.6 Signup credits

CRM owns credit calculation and grant storage.

Default product rule:

> New signup users receive credits equal to 2 months of usage on the cheapest eligible Cloud18 cluster.

Replication Manager must not hardcode this amount. It should display or store the CRM-returned credit result only if needed.

Expected CRM response fragment:

```json
{
  "credit_grant": {
    "id": "credit-grant-id",
    "amount": 123.45,
    "currency": "EUR",
    "policy": "2_months_cheapest_eligible_cloud18_cluster",
    "already_granted": false
  }
}
```

### 6.7 Signup local user mapping

After CRM/GitLab signup or login succeeds, Replication Manager should create or map a local user.

Local mapping fields:

- CRM user id
- GitLab user id or username
- email
- username
- company
- telephone, optional
- signup context: enterprise, partner, direct
- partner/customer/enterprise identifiers when applicable
- local role
- allowed cluster/resource ACLs

The default role must be restrictive. Signup must not create admin users by default.

### 6.8 Signup authorization model

Possible local roles:

| Role | Meaning |
|---|---|
| `external_user` | External customer or temporary bastion user. Limited access. |
| `enterprise_user` | User belonging to enterprise tenant. Access determined by enterprise policy. |
| `partner_customer` | User of a partner-sold cluster. Access limited to assigned cluster/customer scope. |
| `readonly` | Read-only access to permitted resources. |
| `operator` | Operational access to permitted resources. |

Exact names can follow existing RM user model, but the important rule is that signup users must be scoped and non-admin by default.

## 7. Mattermost ownership

Mattermost API is **not** CRM responsibility.

If signup or register needs Mattermost provisioning, it should be handled by:

- Replication Manager, or
- BO tooling, or
- a dedicated integration service

Recommended split:

| Flow | Mattermost responsibility |
|---|---|
| Register | Instance/admin support channel or support access, if required. |
| Signup | End-user invite, channel membership, or user mapping, if required by enterprise/partner policy. |

Mattermost failures should not corrupt CRM state. If Mattermost provisioning is asynchronous, store a local provisioning status in RM or in the owning integration service.

## 8. Developer subscription plan

### 8.1 Goal

Add `developer` as a Cloud18 subscription plan so developer instances can avoid noisy alert mailers.

### 8.2 Backend changes

Add a new plan constant:

```go
const (
    SubPlanFree      = "free"
    SubPlanSupport   = "support"
    SubPlanServices  = "support-services"
    SubPlanPartner   = "partner"
    SubPlanDeveloper = "developer"
)
```

Update subscription validation:

```go
switch req.Plan {
case SubPlanFree, SubPlanSupport, SubPlanServices, SubPlanPartner, SubPlanDeveloper:
    // valid
}
```

Update error message to include `developer`.

### 8.3 Frontend changes

Update Cloud settings subscription fallback list:

```js
const PLANS_FALLBACK = [
  { value: 'free', label: 'Free', desc: 'Community access, config backup to GitLab, basic alerting' },
  { value: 'developer', label: 'Developer', desc: 'Development usage, alert mailers disabled by default' },
  { value: 'support', label: 'Contributing under Support', desc: 'Support ticket access, SLA, and community priority queue' },
  { value: 'support-services', label: 'Contributing under Support and Services', desc: 'Full support + managed DBA/SysOps services access' },
  { value: 'partner', label: 'Market Place Partner', desc: 'Marketplace listing, revenue sharing, and partner API access' },
]
```

Update the subscription help table to include Developer.

CRM-provided plan catalog must still take priority over the fallback list.

## 9. Alert mailer suppression for developer plan

### 9.1 Goal

When the registered instance is on the `developer` plan, alert mailers should be disabled to avoid development spam.

### 9.2 Scope

Disable alert mailers and external alert delivery for developer plan.

Do not disable:

- GitLab/CRM registration confirmation email
- signup confirmation email
- password reset email
- required security/identity email

### 9.3 Suggested helper

Add helper methods near config/plan logic:

```go
func (conf *Config) IsDeveloperPlan() bool {
    return conf.Cloud18SubscriptionPlan == "developer"
}

func (conf *Config) IsAlertMailerEnabledForPlan() bool {
    switch conf.Cloud18SubscriptionPlan {
    case "free", "developer":
        return false
    default:
        return true
    }
}
```

Then replace direct checks such as:

```go
cluster.Conf.Cloud18Alert && cluster.Conf.Cloud18SubscriptionPlan != "free"
```

with a named helper that also excludes `developer`.

### 9.4 Acceptance criteria

- `developer` plan suppresses alert mailers.
- `free` plan continues to suppress gated Cloud18 alert behavior.
- `support`, `support-services`, and `partner` keep existing alert mailer behavior.
- Registration/signup confirmation emails continue to work.

## 10. Frontend UI requirements

### 10.1 Register UI

Keep the existing register UI under Admin / Global Settings / Cloud settings.

Fields remain:

- email
- GitLab password
- domain
- subdomain
- zone

This UI must remain admin-only and must not be reused as the public signup UI.

### 10.2 Signup UI

Create a separate signup UI entry point for users entering an existing instance.

Fields:

- email, required
- password, required
- username, required
- telephone, optional
- company, optional
- gift logo text, optional/display

The signup UI must not show admin instance registration fields such as domain/subdomain/zone unless the instance owner explicitly exposes tenant context.

### 10.3 Login UI

Add or extend SSO login UI for existing users.

The login flow should call the new signup/login handler, not `/api/register`.

## 11. API response contracts

### 11.1 Signup success response

```json
{
  "status": "ok",
  "user": {
    "id": "local-user-id",
    "email": "user@example.com",
    "username": "jdoe",
    "role": "external_user"
  },
  "crm": {
    "user_id": "crm-user-id",
    "gitlab_username": "jdoe"
  },
  "credit_grant": {
    "id": "credit-grant-id",
    "amount": 123.45,
    "currency": "EUR",
    "policy": "2_months_cheapest_eligible_cloud18_cluster",
    "already_granted": false
  }
}
```

### 11.2 Signup pending confirmation response

```json
{
  "status": "pending_confirmation",
  "message": "Confirmation email sent",
  "correlation_id": "signup-correlation-id"
}
```

### 11.3 Signup error response

```json
{
  "error": "CRM signup failed",
  "detail": "user already exists"
}
```

## 12. Regtest requirements

### 12.1 Existing register flow regression

Add or keep tests proving:

- admin can call `/api/register`
- non-admin cannot call `/api/register`
- register proxies expected payload to CRM
- pending status is set after accepted CRM response
- confirm success applies Cloud18 connect state
- unregister calls CRM and clears local Cloud18 state

### 12.2 Developer plan regression

Test cases:

1. `POST /api/register/subscription` accepts `developer`.
2. Invalid plan is rejected.
3. CRM success persists local `cloud18-subscription-plan=developer`.
4. Developer plan disables alert mailer/external alert delivery.
5. Support-like plans still allow alert mailer behavior.

### 12.3 Signup regression

Test cases:

1. `/api/signup` accepts required fields: email, password, username.
2. telephone and company are optional.
3. Replication Manager sends instance context to CRM.
4. CRM credit result is returned or stored without recalculating locally.
5. Signup creates/maps a local non-admin user.
6. Duplicate signup does not duplicate credits.
7. Signup user cannot call admin register routes.
8. Enterprise context maps to enterprise user role/scope.
9. Partner context maps to partner/customer role/scope.

### 12.4 Mattermost regression

Only add Mattermost tests in Replication Manager or the service that owns Mattermost integration.

Do not add Mattermost expectations to CRM API tests.

## 13. Implementation order

### Phase 1: Developer plan in existing register/subscription flow

1. Add `developer` plan constant.
2. Accept `developer` in backend subscription validation.
3. Add `developer` to frontend fallback plan list and help table.
4. Add plan helper for alert mailer suppression.
5. Add regtests for developer plan and alert mailer suppression.

### Phase 2: Signup handler split

1. Add new request/response structs for signup.
2. Add new `/api/signup` handler.
3. Add CRM client helper for signup endpoint.
4. Add local user mapping after CRM signup/login success.
5. Add restrictive default roles/ACLs.
6. Add signup UI separate from Global Settings register UI.
7. Add regtests.

### Phase 3: Enterprise and partner workflows

1. Add signup context configuration: enterprise, partner, direct.
2. Add partner/customer/enterprise identifiers to CRM signup payload.
3. Add ACL mapping for enterprise bastion users.
4. Add ACL mapping for partner customer users.
5. Add optional Mattermost provisioning outside CRM, if required.

## 14. Open questions

1. Should `/api/signup` be public, invite-only, or controlled by instance config?
2. What exact local user table/model should store CRM/GitLab identity mapping?
3. What existing RM ACL model should be used for cluster scoping?
4. Should signup confirmation be async/polled like register, or synchronous through CRM?
5. Which service owns Mattermost API provisioning: Replication Manager, BO, or a dedicated service?
6. Should partner signup require a partner token/invite code?
7. Should enterprise bastion signup require domain allowlist or invite-only access?
8. Should the signup credit result be displayed in RM UI or only stored in CRM?

## 15. Final separation rule

Use this rule when implementing:

- **Register** means: admin registers this Replication Manager instance to Cloud18.
- **Signup** means: user signs up/logs in to enter this already-registered Replication Manager instance.

They must remain different handlers and different product flows.
