package cluster

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// canonicalExpectStatus returns a sorted, deduplicated representation of a
// (post-Normalize) expect-status string so that "200,204" and "204,200"
// produce the same key.  Falls back to the raw string on parse failure
// (defensive; valid configs always parse after Normalize).
func canonicalExpectStatus(s string) string {
	codes, err := config.ParseExpectStatus(s)
	if err != nil || len(codes) == 0 {
		return s
	}
	sort.Ints(codes)
	parts := make([]string, len(codes))
	for i, c := range codes {
		parts[i] = strconv.Itoa(c)
	}
	return strings.Join(parts, ",")
}

// buildLocalCheckKey returns a deduplication key for a route's local
// reachability probe.  Routes with identical keys exercise the same local
// network path and share a single check result.
//
//	tcp:        local-host + destination-port
//	http/https: local-host + destination-port + monitor contract
//	            (path, expected status, auth type/user/secret ref)
//
// http and https routes collapse to the same "http" local class because TLS
// terminates at the HAProxy gateway; the backend always speaks plain HTTP
// regardless of the external scheme.
func buildLocalCheckKey(appHost string, route config.Route) string {
	port := route.DestinationPort
	if port == "" {
		port = route.Port
	}
	switch route.Protocol {
	case "tcp":
		return "local|tcp|" + appHost + "|" + port
	case "http", "https":
		// Defaults match what RouteMonitor.Normalize() would produce for a zero-value
		// monitor, ensuring nil and explicit-default monitors share the same key.
		monPath, expectStatus, authType, authUser, authSecretVar := "/", "200", "", "", ""
		if route.Monitor != nil {
			m := *route.Monitor
			m.Normalize()
			monPath = m.Path
			expectStatus = canonicalExpectStatus(m.ExpectStatus)
			authType = m.AuthType
			authUser = m.AuthUser
			authSecretVar = m.AuthSecretVar
		}
		return strings.Join([]string{"local", "http", appHost, port, monPath, expectStatus, authType, authUser, authSecretVar}, "|")
	default:
		return "local|unsupported|" + route.Protocol + "|" + appHost + "|" + port
	}
}

