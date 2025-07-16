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
	routes := app.GetAppConfig().Deployment.Routes
	var primaryStatus string = stateAppRunning
	if len(routes) == 0 {
		return stateFailed
	}

	routeStatuses := make([]config.RouteStatus, 0, len(routes))
	for _, route := range routes {
		routeStatus := config.RouteStatus{Route: route, Status: stateAppRunning}
		if route.Protocol == "https" {
			httpStatus, _, err := app.GetAppHTTPStatus(route, false)
			if err != nil {
				routeStatus.Status = stateAppWarning
				primaryStatus = stateAppWarning
				if strings.HasPrefix(err.Error(), "unexpected status code") {
					app.ClusterGroup.SetState("APPERR002", state.State{ErrType: "WARN", ErrKey: "APPERR002", ErrDesc: fmt.Sprintf(config.ClusterError["APPERR002"], app.GetId(), httpStatus)})
				} else {
					app.ClusterGroup.SetState("APPERR001", state.State{ErrType: "WARN", ErrKey: "APPERR001", ErrDesc: fmt.Sprintf(config.ClusterError["APPERR001"], app.GetId(), err)})
				}

				httpStatus, _, err := app.GetAppLocalHTTPStatus(route, false)
				if err != nil {
					if strings.HasPrefix(err.Error(), "unexpected status code") {
						app.ClusterGroup.SetState("APPERR002", state.State{ErrType: "WARN", ErrKey: "APPERR002", ErrDesc: fmt.Sprintf(config.ClusterError["APPERR002"], app.GetId(), httpStatus)})
					} else {
						app.ClusterGroup.SetState("APPERR001", state.State{ErrType: "WARN", ErrKey: "APPERR001", ErrDesc: fmt.Sprintf(config.ClusterError["APPERR001"], app.GetId(), err)})
					}

					routeStatus.Status = stateFailed
					if route.Primary {
						primaryStatus = stateFailed
					} else {
						primaryStatus = stateAppWarning
					}
				}
			}
		} else if route.Protocol == "tcp" {
			// For TCP routes, we assume the app is running if it can connect
			err := app.GetAppTCPStatus(route)
			if err != nil {
				routeStatus.Status = stateAppWarning
				primaryStatus = stateAppWarning

				app.ClusterGroup.SetState("APPERR003", state.State{ErrType: "WARN", ErrKey: "APPERR003", ErrDesc: fmt.Sprintf(config.ClusterError["APPERR003"], app.GetId(), err)})

				if err := app.GetAppLocalTCPStatus(route); err != nil {
					app.ClusterGroup.SetState("APPERR003", state.State{ErrType: "WARN", ErrKey: "APPERR003", ErrDesc: fmt.Sprintf(config.ClusterError["APPERR003"], app.GetId(), err)})
					routeStatus.Status = stateFailed
					if route.Primary {
						primaryStatus = stateFailed
					}
				}
			}
		} else {
			app.ClusterGroup.SetState("APPERR004", state.State{ErrType: "WARN", ErrKey: "APPERR004", ErrDesc: fmt.Sprintf(config.ClusterError["APPERR004"], app.GetId(), route.Protocol)})
			routeStatus.Status = stateFailed

			if route.Primary {
				primaryStatus = stateFailed
			} else {
				primaryStatus = stateAppWarning
			}
		}

		routeStatuses = append(routeStatuses, routeStatus)
	}

	app.Lock()
	defer app.Unlock()
	app.RouteStatus = routeStatuses

	return primaryStatus
}

func (app *App) GetAppLocalHTTPStatus(route config.Route, getBody bool) (int, []byte, error) {
	route.CName = app.GetHost() + ":" + route.Port
	a, b, err := app.GetAppHTTPStatus(route, getBody)
	if err != nil {
		route.Protocol = "http"
		return app.GetAppHTTPStatus(route, getBody)
	} else {
		return a, b, nil
	}
}

func (app *App) GetAppHTTPStatus(route config.Route, getBody bool) (int, []byte, error) {
	cluster := app.ClusterGroup

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.Timeout)*time.Second)
	defer cancel()

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}

	urlpost := "https://" + route.CName
	if route.Protocol == "http" {
		urlpost = "http://" + route.CName
		client.Transport = &http.Transport{} // Reset transport for HTTP
	}

	// Create a request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlpost, nil)
	if err != nil {
		return -1, nil, fmt.Errorf("failed to create request to %s: %v", urlpost, err)
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

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, body, errors.New("unexpected status code")
	}

	if !getBody {
		return resp.StatusCode, nil, nil
	}

	return resp.StatusCode, body, nil
}

func (app *App) GetAppLocalTCPStatus(route config.Route) error {
	route.CName = app.GetHost()
	return app.GetAppTCPStatus(route)
}

func (app *App) GetAppTCPStatus(route config.Route) error {
	cluster := app.ClusterGroup
	dialer := net.Dialer{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.Timeout)*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%s", route.CName, route.Port))
	if err != nil {
		return fmt.Errorf("error connecting to %s:%s: %v", route.CName, route.Port, err)
	}
	defer conn.Close()

	// If we can connect, the app is running
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
