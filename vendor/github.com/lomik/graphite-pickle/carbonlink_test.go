package pickle

import (
	"bytes"
	"reflect"
	"testing"

	ogórek "github.com/lomik/og-rek"
)

const sampleCacheQuery = "\x00\x00\x00Y\x80\x02}q\x01(U\x06metricq\x02U,carbon.agents.carbon_agent_server.cache.sizeq\x03U\x04typeq\x04U\x0bcache-queryq\x05u."
const sampleCacheQuery2 = "\x00\x00\x00Y\x80\x02}q\x01(U\x04typeq\x04U\x0bcache-queryq\x05U\x06metricq\x02U,carbon.agents.carbon_agent_server.param.sizeq\x03u."
const sampleCacheQuery3 = "\x00\x00\x00R\x80\x02}(U\x06metricX,\x00\x00\x00carbon.agents.carbon_agent_server.param.sizeU\x04typeU\x0bcache-queryu." // unicode metric

func TestParseCarbonlinkRequest(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    *CarbonlinkRequest
		wantErr bool
	}{
		{name: "Good query",
			data: []byte(sampleCacheQuery)[4:],
			want: &CarbonlinkRequest{
				Type:   "cache-query",
				Metric: "carbon.agents.carbon_agent_server.cache.size"},
		},
		{name: "Good query2",
			data: []byte(sampleCacheQuery2)[4:],
			want: &CarbonlinkRequest{
				Type:   "cache-query",
				Metric: "carbon.agents.carbon_agent_server.param.size"},
		},
		{name: "Invalid query type",
			data: []byte("\x80\x02}q\x00(U\x06metricq\x01U\x03barq\x02U\x04typeq\x03U\x03fooq\x04u."),
			want: &CarbonlinkRequest{
				Type:   "foo",
				Metric: "bar",
			},
		},
		{name: "Garbage",
			data:    []byte("garbage"),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		got, err := ParseCarbonlinkRequest(tt.data)
		if (err != nil) != tt.wantErr {
			t.Errorf("%q. ParseCarbonlinkRequest() error = %v, wantErr %v", tt.name, err, tt.wantErr)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q. ParseCarbonlinkRequest() = %v, want %v", tt.name, got, tt.want)
		}
	}

	// test fast
	for _, tt := range tests {
		got, err := ParseCarbonlinkRequestFast(tt.data)
		if (err != nil) != tt.wantErr {
			t.Errorf("%q. ParseCarbonlinkRequestFast() error = %v, wantErr %v", tt.name, err, tt.wantErr)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q. ParseCarbonlinkRequestFast() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestMarshalCarbonlinkRequest(t *testing.T) {
	req := &CarbonlinkRequest{
		Metric: "carbon.agents.carbon_agent_server.param.size",
		Type:   "cache-query",
	}

	body, err := MarshalCarbonlinkRequest(req)
	if err != nil {
		t.FailNow()
	}

	req2, err := UnmarshalCarbonlinkRequest(body)
	if err != nil {
		t.FailNow()
	}

	if !reflect.DeepEqual(req, req2) {
		t.Errorf("%#v (expected) != %#v (actual)", req, req2)
	}
}

// marshalCarbonlinkRequestOgRek
func marshalCarbonlinkRequestOgRek(req *CarbonlinkRequest) ([]byte, error) {
	var buf bytes.Buffer
	enc := ogórek.NewEncoder(&buf)
	err := enc.Encode(req)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func TestMarshalCarbonlinkRequestOgRek(t *testing.T) {
	req := &CarbonlinkRequest{
		Metric: "carbon.agents.carbon_agent_server.param.size",
		Type:   "cache-query",
	}

	body, err := marshalCarbonlinkRequestOgRek(req)
	if err != nil {
		t.FailNow()
	}

	req2, err := UnmarshalCarbonlinkRequest(body)
	if err != nil {
		t.FailNow()
	}

	if !reflect.DeepEqual(req, req2) {
		t.Errorf("%#v (expected) != %#v (actual)", req, req2)
	}
}

func BenchmarkCarbonLinkPickleParse(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ParseCarbonlinkRequest([]byte(sampleCacheQuery)[4:])
		}
	})
}

func BenchmarkCarbonLinkPickleParseFast(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ParseCarbonlinkRequestFast([]byte(sampleCacheQuery)[4:])
		}
	})
}

func BenchmarkMarshalCarbonlinkRequest(b *testing.B) {
	req := &CarbonlinkRequest{
		Metric: "carbon.agents.carbon_agent_server.param.size",
		Type:   "cache-query",
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			MarshalCarbonlinkRequest(req)
		}
	})
}

func BenchmarkMarshalCarbonlinkRequestOgRek(b *testing.B) {
	req := &CarbonlinkRequest{
		Metric: "carbon.agents.carbon_agent_server.param.size",
		Type:   "cache-query",
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			marshalCarbonlinkRequestOgRek(req)
		}
	})
}