func (app *App) GetMonitoringStatus() string {
	cluster := app.ClusterGroup
	routes := app.GetAppConfig().Deployment.Routes
	appErrKeys := []string{ErrAppConnectFailed, ErrAppUnexpectedStatus, ErrAppTCPConnectFailed, ErrAppUnsupportedProto, ErrAppGatewayConflict}
	errStates := make(map[string]state.State)

	// Gateway conflict check: surface the conflict as WARN state and let the rest
	// of the monitoring checks run — local reachability is independent of gateway
	// route ownership.
	if app.AppConfig != nil {
		if conflicted, reason := cluster.IsAppGatewayConflicted(app.AppConfig.AppHost, app.AppConfig.AppPort); conflicted {
			errStates[ErrAppGatewayConflict] = state.State{
				ErrType:   "WARN",
				ErrKey:    ErrAppGatewayConflict,
				ErrDesc:   fmt.Sprintf(config.ClusterError[ErrAppGatewayConflict], app.GetId(), reason),
				ServerUrl: app.Host,
			}
		}
	}
	failureThreshold := cluster.Conf.AppErrorDebounceThreshold
	if failureThreshold <= 0 {
		// Keep legacy default when the cluster-level override is unset/invalid.
		failureThreshold = appErrFailureThreshold
	}
	routeEndpoint := func(route config.Route) string {
		normalized := route
		normalized.Normalize()
		return config.BuildRouteStateKey(normalized)
	}
	debouncedRecordAppErr := func(routeKey string, states []state.State, routeErr error) {
		failCount := app.IncAppErrConsecutiveCnt(routeKey)
		if failCount < failureThreshold {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlDbg,
				"Debounced app check failure for app %s endpoint %s: %s (fail count %d/%d)",
				app.Name,
				routeKey,
				routeErr,
				failCount,
				failureThreshold,
			)
			return
		}

		for _, st := range states {
			errStates[st.ErrKey] = st
		}
	}
	markSuccessfulRouteCheck := func(routeKey string) {
		app.ResetAppErrConsecutiveCnt(routeKey)
	}

	if len(routes) == 0 {
		errStates[ErrAppConnectFailed] = state.State{ErrType: "WARN", ErrKey: ErrAppConnectFailed, ErrDesc: fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), "no routes defined"), ServerUrl: app.Host}
		app.ResetAllAppErrConsecutiveCnt()
		for _, key := range appErrKeys {
			if st, ok := errStates[key]; ok {
				app.RecordAppError(key, st)
			} else {
				app.ResetAppError(key)
			}
		}
		app.SetRouteStatuses(nil)
		return stateFailed
	}

	// Run each unique local endpoint check exactly once.
	type localResult struct {
		err    error
		status int    // HTTP status code; 0 for TCP
		errKey string // error key for debounce reporting on HTTP failure
	}
	appHost := app.GetHost()
	localCache := make(map[string]*localResult)
	for _, route := range routes {
		lKey := buildLocalCheckKey(appHost, route)
		if _, seen := localCache[lKey]; seen {
			continue
		}
		switch route.Protocol {
		case "https", "http":
			status, _, err := app.GetAppLocalHTTPStatus(route, false)
			res := &localResult{err: err, status: status, errKey: ErrAppConnectFailed}
			if err != nil && strings.HasPrefix(err.Error(), "unexpected status code") {
				res.errKey = ErrAppUnexpectedStatus
			}
			localCache[lKey] = res
		case "tcp":
			err := app.GetAppLocalTCPStatus(route)
			localCache[lKey] = &localResult{err: err, errKey: ErrAppTCPConnectFailed}
		default:
			localCache[lKey] = &localResult{err: fmt.Errorf("unsupported protocol: %s", route.Protocol)}
		}
	}

	// Evaluate each route using cached local results; also run external checks.
	routeStatuses := make([]config.RouteStatus, 0, len(routes))
	for _, route := range routes {
		routeKey := routeEndpoint(route)
		routeStatus := config.RouteStatus{Route: route, Status: stateAppRunning}
		routeNorm := route
		routeNorm.Normalize()
		routeLabel := " on route " + routeNorm.Label()
		lKey := buildLocalCheckKey(appHost, route)

		switch route.Protocol {
		case "https", "http":
			localRes := localCache[lKey]
			if localRes.err != nil {
				errDesc := fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), localRes.err)
				if localRes.errKey == ErrAppUnexpectedStatus {
					errDesc = fmt.Sprintf(config.ClusterError[ErrAppUnexpectedStatus], app.GetId(), localRes.status)
				}
				debouncedRecordAppErr(routeKey, []state.State{{ErrType: "WARN", ErrKey: localRes.errKey, ErrDesc: errDesc + routeLabel, ServerUrl: app.Host}}, localRes.err)
				routeStatus.Status = stateFailed
			} else {
				extStatus, _, extErr := app.GetAppHTTPStatus(route, false)
				if extErr == nil {
					markSuccessfulRouteCheck(routeKey)
				} else {
					errKey := ErrAppConnectFailed
					errDesc := fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), extErr)
					if strings.HasPrefix(extErr.Error(), "unexpected status code") {
						errKey = ErrAppUnexpectedStatus
						errDesc = fmt.Sprintf(config.ClusterError[ErrAppUnexpectedStatus], app.GetId(), extStatus)
					}
					debouncedRecordAppErr(routeKey, []state.State{{ErrType: "WARN", ErrKey: errKey, ErrDesc: errDesc + routeLabel, ServerUrl: app.Host}}, extErr)
					routeStatus.Status = stateAppWarning
				}
			}
		case "tcp":
			localRes := localCache[lKey]
			if localRes.err != nil {
				// Temporary compatibility: emit APPERR003 (canonical) and APPERR001 together.
				debouncedRecordAppErr(routeKey, []state.State{
					{ErrType: "WARN", ErrKey: ErrAppTCPConnectFailed, ErrDesc: fmt.Sprintf(config.ClusterError[ErrAppTCPConnectFailed], app.GetId(), localRes.err) + routeLabel, ServerUrl: app.Host},
					{ErrType: "WARN", ErrKey: ErrAppConnectFailed, ErrDesc: fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), localRes.err) + routeLabel, ServerUrl: app.Host},
				}, localRes.err)
				routeStatus.Status = stateFailed
			} else if extErr := app.GetAppTCPStatus(route); extErr != nil {
				routeStatus.Status = stateAppWarning
			} else {
				markSuccessfulRouteCheck(routeKey)
			}
		default:
			// Keep APPERR004 argument order aligned with config.ClusterError format string.
			errStates[ErrAppUnsupportedProto] = state.State{ErrType: "WARN", ErrKey: ErrAppUnsupportedProto, ErrDesc: fmt.Sprintf(config.ClusterError[ErrAppUnsupportedProto], route.Protocol, app.GetId()) + routeLabel, ServerUrl: app.Host}
			app.ResetAppErrConsecutiveCnt(routeKey)
			routeStatus.Status = stateFailed
		}

		routeStatuses = append(routeStatuses, routeStatus)
	}

	// Aggregate app status:
	//   Failed     – all deduped local endpoints are down (vacuously true when no local checks exist)
	//   AppWarning – at least one local endpoint succeeds but some route has an issue
	//   Running    – all checks pass
	allLocalFailed := true
	for _, res := range localCache {
		if res.err == nil {
			allLocalFailed = false
			break
		}
	}

	var primaryStatus string
	if allLocalFailed {
		primaryStatus = stateFailed
	} else {
		anyNotRunning := false
		for _, rs := range routeStatuses {
			if rs.Status != stateAppRunning {
				anyNotRunning = true
				break
			}
		}
		if anyNotRunning {
			primaryStatus = stateAppWarning
		} else {
			primaryStatus = stateAppRunning
		}
	}

	for _, key := range appErrKeys {
		if st, ok := errStates[key]; ok {
			app.RecordAppError(key, st)
		} else {
			app.ResetAppError(key)
		}
	}
	app.SetRouteStatuses(routeStatuses)

	return primaryStatus
}

