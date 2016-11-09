package haproxy

import (
	"encoding/json"
	"io/ioutil"
	"testing"
)

const (
	FILTERS_CORRECT_JSON = "../test/test_filters_correct.json"
	FILTERS_WRONG_JSON   = "../test/test_filters_wrong.json"
)

func TestFilters_ParseFilter(t *testing.T) {

	fakeRoute := "my_route"

	j, _ := ioutil.ReadFile(FILTERS_CORRECT_JSON)
	var filtersCorrect, filtersWrong []*Filter
	_ = json.Unmarshal(j, &filtersCorrect)

	i, _ := ioutil.ReadFile(FILTERS_WRONG_JSON)
	_ = json.Unmarshal(i, &filtersWrong)

	for _, filter := range filtersCorrect {
		if _, err := parseFilter(fakeRoute, filter); err != nil {
			t.Errorf("Failed to correctly parse a filter %s", err.Error())
		}
	}

	for _, filter := range filtersWrong {
		if _, err := parseFilter(fakeRoute, filter); err == nil {
			t.Errorf("Filter parsing should fail with incorrect filters")
		}
	}

}

func TestFilters_ParseFilterCondition(t *testing.T) {

	/*
	  these two notations should be equivalent. The full Haproxy condition
	  should pass through untouched
	*/

	tests := []struct {
		Input          string
		ExpectedString string
		ExpectedNegate bool
	}{
		{"hdr_sub(user-agent) Android", "hdr_sub(user-agent) Android", false},
		{"user-agent=Android", "hdr_sub(user-agent) Android", false},
		{"user-agent!=Android", "hdr_sub(user-agent) Android", true},
		{"User-Agent=Android", "hdr_sub(user-agent) Android", false},
		{"user-agent = Android", "hdr_sub(user-agent) Android", false},
		{"user-agent  =  Android", "user-agent  =  Android", false},
		{"user.agent = Ios", "hdr_sub(user-agent) Ios", false},
		{"host = www.google.com", "hdr_str(host) www.google.com", false},
		{"host != www.google.com", "hdr_str(host) www.google.com", true},
		{"cookie MYCUSTOMER contains Value=good", "cook_sub(MYCUSTOMER) Value=good", false},
		{"has cookie JSESSIONID", "cook(JSESSIONID) -m found", false},
		{"misses cookie JSESSIONID", "cook_cnt(JSESSIONID) eq 0", false},

		{"has header X-SPECIAL", "hdr_cnt(X-SPECIAL) gt 0", false},
		{"misses header X-SPECIAL", "hdr_cnt(X-SPECIAL) eq 0", false},
	}

	for i, condition := range tests {
		if result, negate := parseFilterCondition(condition.Input); result != condition.ExpectedString || negate != condition.ExpectedNegate {
			t.Errorf("Failed to correctly parse filter condition %d. Got %s but expected %s with negate %s", (i + 1), result, condition.ExpectedString, condition.ExpectedNegate)
		}
	}
}
