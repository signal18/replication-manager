package api

import (
	"github.com/gin-gonic/gin"
	"github.com/magneticio/vamp-router/haproxy"
	"net/http"
)

func GetFrontends(c *gin.Context) {

	Config(c).BeginReadTrans()
	defer Config(c).EndReadTrans()

	result := Config(c).GetFrontends()
	if result != nil {
		c.JSON(http.StatusOK, result)
	} else {
		c.JSON(http.StatusNotFound, gin.H{"status": "no frontends found"})
	}

}

func GetFrontend(c *gin.Context) {

	Config(c).BeginReadTrans()
	defer Config(c).EndReadTrans()

	frontend := c.Params.ByName("name")

	if result, err := Config(c).GetFrontend(frontend); err != nil {
		HandleError(c, err)
	} else {
		c.JSON(http.StatusOK, result)
	}
}

func PostFrontend(c *gin.Context) {

	Config(c).BeginWriteTrans()
	defer Config(c).EndWriteTrans()

	var frontend haproxy.Frontend

	if c.Bind(&frontend) {
		if err := Config(c).AddFrontend(&frontend); err != nil {
			HandleError(c, err)
		} else {
			HandleReload(c, Config(c), http.StatusCreated, gin.H{"status": "created frontend"})
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"status": "bad request"})
	}
}

func DeleteFrontend(c *gin.Context) {

	Config(c).BeginWriteTrans()
	defer Config(c).EndWriteTrans()

	frontendName := c.Params.ByName("name")

	if err := Config(c).DeleteFrontend(frontendName); err != nil {
		HandleError(c, err)
	} else {
		HandleReload(c, Config(c), http.StatusNoContent, gin.H{})
	}
}

func GetFrontendFilters(c *gin.Context) {

	Config(c).BeginReadTrans()
	defer Config(c).EndReadTrans()

	frontend := c.Params.ByName("name")

	status := Config(c).GetFilters(frontend)
	c.JSON(http.StatusOK, status)

}

func PostFrontendFilter(c *gin.Context) {

	Config(c).BeginWriteTrans()
	defer Config(c).EndWriteTrans()

	var Filter haproxy.Filter
	frontend := c.Params.ByName("name")

	if c.Bind(&Filter) {
		Config(c).AddFilter(frontend, &Filter)
		HandleReload(c, Config(c), http.StatusCreated, gin.H{"status": "created Filter"})
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"status": "bad request"})
	}
}

func DeleteFrontendFilter(c *gin.Context) {

	Config(c).BeginWriteTrans()
	defer Config(c).EndWriteTrans()

	frontendName := c.Params.ByName("name")
	FilterName := c.Params.ByName("Filter_name")

	if err := Config(c).DeleteFilter(frontendName, FilterName); err != nil {
		HandleError(c, err)
	} else {
		HandleReload(c, Config(c), http.StatusNoContent, gin.H{})
	}
}