func (app *App) GetAppLocalHTTPStatus(route config.Route, getBody bool) (int, []byte, error) {
	port := route.DestinationPort
	if port == "" {
		port = route.Port
	}
	route.CName = app.GetHost() + ":" + port
	// Clear Mode so GetAppHTTPStatus doesn't re-append SourcePort on top of the
	// already-resolved host:destPort address.
	route.Mode = ""
	a, b, err := app.GetAppHTTPStatus(route, getBody)
	if err != nil {
		// TLS terminates at HAProxy for all gateway-hosted routes, so the
		// backend always speaks plain HTTP regardless of the external protocol
		// or any auth configuration.  Always fall back to HTTP so a healthy
		// backend is not misreported as down when the HTTPS check fails.
		fallback := route
		fallback.Protocol = "http"
		return app.GetAppHTTPStatus(fallback, getBody)
	}
	return a, b, nil
}

// GetDeploymentRoutesSnapshot returns a copy of the app's current deployment
// routes taken under the app lock.  Use this when reading routes outside the
// app's own goroutine to avoid racing with API route mutations.
func (app *App) GetDeploymentRoutesSnapshot() []config.Route {
	app.Lock()
	defer app.Unlock()
	if app.AppConfig == nil || app.AppConfig.Deployment == nil || len(app.AppConfig.Deployment.Routes) == 0 {
		return nil
	}
	cp := make([]config.Route, len(app.AppConfig.Deployment.Routes))
	copy(cp, app.AppConfig.Deployment.Routes)
	return cp
}

