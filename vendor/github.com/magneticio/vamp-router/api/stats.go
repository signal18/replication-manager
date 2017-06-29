package api

import (
	"github.com/gin-gonic/gin"
	"github.com/magneticio/vamp-router/metrics"
	"net/http"
)

func GetAllStats(c *gin.Context) {

	status, err := Runtime(c).GetJsonStats("all")
	if err != nil {
		c.String(500, err.Error())
	} else {
		c.JSON(http.StatusOK, status)
	}

}

func GetBackendStats(c *gin.Context) {

	status, err := Runtime(c).GetJsonStats("backend")
	if err != nil {
		c.String(500, err.Error())
	} else {
		c.JSON(http.StatusOK, status)
	}

}

func GetFrontendStats(c *gin.Context) {

	status, err := Runtime(c).GetJsonStats("frontend")
	if err != nil {
		c.String(500, err.Error())
	} else {
		c.JSON(http.StatusOK, status)
	}
}

func GetServerStats(c *gin.Context) {

	status, err := Runtime(c).GetJsonStats("server")
	if err != nil {
		c.String(500, err.Error())
	} else {
		c.JSON(http.StatusOK, status)
	}

}

func GetSSEStream(c *gin.Context) {
	sseBroker := c.MustGet("sseBroker").(*metrics.SSEBroker)
	sseBroker.ServeHTTP(c.Writer, c.Request)
}

func GetSSEContentType(c *gin.Context) {
	c.Writer.Header().Set("X-VAMP-STREAM", "vamp-router")
	c.String(http.StatusOK, "")
}
