package helpers

import (
	"runtime"
	"testing"
)

func TestHelpers_HaproxyLocation(t *testing.T) {

	runtime := runtime.GOOS
	location := HaproxyLocation()

	if runtime == "darwin" && location != MAC_HAPROXY_BIN_LOCATION {
		t.Errorf("Failed to map OS to correct Haproxy location on Darwin")
	}

	if runtime == "linux" && location != LINUX_HAPROXY_BIN_LOCATION {
		t.Errorf("Failed to map OS to correct Haproxy location on Linux")
	}

}
