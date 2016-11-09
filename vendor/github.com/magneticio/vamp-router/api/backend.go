package api

import (
	"github.com/gin-gonic/gin"
	"github.com/magneticio/vamp-router/haproxy"
	"net/http"
)

func GetBackends(c *gin.Context) {

	Config(c).BeginReadTrans()
	defer Config(c).EndReadTrans()

	result := Config(c).GetBackends()
	if result != nil {
		c.JSON(http.StatusOK, result)
	} else {
		c.JSON(http.StatusNotFound, gin.H{"status": "no backends found"})
	}

}

func GetBackend(c *gin.Context) {

	Config(c).BeginReadTrans()
	defer Config(c).EndReadTrans()

	backend := c.Params.ByName("name")

	if result, err := Config(c).GetBackend(backend); err != nil {
		HandleError(c, err)
	} else {
		c.JSON(http.StatusOK, result)
	}
}

func PostBackend(c *gin.Context) {

	Config(c).BeginWriteTrans()
	defer Config(c).EndWriteTrans()

	var backend haproxy.Backend

	if c.Bind(&backend) {

		if err := Config(c).AddBackend(&backend); err != nil {
			HandleError(c, err)
		} else {
			HandleReload(c, Config(c), http.StatusCreated, gin.H{"status": "created backend"})
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"status": "bad request"})
	}
}

func DeleteBackend(c *gin.Context) {

	Config(c).BeginReadTrans()
	defer Config(c).EndReadTrans()

	name := c.Params.ByName("name")

	if err := Config(c).DeleteBackend(name); err != nil {
		HandleError(c, err)
	} else {
		HandleReload(c, Config(c), http.StatusNoContent, gin.H{})
	}
}

func GetServers(c *gin.Context) {

	Config(c).BeginReadTrans()
	defer Config(c).EndReadTrans()

	backend := c.Params.ByName("name")

	if result, err := Config(c).GetServers(backend); err != nil {
		HandleError(c, err)
	} else {
		c.JSON(http.StatusOK, result)
	}
}

func GetServer(c *gin.Context) {

	Config(c).BeginReadTrans()
	defer Config(c).EndReadTrans()

	backend := c.Params.ByName("name")
	server := c.Params.ByName("server")

	if result, err := Config(c).GetServer(backend, server); err != nil {
		HandleError(c, err)
	} else {
		c.JSON(http.StatusOK, result)
	}
}

func PostServer(c *gin.Context) {

	Config(c).BeginWriteTrans()
	defer Config(c).EndWriteTrans()

	var server haproxy.ServerDetail
	backend := c.Params.ByName("name")

	if c.Bind(&server) {
		if err := Config(c).AddServer(backend, &server); err != nil {
			HandleError(c, err)
		} else {
			HandleReload(c, Config(c), http.StatusCreated, gin.H{"status": "created server"})
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"status": "bad request"})
	}
}

func PutServerWeight(c *gin.Context) {

	Config(c).BeginWriteTrans()
	defer Config(c).EndWriteTrans()

	var json UpdateWeight
	backend := c.Params.ByName("name")
	server := c.Params.ByName("server")

	if c.Bind(&json) {
		status, err := Runtime(c).SetWeight(backend, server, json.Weight)

		// check on Runtime errors
		if err != nil {
			c.String(500, err.Error())
		} else {
			switch status {
			case "No such server.\n\n":
				c.String(404, status)
			case "No such backend.\n\n":
				c.String(404, status)
			default:

				//update the Config(c) object with the new weight
				if err := Config(c).SetWeight(backend, server, json.Weight); err != nil {
					HandleError(c, err)
				} else {
					HandleReload(c, Config(c), http.StatusOK, gin.H{"status": "updated server weight"})
				}
			}
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"status": "bad request"})
	}
}

func DeleteServer(c *gin.Context) {

	Config(c).BeginWriteTrans()
	defer Config(c).EndWriteTrans()

	backend := c.Params.ByName("name")
	server := c.Params.ByName("server")

	if err := Config(c).DeleteServer(backend, server); err != nil {
		HandleError(c, err)
	} else {
		HandleReload(c, Config(c), http.StatusNoContent, gin.H{})
	}
}