func (app *App) GetAppHTTPStatus(route config.Route, getBody bool) (int, []byte, error) {
	cluster := app.ClusterGroup

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.Timeout)*time.Second)
	defer cancel()

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}

	host := route.CName
	if route.Mode == "port" && route.SourcePort != "" {
		host = route.CName + ":" + route.SourcePort
	}

	monPath := "/"
	if route.Monitor != nil && route.Monitor.Path != "" {
		monPath = route.Monitor.Path
	}

	urlpost := "https://" + host + monPath
	if route.Protocol == "http" {
		urlpost = "http://" + host + monPath
		client.Transport = &http.Transport{} // Reset transport for HTTP
	}

	// Create a request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlpost, nil)
	if err != nil {
		return -1, nil, fmt.Errorf("failed to create request to %s: %v", urlpost, err)
	}

	// Inject auth header when configured (HTTP/HTTPS only).
	if route.Monitor != nil {
		switch route.Monitor.AuthType {
		case "basic":
			if route.Monitor.AuthSecretVar != "" {
				secret, serr := cluster.GetAppDecryptedVariableValue(app, route.Monitor.AuthSecretVar)
				if serr != nil {
					return -1, nil, fmt.Errorf("monitor basic auth: cannot resolve secret var %q for app %s route %s: %v",
						route.Monitor.AuthSecretVar, app.Name, route.CName, serr)
				}
				req.SetBasicAuth(route.Monitor.AuthUser, secret)
			}
		case "bearer":
			if route.Monitor.AuthSecretVar != "" {
				secret, serr := cluster.GetAppDecryptedVariableValue(app, route.Monitor.AuthSecretVar)
				if serr != nil {
					return -1, nil, fmt.Errorf("monitor bearer auth: cannot resolve secret var %q for app %s route %s: %v",
						route.Monitor.AuthSecretVar, app.Name, route.CName, serr)
				}
				req.Header.Set("Authorization", "Bearer "+secret)
			}
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return -1, nil, fmt.Errorf("error connecting to %s: %v", urlpost, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("error reading response body from %s: %v", urlpost, err)
	}

	// Determine accepted status codes from the monitor config; default to 200.
	expectedCodes := []int{200}
	if route.Monitor != nil && route.Monitor.ExpectStatus != "" {
		if codes, perr := config.ParseExpectStatus(route.Monitor.ExpectStatus); perr == nil && len(codes) > 0 {
			expectedCodes = codes
		}
	}
	accepted := false
	for _, code := range expectedCodes {
		if resp.StatusCode == code {
			accepted = true
			break
		}
	}
	if !accepted {
		return resp.StatusCode, body, errors.New("unexpected status code")
	}

	if !getBody {
		return resp.StatusCode, nil, nil
	}

	return resp.StatusCode, body, nil
}

func (app *App) GetAppLocalTCPStatus(route config.Route) error {
	port := route.DestinationPort
	if port == "" {
		port = route.Port
	}
	route.CName = app.GetHost()
	route.Port = port
	// Clear Mode so GetAppTCPStatus doesn't override Port with SourcePort.
	route.Mode = ""
	return app.GetAppTCPStatus(route)
}

func (app *App) GetAppTCPStatus(route config.Route) error {
	cluster := app.ClusterGroup
	dialer := net.Dialer{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.Timeout)*time.Second)
	defer cancel()

	// For external TCP checks use the source port (the gateway listener port).
	// For local checks the caller has already set route.Port to the destination port.
	port := route.Port
	if route.Mode == "port" && route.CName != "" && route.SourcePort != "" {
		port = route.SourcePort
	}
	if port == "" {
		port = route.DestinationPort
	}

	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%s", route.CName, port))
	if err != nil {
		return fmt.Errorf("error connecting to %s:%s: %v", route.CName, port, err)
	}
	defer conn.Close()

	return nil
}

func (app *App) CheckPrimaryRoute() {
	cluster := app.ClusterGroup
	hasPrimaryRoute := false
	for _, route := range app.AppConfig.Deployment.Routes {
		if route.Primary {
			hasPrimaryRoute = true
			app.AppConfig.Deployment.PrimaryRoute = route
			break
		}
	}
	if !hasPrimaryRoute && len(app.AppConfig.Deployment.Routes) > 0 {
		app.AppConfig.Deployment.Routes[0].Primary = true
		app.AppConfig.Deployment.PrimaryRoute = app.AppConfig.Deployment.Routes[0]
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "No primary route defined for app %s, setting first route as primary", app.Name)
	}
}

func (app *App) CheckAppCredits() {
	if app.AppConfig.ProvAppCreditPlanned < 0 {
		app.ClusterGroup.SetState("CREDIT02", state.State{ErrType: "WARN", ErrKey: "CREDIT02", ErrDesc: fmt.Sprintf(config.ClusterError["CREDIT02"], app.GetId(), app.AppConfig.ProvAppCreditPlanned)})
	}
	if app.AppConfig.ProvAppCreditUsed < 0 {
		app.ClusterGroup.SetState("CREDIT03", state.State{ErrType: "WARN", ErrKey: "CREDIT03", ErrDesc: fmt.Sprintf(config.ClusterError["CREDIT03"], app.GetId(), app.AppConfig.ProvAppCreditUsed)})
	}
	if app.AppConfig.ProvAppCreditPlanned != app.AppConfig.ProvAppCreditUsed {
		app.ClusterGroup.SetState("CREDIT04", state.State{ErrType: "WARN", ErrKey: "CREDIT04", ErrDesc: fmt.Sprintf(config.ClusterError["CREDIT04"], app.GetId(), app.AppConfig.ProvAppCreditPlanned, app.AppConfig.ProvAppCreditUsed)})
	}
}
