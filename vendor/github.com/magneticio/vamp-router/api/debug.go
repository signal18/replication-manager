package api

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

func Reset(c *gin.Context) {

	err := Runtime(c).Reset()
	if err != nil {
		HandleError(c, err)
	} else {
		c.JSON(http.StatusOK, gin.H{"status": "reset counters"})
	}
}
