package pickle

import (
	"reflect"
	"testing"
)

func TestParseMessage(t *testing.T) {
	table := []([]struct {
		name      string
		value     float64
		timestamp int64
	}){
		{
			// first line - input
			// line with empty name - error expected
			{"\x80\x02]q\x00q\x0bhello.worldq\x01Rixf8\xd3\x8eVK*\x86q\x02\x86q\x03a.", 0, 0},
			{"", 0, 0},
		},
		{
			{"\x80\x02]q\x00U\x0bhello.worldq\x01J\xf8\xd3\x8eVK*\x86q\x02\x86q\x03a.", 0, 0},
			{"hello.world", 42, 1452200952},
		},
		{
			// One metric with one datapoint
			// [("param1", (1423931224, 60.2))]
			{"(lp0\n(S'param1'\np1\n(I1423931224\nF60.2\ntp2\ntp3\na.", 0, 0},
			{"param1", 60.2, 1423931224},
		},
		{
			// One metric with multiple datapoints
			// [("param1", (1423931224, 60.2), (1423931225, 50.2), (1423931226, 40.2))]
			{"(lp0\n(S'param1'\np1\n(I1423931224\nF60.2\ntp2\n(I1423931225\nF50.2\ntp3\n(I1423931226\nF40.2\ntp4\ntp5\na.", 0, 0},
			{"param1", 60.2, 1423931224},
			{"param1", 50.2, 1423931225},
			{"param1", 40.2, 1423931226},
		},
		{
			// Multiple metrics with single datapoints
			// [("param1", (1423931224, 60.2)), ("param2", (1423931224, -15))]
			{"(lp0\n(S'param1'\np1\n(I1423931224\nF60.2\ntp2\ntp3\na(S'param2'\np4\n(I1423931224\nI-15\ntp5\ntp6\na.", 0, 0},
			{"param1", 60.2, 1423931224},
			{"param2", -15, 1423931224},
		},
		{
			// Complex update
			// [("param1", (1423931224, 60.2), (1423931284, 42)), ("param2", (1423931224, -15))]
			{"(lp0\n(S'param1'\np1\n(I1423931224\nF60.2\ntp2\n(I1423931284\nI42\ntp3\ntp4\na(S'param2'\np5\n(I1423931224\nI-15\ntp6\ntp7\na.", 0, 0},
			{"param1", 60.2, 1423931224},
			{"param1", 42, 1423931284},
			{"param2", -15, 1423931224},
		},
		{
			// long value
			// https://github.com/lomik/go-carbon/issues/182
			{"\x80\x02]q\x00U(hostname.interface-eth_lan2.if_octets.rxq\x01J\xe3\x91JY\x8a\x04S\xab\x8a\x00\x86q\x02\x86q\x03a.", 0, 0},
			{"hostname.interface-eth_lan2.if_octets.rx", 9087827.0, 1498059235},
		},
		{
			// bad #0 empty
			{"", 0, 0},
			{"", 0, 0},
		},
		{
			// bad #1 incorrect numper of elements
			{"(lp0\n(S'param1'\np1\ntp2\na.", 0, 0},
			{"", 0, 0},
		},
		{
			// bad #2 too few elements in a datapoint
			{"(lp0\n(S'param1'\np1\n(I1423931224\nF60.2\ntp2\n(I1423931284\ntp3\ntp4\na.", 0, 0},
			{"param1", 60.2, 1423931224},
			{"", 0, 0},
		},
		{
			// bad #3 too many elements in a datapoint
			{"(lp0\n(S'param1'\np1\n(I1423931224\nF60.2\nI3\ntp2\ntp3\na.", 0, 0},
			{"", 0, 0},
		},
		{
			// bad #4 negative timestamp in a datapoint
			{"(lp0\n(S'param1'\np1\n(I-1423931224\nI60\ntp2\ntp3\na.", 0, 0},
			{"", 0, 0},
		},
		{
			// bad #5 timestamp too big for uint32
			{"(lp0\n(S'param1'\np1\n(I4294967296\nF60.2\ntp2\ntp3\na.", 0, 0},
			{"", 0, 0},
		},
		{
			{"\x80\x02]q\x00U\x0bhello.worldq\x01J\xf8\xd3\x8eVK*\x86q\x02\x86q\x03a.", 0, 0},
			{"hello.world", 42, 1452200952},
		},
		{
			{"\x80\x02]q\x00(U\x0bhello.worldq\x01J\xf8\xd3\x8eVK*\x86q\x02e\x85q\x03.", 0, 0},
			{"hello.world", 42, 1452200952},
		},
		{
			{"\x80\x02U\x0bhello.worldq\x00]q\x01(J\xf8\xd3\x8eVK*e\x86q\x02\x85q\x03.", 0, 0},
			{"hello.world", 42, 1452200952},
		},
		{
			{"\x80\x02U\x0bhello.worldq\x00J\xf8\xd3\x8eVK*\x86q\x01\x86q\x02\x85q\x03.", 0, 0},
			{"hello.world", 42, 1452200952},
		},
		{
			{"\x80\x02]q\x00X\x0b\x00\x00\x00hello.worldq\x01J\xf8\xd3\x8eVK*\x86q\x02\x86q\x03a.", 0, 0},
			{"hello.world", 42, 1452200952},
		},
		{
			{"\x80\x02]q\x00U\x0bhello.worldq\x01GA\xd5\xa3\xb4\xfe\x00\x00\x00G@E\x00\x00\x00\x00\x00\x00\x86q\x02\x86q\x03a.", 0, 0},
			{"hello.world", 42, 1452200952},
		},
	}

	for testIndex, tt := range table {
		i := 0
		err := ParseMessage([]byte(tt[0].name), func(name string, value float64, timestamp int64) {
			i++
			if tt[i].name != name {
				t.Fatalf("%#v != %#v", name, tt[i].name)
			}
			if tt[i].value != value {
				t.Fatalf("%#v != %#v", value, tt[i].value)
			}
			if tt[i].timestamp != timestamp {
				t.Fatalf("%#v != %#v", timestamp, tt[i].timestamp)
			}
		})

		if err != nil {
			if tt[i+1].name != "" {
				t.Fatalf("unexpected error, test %#d", testIndex)
			}
		} else {
			if i < len(tt)-1 && tt[i+1].name == "" {
				t.Fatalf("error not raised, test %#d", testIndex)
			}
		}
	}
}

