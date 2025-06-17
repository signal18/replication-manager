package cluster

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

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
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + route.CName

	resp, err := client.Get(urlpost)
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
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%s", route.CName, route.Port))
	if err != nil {
		return fmt.Errorf("error connecting to %s: %v", route.CName, err)
	}
	defer conn.Close()

	// If we can connect, the app is running
	return nil
}
