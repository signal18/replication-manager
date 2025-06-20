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
	var primaryStatus string = app.State
	if len(routes) == 0 {
		return stateFailed
	}

	routeStatuses := make([]config.RouteStatus, 0, len(routes))
	for _, route := range routes {
		routeStatus := config.RouteStatus{Route: route, Status: stateAppRunning}
		if route.Protocol == "https" {
			httpStatus, _, err := app.GetAppHTTPStatus(route, false)
			if err != nil {
				if strings.HasPrefix(err.Error(), "unexpected status code") {
					app.ClusterGroup.SetState("APPERR002", state.State{ErrType: "WARN", ErrKey: "APPERR002", ErrDesc: fmt.Sprintf(config.AppError["APPERR002"], app.GetId(), httpStatus)})
				} else {
					app.ClusterGroup.SetState("APPERR001", state.State{ErrType: "WARN", ErrKey: "APPERR001", ErrDesc: fmt.Sprintf(config.AppError["APPERR001"], app.GetId(), err)})
				}

				routeStatus.Status = stateFailed

				if route.Primary {
					primaryStatus = stateFailed
				} else {
					primaryStatus = stateAppWarning
				}
			}
		} else if route.Protocol == "tcp" {
			// For TCP routes, we assume the app is running if it can connect
			if err := app.GetAppTCPStatus(route); err != nil {
				app.ClusterGroup.SetState("APPERR003", state.State{ErrType: "WARN", ErrKey: "APPERR003", ErrDesc: fmt.Sprintf(config.AppError["APPERR003"], app.GetId(), err)})
				routeStatus.Status = stateFailed

				if route.Primary {
					primaryStatus = stateFailed
				} else {
					primaryStatus = stateAppWarning
				}
			}
		} else {
			app.ClusterGroup.SetState("APPERR004", state.State{ErrType: "WARN", ErrKey: "APPERR004", ErrDesc: fmt.Sprintf(config.AppError["APPERR004"], app.GetId(), route.Protocol)})
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

func (app *App) GetAppHTTPStatus(route config.Route, getBody bool) (int, []byte, error) {
	cluster := app.ClusterGroup

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.Timeout)*time.Second)
	defer cancel()

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}

	urlpost := "https://" + route.CName

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