func TestMarshalMessage(t *testing.T) {
	unmarshalMessages := func(pkt []byte) ([]Message, error) {
		var msgs []Message
		err := ParseMessage(pkt, func(name string, value float64, timestamp int64) {
			if len(msgs) > 0 && name == msgs[len(msgs)-1].Name {
				msg := &msgs[len(msgs)-1]
				msg.Points = append(msg.Points,
					DataPoint{Timestamp: timestamp, Value: value})
			} else {
				msgs = append(msgs, Message{
					Name:   name,
					Points: []DataPoint{{Timestamp: timestamp, Value: value}},
				})
			}

		})
		if err != nil {
			return nil, err
		}
		return msgs, nil
	}

	table := []struct {
		msgs []Message
	}{
		{
			// One metric with one datapoint
			// [("param1", (1423931224, 60.2))]
			msgs: []Message{
				{
					Name:   "hello.world",
					Points: []DataPoint{{Timestamp: 1452200952, Value: 42}},
				},
			},
		},
		{
			// One metric with one datapoint
			// [("param1", (1423931224, 60.2), (1423931225, 50.2), (1423931226, 40.2))]
			msgs: []Message{
				{
					Name:   "param1",
					Points: []DataPoint{{Timestamp: 1423931224, Value: 60.2}},
				},
			},
		},
		{
			// One metric with multiple datapoints
			msgs: []Message{
				{
					Name: "param1",
					Points: []DataPoint{
						{Timestamp: 1423931224, Value: 60.2},
						{Timestamp: 1423931225, Value: 50.2},
						{Timestamp: 1423931226, Value: 40.2},
					},
				},
			},
		},
		{
			// Multiple metrics with single datapoints
			// [("param1", (1423931224, 60.2)), ("param2", (1423931224, -15))]
			msgs: []Message{
				{
					Name:   "param1",
					Points: []DataPoint{{Timestamp: 1423931224, Value: 60.2}},
				},
				{
					Name:   "param2",
					Points: []DataPoint{{Timestamp: 1423931224, Value: -15}},
				},
			},
		},
		{
			// Complex update
			// [("param1", (1423931224, 60.2), (1423931284, 42)), ("param2", (1423931224, -15))]
			msgs: []Message{
				{
					Name: "param1",
					Points: []DataPoint{
						{Timestamp: 1423931224, Value: 60.2},
						{Timestamp: 1423931284, Value: 42},
					},
				},
				{
					Name:   "param2",
					Points: []DataPoint{{Timestamp: 1423931224, Value: -15}},
				},
			},
		},
	}
	for testIndex, tt := range table {
		data, err := MarshalMessages(tt.msgs)
		if err != nil {
			t.Fatalf("error during marshaling message, test %#d", testIndex)
		}
		got, err := unmarshalMessages(data)
		if !reflect.DeepEqual(got, tt.msgs) {
			t.Errorf("%#v != %#v, test %#d", got, tt.msgs, testIndex)
		}
	}
}

