package graphite

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	pb "github.com/signal18/replication-manager/graphite/carbonzipper/carbonzipperpb"
	"github.com/signal18/replication-manager/graphite/expr"
)

var errNoMetrics = errors.New("no metrics")

type unmarshaler interface {
	Unmarshal([]byte) error
}

type zipper struct {
	z      string
	client *http.Client
}

func (z zipper) Find(metric string) (pb.GlobResponse, error) {

	u, _ := url.Parse(z.z + "/metrics/find/")

	u.RawQuery = url.Values{
		"query":  []string{metric},
		"format": []string{"protobuf"},
	}.Encode()

	var pbresp pb.GlobResponse

	err := z.get("Find", u, &pbresp)

	return pbresp, err
}

func (z zipper) get(who string, u *url.URL, msg unmarshaler) error {
	resp, err := z.client.Get(u.String())
	if err != nil {
		return fmt.Errorf("http.Get: %+v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("io.ReadAll: %+v", err)
	}

	err = msg.Unmarshal(body)
	if err != nil {
		return fmt.Errorf("proto.Unmarshal: %+v", err)
	}

	return nil
}

func (z zipper) Passthrough(metric string) ([]byte, error) {

	u, _ := url.Parse(z.z + metric)

	resp, err := z.client.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("http.Get: %+v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("io.ReadAll: %+v", err)
	}

	return body, nil
}

func (z zipper) Render(metric string, from, until int32) (expr.MetricData, error) {

	u, _ := url.Parse(z.z + "/render/")

	u.RawQuery = url.Values{
		"target": []string{metric},
		"format": []string{"protobuf"},
		"from":   []string{strconv.Itoa(int(from))},
		"until":  []string{strconv.Itoa(int(until))},
	}.Encode()

	var pbresp pb.MultiFetchResponse
	err := z.get("Render", u, &pbresp)
	if err != nil {
		return expr.MetricData{}, err
	}

	if m := pbresp.Metrics; len(m) == 0 {
		return expr.MetricData{}, errNoMetrics
	}

	mdata := expr.MetricData{FetchResponse: *pbresp.Metrics[0]}

	return mdata, nil
}
