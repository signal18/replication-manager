package misc

import (
	"github.com/satori/go.uuid"
	"strings"
)

func GetUUID() string {
	myUUID := uuid.NewV4()
	return strings.Split(myUUID.String(), "-")[0]
}