func BenchmarkParseMessage(b *testing.B) {
	goodPickles := [][]byte{
		[]byte("(lp0\n(S'param1'\np1\n(I1423931224\nF60.2\ntp2\ntp3\na."),
		[]byte("(lp0\n(S'param1'\np1\n(I1423931224\nF60.2\ntp2\n(I1423931225\nF50.2\ntp3\n(I1423931226\nF40.2\ntp4\ntp5\na."),
		[]byte("(lp0\n(S'param1'\np1\n(I1423931224\nF60.2\ntp2\ntp3\na(S'param2'\np4\n(I1423931224\nI-15\ntp5\ntp6\na."),
		[]byte("(lp0\n(S'param1'\np1\n(I1423931224\nF60.2\ntp2\n(I1423931284\nI42\ntp3\ntp4\na(S'param2'\np5\n(I1423931224\nI-15\ntp6\ntp7\na."),
	}
	// run the Fib function b.N times
	for n := 0; n < b.N; n++ {
		err := ParseMessage(goodPickles[n%len(goodPickles)], func(string, float64, int64) {})
		if err != nil {
			b.Fatalf("Error raised while benchmarking")
		}
	}
}

func BenchmarkMarshalMessages(b *testing.B) {
	messagesList := [][]Message{
		// One metric with one datapoint
		// [("param1", (1423931224, 60.2))]
		{
			{
				Name:   "hello.world",
				Points: []DataPoint{{Timestamp: 1452200952, Value: 42}},
			},
		},
		// One metric with one datapoint
		// [("param1", (1423931224, 60.2), (1423931225, 50.2), (1423931226, 40.2))]
		{
			{
				Name:   "param1",
				Points: []DataPoint{{Timestamp: 1423931224, Value: 60.2}},
			},
		},
		// One metric with multiple datapoints
		{
			{
				Name: "param1",
				Points: []DataPoint{
					{Timestamp: 1423931224, Value: 60.2},
					{Timestamp: 1423931225, Value: 50.2},
					{Timestamp: 1423931226, Value: 40.2},
				},
			},
		},
		// Multiple metrics with single datapoints
		// [("param1", (1423931224, 60.2)), ("param2", (1423931224, -15))]
		{
			{
				Name:   "param1",
				Points: []DataPoint{{Timestamp: 1423931224, Value: 60.2}},
			},
			{
				Name:   "param2",
				Points: []DataPoint{{Timestamp: 1423931224, Value: -15}},
			},
		},
		// Complex update
		// [("param1", (1423931224, 60.2), (1423931284, 42)), ("param2", (1423931224, -15))]
		{
			{
				Name: "param1",
				Points: []DataPoint{
					{Timestamp: 1423931224, Value: 60.2},
					{Timestamp: 1423931284, Value: 42},
				},
			},
			{
				Name:   "param2",
				Points: []DataPoint{{Timestamp: 1423931224, Value: -15}},
			},
		},
	}
	for n := 0; n < b.N; n++ {
		_, err := MarshalMessages(messagesList[n%len(messagesList)])
		if err != nil {
			b.Fatalf("Error raised while benchmarking")
		}
	}
}
