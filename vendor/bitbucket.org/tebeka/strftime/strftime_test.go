package strftime

import (
	"testing"
	"time"
)

type TestCase struct {
	format, value string
}

var testTime = time.Date(2009, time.November, 10, 23, 1, 2, 3, time.UTC)
var testCases = []*TestCase{
	&TestCase{"%a", "Tue"},
	&TestCase{"%A", "Tuesday"},
	&TestCase{"%b", "Nov"},
	&TestCase{"%B", "November"},
	&TestCase{"%c", "Tue, 10 Nov 2009 23:01:02 UTC"},
	&TestCase{"%d", "10"},
	&TestCase{"%H", "23"},
	&TestCase{"%I", "11"},
	&TestCase{"%j", "314"},
	&TestCase{"%m", "11"},
	&TestCase{"%M", "01"},
	&TestCase{"%p", "PM"},
	&TestCase{"%S", "02"},
	&TestCase{"%U", "45"},
	&TestCase{"%w", "2"},
	&TestCase{"%W", "45"},
	&TestCase{"%x", "11/10/09"},
	&TestCase{"%X", "23:01:02"},
	&TestCase{"%y", "09"},
	&TestCase{"%Y", "2009"},
	&TestCase{"%Z", "UTC"},

	// Escape
	&TestCase{"%%%Y", "%2009"},
	// Embedded
	&TestCase{"/path/%Y/%m/report", "/path/2009/11/report"},
	//Empty
	&TestCase{"", ""},
}

func TestFormats(t *testing.T) {
	for _, tc := range testCases {
		value, err := Format(tc.format, testTime)
		if err != nil {
			t.Fatalf("error formatting %s - %s", tc.format, err)
		}
		if value != tc.value {
			t.Fatalf("error in %s: got %s instead of %s", tc.format, value, tc.value)
		}
	}
}

func TestUnknown(t *testing.T) {
	_, err := Format("%g", testTime)
	if err == nil {
		t.Fatalf("managed to expand %g")
	}
}
