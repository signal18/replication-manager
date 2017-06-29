package api

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

func GetInfo(c *gin.Context) {

	version := c.MustGet("appVersion").(string)

	status, err := Runtime(c).GetInfo()
	if err != nil {
		HandleError(c, err)
	} else {

		apiInfo := struct {
			Message string
			Version string
			Status  interface{}
		}{"Hi, I'm Vamp Router! How are you?", version, status}

		c.JSON(http.StatusOK, apiInfo)
	}
}
