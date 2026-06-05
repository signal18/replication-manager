package cluster

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func (app *App) GetMonitoringStatus() string {
	cluster := app.ClusterGroup
	routes := app.GetAppConfig().Deployment.Routes
	var primaryStatus = stateAppRunning
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

	routeStatuses := make([]config.RouteStatus, 0, len(routes))
	for _, route := range routes {
		routeKey := routeEndpoint(route)
		routeStatus := config.RouteStatus{Route: route, Status: stateAppRunning}
		routeNorm := route
		routeNorm.Normalize()
		routeLabel := " on route " + routeNorm.Label()
		switch route.Protocol {
		case "https":
			httpStatus, _, err := app.GetAppHTTPStatus(route, false)
			if err == nil {
				markSuccessfulRouteCheck(routeKey)
			} else {
				routeStatus.Status = stateAppWarning
				// Don't set primaryStatus yet — defer until local check outcome is known.

				errKey := ErrAppConnectFailed
				errDesc := fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), err)
				if strings.HasPrefix(err.Error(), "unexpected status code") {
					errKey = ErrAppUnexpectedStatus
					errDesc = fmt.Sprintf(config.ClusterError[ErrAppUnexpectedStatus], app.GetId(), httpStatus)
				}

				httpStatus, _, err := app.GetAppLocalHTTPStatus(route, false)
				if err != nil {
					if strings.HasPrefix(err.Error(), "unexpected status code") {
						errKey = ErrAppUnexpectedStatus
						errDesc = fmt.Sprintf(config.ClusterError[ErrAppUnexpectedStatus], app.GetId(), httpStatus)
					} else {
						errKey = ErrAppConnectFailed
						errDesc = fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), err)
					}

					debouncedRecordAppErr(routeKey, []state.State{{ErrType: "WARN", ErrKey: errKey, ErrDesc: errDesc + routeLabel, ServerUrl: app.Host}}, err)

					if route.Primary {
						primaryStatus = stateFailed
					} else {
						primaryStatus = stateAppWarning
					}
					routeStatus.Status = stateFailed
				} else {
					markSuccessfulRouteCheck(routeKey)
				}
			}
		case "http":
			// Port-route HTTP: skip external check when no cname is defined.
			if route.CName != "" {
				httpStatus, _, err := app.GetAppHTTPStatus(route, false)
				if err == nil {
					markSuccessfulRouteCheck(routeKey)
				} else {
					routeStatus.Status = stateAppWarning
					// Don't set primaryStatus yet — defer until local check outcome is known.
					errKey := ErrAppConnectFailed
					errDesc := fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), err)
					if strings.HasPrefix(err.Error(), "unexpected status code") {
						errKey = ErrAppUnexpectedStatus
						errDesc = fmt.Sprintf(config.ClusterError[ErrAppUnexpectedStatus], app.GetId(), httpStatus)
					}
					httpStatus, _, err = app.GetAppLocalHTTPStatus(route, false)
					if err != nil {
						if strings.HasPrefix(err.Error(), "unexpected status code") {
							errKey = ErrAppUnexpectedStatus
							errDesc = fmt.Sprintf(config.ClusterError[ErrAppUnexpectedStatus], app.GetId(), httpStatus)
						} else {
							errKey = ErrAppConnectFailed
							errDesc = fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), err)
						}
						debouncedRecordAppErr(routeKey, []state.State{{ErrType: "WARN", ErrKey: errKey, ErrDesc: errDesc + routeLabel, ServerUrl: app.Host}}, err)
						if route.Primary {
							primaryStatus = stateFailed
						} else {
							primaryStatus = stateAppWarning
						}
						routeStatus.Status = stateFailed
					} else {
						markSuccessfulRouteCheck(routeKey)
					}
				}
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
					"Port-route HTTP check skipped for app %s (no cname defined), using local/backend check only", app.Name)
				if err := app.GetAppLocalHTTPStatus2xx(route); err != nil {
					debouncedRecordAppErr(routeKey, []state.State{{ErrType: "WARN", ErrKey: ErrAppConnectFailed, ErrDesc: fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), err) + routeLabel, ServerUrl: app.Host}}, err)
					if route.Primary {
						primaryStatus = stateFailed
					} else {
						primaryStatus = stateAppWarning
					}
					routeStatus.Status = stateFailed
				} else {
					markSuccessfulRouteCheck(routeKey)
				}
			}
		case "tcp":
			// For port-mode TCP routes without a CNAME there is no external gateway
			// endpoint to probe — skip directly to the local/backend check.
			if route.Mode == "port" && route.CName == "" {
				if err := app.GetAppLocalTCPStatus(route); err != nil {
					debouncedRecordAppErr(routeKey, []state.State{
						{ErrType: "WARN", ErrKey: ErrAppTCPConnectFailed, ErrDesc: fmt.Sprintf(config.ClusterError[ErrAppTCPConnectFailed], app.GetId(), err) + routeLabel, ServerUrl: app.Host},
						{ErrType: "WARN", ErrKey: ErrAppConnectFailed, ErrDesc: fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), err) + routeLabel, ServerUrl: app.Host},
					}, err)
					routeStatus.Status = stateFailed
					if route.Primary {
						primaryStatus = stateFailed
					} else {
						primaryStatus = stateAppWarning
					}
				} else {
					markSuccessfulRouteCheck(routeKey)
				}
				break
			}
			err := app.GetAppTCPStatus(route)
			if err == nil {
				markSuccessfulRouteCheck(routeKey)
			} else {
				routeStatus.Status = stateAppWarning
				primaryStatus = stateAppWarning

				if err := app.GetAppLocalTCPStatus(route); err != nil {
					// Temporary compatibility: emit APPERR003 (canonical) and APPERR001 together.
					debouncedRecordAppErr(routeKey, []state.State{
						{ErrType: "WARN", ErrKey: ErrAppTCPConnectFailed, ErrDesc: fmt.Sprintf(config.ClusterError[ErrAppTCPConnectFailed], app.GetId(), err) + routeLabel, ServerUrl: app.Host},
						{ErrType: "WARN", ErrKey: ErrAppConnectFailed, ErrDesc: fmt.Sprintf(config.ClusterError[ErrAppConnectFailed], app.GetId(), err) + routeLabel, ServerUrl: app.Host},
					}, err)

					routeStatus.Status = stateFailed
					if route.Primary {
						primaryStatus = stateFailed
					}
				} else {
					markSuccessfulRouteCheck(routeKey)
				}
			}
		default:
			// Keep APPERR004 argument order aligned with config.ClusterError format string.
			errStates[ErrAppUnsupportedProto] = state.State{ErrType: "WARN", ErrKey: ErrAppUnsupportedProto, ErrDesc: fmt.Sprintf(config.ClusterError[ErrAppUnsupportedProto], route.Protocol, app.GetId()) + routeLabel, ServerUrl: app.Host}
			app.ResetAppErrConsecutiveCnt(routeKey)
			routeStatus.Status = stateFailed

			if route.Primary {
				primaryStatus = stateFailed
			} else {
				primaryStatus = stateAppWarning
			}
		}

		routeStatuses = append(routeStatuses, routeStatus)
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
		// If auth is configured on an HTTPS route, skip the HTTP fallback: a
		// plain HTTP check without credentials cannot confirm the authenticated
		// endpoint is working, so we report the original failure instead.
		if route.Protocol == "https" && route.Monitor != nil && route.Monitor.AuthType != "" {
			return -1, nil, err
		}
		fallback := route
		fallback.Protocol = "http"
		return app.GetAppHTTPStatus(fallback, getBody)
	}
	return a, b, nil
}

// GetAppLocalHTTPStatus2xx performs a local HTTP check using the destination
// port and returns nil when the response is 2xx.
func (app *App) GetAppLocalHTTPStatus2xx(route config.Route) error {
	_, _, err := app.GetAppLocalHTTPStatus(route, false)
	return err
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
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
						"monitor basic auth: cannot resolve secret var %q for app %s route %s: %v",
						route.Monitor.AuthSecretVar, app.Name, route.CName, serr)
				} else {
					req.SetBasicAuth(route.Monitor.AuthUser, secret)
				}
			}
		case "bearer":
			if route.Monitor.AuthSecretVar != "" {
				secret, serr := cluster.GetAppDecryptedVariableValue(app, route.Monitor.AuthSecretVar)
				if serr != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
						"monitor bearer auth: cannot resolve secret var %q for app %s route %s: %v",
						route.Monitor.AuthSecretVar, app.Name, route.CName, serr)
				} else {
					req.Header.Set("Authorization", "Bearer "+secret)
				}
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
