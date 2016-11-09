package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/magneticio/vamp-router/haproxy"
	"github.com/magneticio/vamp-router/metrics"
	gologger "github.com/op/go-logging"
	"net/http"
)

func CreateApi(log *gologger.Logger, haConfig *haproxy.Config, haRuntime *haproxy.Runtime, SSEBroker *metrics.SSEBroker, version string) (*gin.Engine, error) {

	gin.SetMode("release")

	r := gin.New()
	r.Use(HaproxyMiddleware(haConfig, haRuntime))
	r.Use(LoggerMiddleware(log))
	r.Use(gin.Recovery())
	v1 := r.Group("/v1")

	{

		/*
		   Frontend
		*/
		v1.GET("/frontends", GetFrontends)
		v1.POST("/frontends", PostFrontend)
		v1.POST("frontends/:name/filters", PostFrontendFilter)
		v1.GET("/frontends/:name/filters", GetFrontendFilters)
		v1.DELETE("/frontends/:name/filters/:filter_name", DeleteFrontendFilter)
		v1.GET("/frontends/:name", GetFrontend)
		v1.DELETE("/frontends/:name", DeleteFrontend)

		/*
		   Backend
		*/
		v1.GET("/backends", GetBackends)
		v1.POST("/backends", PostBackend)
		v1.GET("/backends/:name", GetBackend)
		v1.DELETE("/backends/:name", DeleteBackend)
		v1.GET("/backends/:name/servers", GetServers)
		v1.GET("/backends/:name/servers/:server", GetServer)
		v1.PUT("/backends/:name/servers/:server", PutServerWeight)
		v1.POST("/backends/:name/servers", PostServer)
		v1.DELETE("/backends/:name/servers/:server", DeleteServer)

		/*
		   Stats
		*/
		v1.GET("/stats", GetAllStats)
		v1.GET("/stats/backends", GetBackendStats)
		v1.GET("/stats/frontends", GetFrontendStats)
		v1.GET("/stats/servers", GetServerStats)
		v1.GET("/stats/stream", SSEMiddleware(SSEBroker), GetSSEStream)
		v1.HEAD("/stats/stream", GetSSEContentType)

		/*
		   Config
		*/
		v1.GET("/config", GetConfig)
		v1.POST("/config", PostConfig)

		/*
			Routes
		*/
		v1.GET("/routes", GetRoutes)
		v1.POST("/routes", PostRoute)

		v1.GET("/routes/:route", GetRoute)
		v1.PUT("/routes/:route", PutRoute)
		v1.DELETE("/routes/:route", DeleteRoute)

		v1.GET("/routes/:route/services", GetRouteServices)

		// You can post one, or multiple services in one go.
		v1.POST("/routes/:route/services", PostRouteService)

		// This endpoint allows you to update all services in a route in one go.
		// Any services in the JSON object not already part of the route, i.e. they are new and thus cannot
		// be updated, are silently discarded.
		v1.PUT("/routes/:route/services", PutRouteServices)
		v1.GET("/routes/:route/services/:service", GetRouteService)
		v1.PUT("/routes/:route/services/:service", PutRouteService)
		v1.DELETE("/routes/:route/services/:service", DeleteRouteService)

		v1.GET("/routes/:route/services/:service/servers", GetServiceServers)
		v1.GET("/routes/:route/services/:service/servers/:server", GetServiceServer)
		v1.PUT("/routes/:route/services/:service/servers/:server", PutServiceServer)
		v1.POST("/routes/:route/services/:service/servers", PostServiceServer)
		v1.DELETE("/routes/:route/services/:service/servers/:server", DeleteServiceServer)
		/*
		   Info
		*/
		v1.GET("/info", InfoMiddleWare(version), GetInfo)

		/*
			Debug helpers
		*/

		v1.GET("/debug/reset", Reset)
	}

	return r, nil
}

// Handles the reloading and persisting of the Haproxy config after a successful mutation of the
// config object.
func HandleReload(c *gin.Context, config *haproxy.Config, status int, message gin.H) {

	runtime := c.MustGet("haRuntime").(*haproxy.Runtime)

	err := config.RenderAndPersist()
	if err != nil {
		HandleError(c, &haproxy.Error{http.StatusInternalServerError, errors.New("Error rendering config file")})
		return
	}

	err = runtime.Reload(config)
	if err != nil {
		HandleError(c, &haproxy.Error{http.StatusInternalServerError, errors.New("Error reloading the HAproxy configuration")})
		return
	}

	HandleSucces(c, status, message)
}

// Handles the simple successful return status
func HandleSucces(c *gin.Context, status int, message gin.H) {
	if status == 204 {
		c.String(status, "")
	} else {
		c.JSON(status, message)
	}
}

// Handles the return of an error from the Haproxy object
func HandleError(c *gin.Context, err *haproxy.Error) {
	c.JSON(err.Code, gin.H{"status": err.Error()})
}

// helper methods to grab the injected Config from the Http context
func Config(c *gin.Context) *haproxy.Config {
	return c.MustGet("haConfig").(*haproxy.Config)
}

// helper methods to grab the injected Runtime from the Http context
func Runtime(c *gin.Context) *haproxy.Runtime {
	return c.MustGet("haRuntime").(*haproxy.Runtime)
}
