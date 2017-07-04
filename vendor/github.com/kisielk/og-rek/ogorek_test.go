package ogórek

import (
	"bytes"
	"encoding/hex"
	"io"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func bigInt(s string) *big.Int {
	i := new(big.Int)
	_, ok := i.SetString(s, 10)
	if !ok {
		panic("bigInt")
	}
	return i
}

func TestMarker(t *testing.T) {
	buf := bytes.Buffer{}
	dec := NewDecoder(&buf)
	dec.mark()
	k, err := dec.marker()
	if err != nil {
		t.Error(err)
	}
	if k != 0 {
		t.Error("no marker found")
	}
}

var graphitePickle1, _ = hex.DecodeString("80025d71017d710228550676616c75657371035d71042847407d90000000000047407f100000000000474080e0000000000047409764000000000047409c40000000000047409d88000000000047409f74000000000047409c74000000000047409cdc00000000004740a10000000000004740a0d800000000004740938800000000004740a00e00000000004740988800000000004e4e655505737461727471054a00d87a5255047374657071064a805101005503656e6471074a00f08f5255046e616d657108552d5a5a5a5a2e55555555555555552e43434343434343432e4d4d4d4d4d4d4d4d2e5858585858585858582e545454710975612e")
var graphitePickle2, _ = hex.DecodeString("286c70300a286470310a53277374617274270a70320a49313338333738323430300a73532773746570270a70330a4938363430300a735327656e64270a70340a49313338353136343830300a73532776616c756573270a70350a286c70360a463437332e300a61463439372e300a61463534302e300a6146313439372e300a6146313830382e300a6146313839302e300a6146323031332e300a6146313832312e300a6146313834372e300a6146323137362e300a6146323135362e300a6146313235302e300a6146323035352e300a6146313537302e300a614e614e617353276e616d65270a70370a5327757365722e6c6f67696e2e617265612e6d616368696e652e6d65747269632e6d696e757465270a70380a73612e")
var graphitePickle3, _ = hex.DecodeString("286c70310a286470320a5327696e74657276616c73270a70330a286c70340a7353276d65747269635f70617468270a70350a5327636172626f6e2e6167656e7473270a70360a73532769734c656166270a70370a4930300a7361286470380a67330a286c70390a7367350a5327636172626f6e2e61676772656761746f72270a7031300a7367370a4930300a736128647031310a67330a286c7031320a7367350a5327636172626f6e2e72656c617973270a7031330a7367370a4930300a73612e")

var tests = []struct {
	name     string
	input    string
	expected interface{}
}{
	{"int", "I5\n.", int64(5)},
	{"float", "F1.23\n.", float64(1.23)},
	{"long", "L12321231232131231231L\n.", bigInt("12321231232131231231")},
	{"None", "N.", None{}},
	{"empty tuple", "(t.", Tuple{}},
	{"tuple of two ints", "(I1\nI2\ntp0\n.", Tuple{int64(1), int64(2)}},
	{"nested tuples", "((I1\nI2\ntp0\n(I3\nI4\ntp1\ntp2\n.",
		Tuple{Tuple{int64(1), int64(2)}, Tuple{int64(3), int64(4)}}},
	{"tuple with top 1 items from stack", "I0\n\x85.", Tuple{int64(0)}},
	{"tuple with top 2 items from stack", "I0\nI1\n\x86.", Tuple{int64(0), int64(1)}},
	{"tuple with top 3 items from stack", "I0\nI1\nI2\n\x87.", Tuple{int64(0), int64(1), int64(2)}},
	{"empty list", "(lp0\n.", []interface{}{}},
	{"list of numbers", "(lp0\nI1\naI2\naI3\naI4\na.", []interface{}{int64(1), int64(2), int64(3), int64(4)}},
	{"string", "S'abc'\np0\n.", string("abc")},
	{"unicode", "V\\u65e5\\u672c\\u8a9e\np0\n.", string("日本語")},
	{"unicode2", "V' \\u77e5\\u4e8b\\u5c11\\u65f6\\u70e6\\u607c\\u5c11\\u3001\\u8bc6\\u4eba\\u591a\\u5904\\u662f\\u975e\\u591a\\u3002\n.", string("' 知事少时烦恼少、识人多处是非多。")},
	{"empty dict", "(dp0\n.", make(map[interface{}]interface{})},
	{"dict with strings", "(dp0\nS'a'\np1\nS'1'\np2\nsS'b'\np3\nS'2'\np4\ns.", map[interface{}]interface{}{"a": "1", "b": "2"}},
	{"GLOBAL and REDUCE opcodes", "cfoo\nbar\nS'bing'\n\x85R.", Call{Callable: Class{Module: "foo", Name: "bar"}, Args: []interface{}{"bing"}}},
	{"LONG_BINPUT opcode", "(lr0000I17\na.", []interface{}{int64(17)}},
	{"graphite message1", string(graphitePickle1), []interface{}{map[interface{}]interface{}{"values": []interface{}{float64(473), float64(497), float64(540), float64(1497), float64(1808), float64(1890), float64(2013), float64(1821), float64(1847), float64(2176), float64(2156), float64(1250), float64(2055), float64(1570), None{}, None{}}, "start": int64(1383782400), "step": int64(86400), "end": int64(1385164800), "name": "ZZZZ.UUUUUUUU.CCCCCCCC.MMMMMMMM.XXXXXXXXX.TTT"}}},
	{"graphite message2", string(graphitePickle2), []interface{}{map[interface{}]interface{}{"values": []interface{}{float64(473), float64(497), float64(540), float64(1497), float64(1808), float64(1890), float64(2013), float64(1821), float64(1847), float64(2176), float64(2156), float64(1250), float64(2055), float64(1570), None{}, None{}}, "start": int64(1383782400), "step": int64(86400), "end": int64(1385164800), "name": "user.login.area.machine.metric.minute"}}},
	{"graphite message3", string(graphitePickle3), []interface{}{map[interface{}]interface{}{"intervals": []interface{}{}, "metric_path": "carbon.agents", "isLeaf": false}, map[interface{}]interface{}{"intervals": []interface{}{}, "metric_path": "carbon.aggregator", "isLeaf": false}, map[interface{}]interface{}{"intervals": []interface{}{}, "metric_path": "carbon.relays", "isLeaf": false}}},
	{"too long line", "V28,34,30,55,100,130,87,169,194,202,232,252,267,274,286,315,308,221,358,368,401,406,434,452,475,422,497,530,517,559,400,418,571,578,599,600,625,630,635,647,220,715,736,760,705,785,794,495,808,852,861,863,869,875,890,893,896,922,812,980,1074,1087,1145,1153,1163,1171,445,1195,1203,1242,1255,1274,52,1287,1319,636,1160,1339,1345,1353,1369,1391,1396,1405,1221,1410,1431,1451,1460,1470,1472,1492,1517,1528,419,1530,1532,1535,1573,1547,1574,1437,1594,1595,847,1551,983,1637,1647,1666,1672,1691,1726,1515,1731,1739,1741,1723,1776,1685,505,1624,1436,1890,728,1910,1931,1544,2013,2025,2030,2043,2069,1162,2129,2160,2199,2210,1911,2246,804,2276,1673,2299,2315,2322,2328,2355,2376,2405,1159,2425,2430,2452,1804,2442,2567,2577,1167,2611,2534,1879,2623,2682,2699,2652,2742,2754,2774,2782,2795,2431,2821,2751,2850,2090,513,2898,592,2932,2933,1555,2969,3003,3007,3010,2595,3064,3087,3105,3106,3110,151,3129,3132,304,3173,3205,3233,3245,3279,3302,3307,714,316,3331,3347,3360,3375,3380,3442,2620,3482,3493,3504,3516,3517,3518,3533,3511,2681,3530,3601,3606,3615,1210,3633,3651,3688,3690,3781,1907,3839,3840,3847,3867,3816,3899,3924,2345,3912,3966,982,4040,4056,4076,4084,4105,2649,4171,3873,1415,3567,4188,4221,4227,4231,2279,4250,4253,770,894,4343,4356,4289,4404,4438,2572,3124,4334,2114,3953,4522,4537,4561,4571,641,4629,4640,4664,4687,4702,4709,4740,4605,4746,4768,3856,3980,4814,2984,4895,4908,1249,4944,4947,4979,4988,4995,32,4066,5043,4956,5069,5072,5076,5084,5085,5137,4262,5152,479,5156,3114,1277,5183,5186,1825,5106,5216,963,5239,5252,5218,5284,1980,1972,5352,5364,5294,5379,5387,5391,5397,5419,5434,5468,5471,3350,5510,5522,5525,5538,5554,5573,5597,5610,5615,5624,842,2851,5641,5655,5656,5658,5678,5682,5696,5699,5709,5728,5753,851,5805,3528,5822,801,5855,2929,5871,5899,5918,5925,5927,5931,5935,5939,5958,778,5971,5980,5300,6009,6023,6030,6032,6016,6110,5009,6155,6197,1760,6253,6267,4886,5608,6289,6308,6311,6321,6316,6333,6244,6070,6349,6353,6186,6357,6366,6386,6387,6389,6399,6411,6421,6432,6437,6465,6302,6493,5602,6511,6529,6536,6170,6557,6561,6577,6581,6590,5290,5649,6231,6275,6635,6651,6652,5929,6692,6693,6695,6705,6711,6723,6738,6752,6753,3629,2975,6790,5845,338,6814,6826,6478,6860,6872,6882,880,356,6897,4102,6910,6611,1030,6934,6936,6987,6984,6999,827,6902,7027,7049,7051,4628,7084,7083,7071,7102,7137,5867,7152,6048,2410,3896,7168,7177,7224,6606,7233,1793,7261,7284,7290,7292,5212,7315,6964,3238,355,1969,4256,448,7325,908,2824,2981,3193,3363,3613,5325,6388,2247,1348,72,131,5414,7285,7343,7349,7362,7372,7381,7410,7418,7443,5512,7470,7487,7497,7516,7277,2622,2863,945,4344,3774,1024,2272,7523,4476,256,5643,3164,7539,7540,7489,1932,7559,7575,7602,7605,7609,7608,7619,7204,7652,7663,6907,7672,7654,7674,7687,7718,7745,1202,4030,7797,7801,7799,2924,7871,7873,7900,7907,7911,7912,7917,7923,7935,8007,8017,7636,8084,8087,3686,8114,8153,8158,8171,8175,8182,8205,8222,8225,8229,8232,8234,8244,8247,7256,8279,6929,8285,7040,8328,707,6773,7949,8468,5759,6344,8509,1635\n.", "28,34,30,55,100,130,87,169,194,202,232,252,267,274,286,315,308,221,358,368,401,406,434,452,475,422,497,530,517,559,400,418,571,578,599,600,625,630,635,647,220,715,736,760,705,785,794,495,808,852,861,863,869,875,890,893,896,922,812,980,1074,1087,1145,1153,1163,1171,445,1195,1203,1242,1255,1274,52,1287,1319,636,1160,1339,1345,1353,1369,1391,1396,1405,1221,1410,1431,1451,1460,1470,1472,1492,1517,1528,419,1530,1532,1535,1573,1547,1574,1437,1594,1595,847,1551,983,1637,1647,1666,1672,1691,1726,1515,1731,1739,1741,1723,1776,1685,505,1624,1436,1890,728,1910,1931,1544,2013,2025,2030,2043,2069,1162,2129,2160,2199,2210,1911,2246,804,2276,1673,2299,2315,2322,2328,2355,2376,2405,1159,2425,2430,2452,1804,2442,2567,2577,1167,2611,2534,1879,2623,2682,2699,2652,2742,2754,2774,2782,2795,2431,2821,2751,2850,2090,513,2898,592,2932,2933,1555,2969,3003,3007,3010,2595,3064,3087,3105,3106,3110,151,3129,3132,304,3173,3205,3233,3245,3279,3302,3307,714,316,3331,3347,3360,3375,3380,3442,2620,3482,3493,3504,3516,3517,3518,3533,3511,2681,3530,3601,3606,3615,1210,3633,3651,3688,3690,3781,1907,3839,3840,3847,3867,3816,3899,3924,2345,3912,3966,982,4040,4056,4076,4084,4105,2649,4171,3873,1415,3567,4188,4221,4227,4231,2279,4250,4253,770,894,4343,4356,4289,4404,4438,2572,3124,4334,2114,3953,4522,4537,4561,4571,641,4629,4640,4664,4687,4702,4709,4740,4605,4746,4768,3856,3980,4814,2984,4895,4908,1249,4944,4947,4979,4988,4995,32,4066,5043,4956,5069,5072,5076,5084,5085,5137,4262,5152,479,5156,3114,1277,5183,5186,1825,5106,5216,963,5239,5252,5218,5284,1980,1972,5352,5364,5294,5379,5387,5391,5397,5419,5434,5468,5471,3350,5510,5522,5525,5538,5554,5573,5597,5610,5615,5624,842,2851,5641,5655,5656,5658,5678,5682,5696,5699,5709,5728,5753,851,5805,3528,5822,801,5855,2929,5871,5899,5918,5925,5927,5931,5935,5939,5958,778,5971,5980,5300,6009,6023,6030,6032,6016,6110,5009,6155,6197,1760,6253,6267,4886,5608,6289,6308,6311,6321,6316,6333,6244,6070,6349,6353,6186,6357,6366,6386,6387,6389,6399,6411,6421,6432,6437,6465,6302,6493,5602,6511,6529,6536,6170,6557,6561,6577,6581,6590,5290,5649,6231,6275,6635,6651,6652,5929,6692,6693,6695,6705,6711,6723,6738,6752,6753,3629,2975,6790,5845,338,6814,6826,6478,6860,6872,6882,880,356,6897,4102,6910,6611,1030,6934,6936,6987,6984,6999,827,6902,7027,7049,7051,4628,7084,7083,7071,7102,7137,5867,7152,6048,2410,3896,7168,7177,7224,6606,7233,1793,7261,7284,7290,7292,5212,7315,6964,3238,355,1969,4256,448,7325,908,2824,2981,3193,3363,3613,5325,6388,2247,1348,72,131,5414,7285,7343,7349,7362,7372,7381,7410,7418,7443,5512,7470,7487,7497,7516,7277,2622,2863,945,4344,3774,1024,2272,7523,4476,256,5643,3164,7539,7540,7489,1932,7559,7575,7602,7605,7609,7608,7619,7204,7652,7663,6907,7672,7654,7674,7687,7718,7745,1202,4030,7797,7801,7799,2924,7871,7873,7900,7907,7911,7912,7917,7923,7935,8007,8017,7636,8084,8087,3686,8114,8153,8158,8171,8175,8182,8205,8222,8225,8229,8232,8234,8244,8247,7256,8279,6929,8285,7040,8328,707,6773,7949,8468,5759,6344,8509,1635"},
	{"FRAME Opcode and int", "\x95\x00\x00\x00\x00\x00\x00\x00\x00I5\n.", int64(5)},
	{"SHORTBINUNICODE opcode", "\x8c\t\xe6\x97\xa5\xe6\x9c\xac\xe8\xaa\x9e\x94.", "日本語"},
	{"STACK_GLOBAL opcode", "S'foo'\nS'bar'\n\x93.", Class{Module: "foo", Name: "bar"}},
}

func TestDecode(t *testing.T) {
	for _, test := range tests {
		// decode(input) -> expected
		buf := bytes.NewBufferString(test.input)
		dec := NewDecoder(buf)
		v, err := dec.Decode()
		if err != nil {
			t.Error(err)
		}

		if !reflect.DeepEqual(v, test.expected) {
			t.Errorf("%s: decode:\nhave: %#v\nwant: %#v", test.name, v, test.expected)
		}

		// decode more -> EOF
		v, err = dec.Decode()
		if !(v == nil && err == io.EOF) {
			t.Errorf("%s: decode: no EOF at end: v = %#v  err = %#v", test.name, v, err)
		}

		// expected (= decoded(input)) -> encode -> decode = identity
		buf.Reset()
		enc := NewEncoder(buf)
		err = enc.Encode(test.expected)
		if err != nil {
			t.Errorf("%s: encode(expected): %v", test.name, err)
		} else {
			dec := NewDecoder(buf)
			v, err := dec.Decode()
			if err != nil {
				t.Error(err)
			}

			if !reflect.DeepEqual(v, test.expected) {
				t.Errorf("%s: expected -> decode -> encode != identity\nhave: %#v\nwant: %#v", test.name, v, test.expected)
			}
		}

		// for truncated input io.ErrUnexpectedEOF must be returned
		for l := len(test.input) - 1; l > 0; l-- {
			buf := bytes.NewBufferString(test.input[:l])
			dec := NewDecoder(buf)
			//println(test.name, l)
			v, err := dec.Decode()
			// strconv.UnquoteChar used in loadUnicode always returns
			// SyntaxError, at least unless the following CL is accepted:
			// https://go-review.googlesource.com/37052
			if err == strconv.ErrSyntax && strings.HasPrefix(test.name, "unicode") {
				err = io.ErrUnexpectedEOF
			}
			if !(v == nil && err == io.ErrUnexpectedEOF) {
				t.Errorf("%s: no ErrUnexpectedEOF on [:%d] truncated stream: v = %#v  err = %#v", test.name, l, v, err)
			}
		}

		// by using input with omitted prefix we can test how code handles pickle stack overflow:
		// it must not panic
		for i := 0; i < len(test.input); i++ {
			buf := bytes.NewBufferString(test.input[i:])
			dec := NewDecoder(buf)
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s: panic on input[%d:]: %v", test.name, i, r)
					}
				}()
				dec.Decode()
			}()
		}
	}
}

// test that .Decode() decodes only until stop opcode, and can continue
// decoding further on next call
func TestDecodeMultiple(t *testing.T) {
	input := "I5\n.I7\n.N."
	expected := []interface{}{int64(5), int64(7), None{}}

	buf := bytes.NewBufferString(input)
	dec := NewDecoder(buf)

	for i, objOk := range expected {
		obj, err := dec.Decode()
		if err != nil {
			t.Errorf("step #%v: %v", i, err)
		}

		if !reflect.DeepEqual(obj, objOk) {
			t.Errorf("step #%v: %q  ; want %q", i, obj, objOk)
		}
	}

	obj, err := dec.Decode()
	if !(obj == nil && err == io.EOF) {
		t.Errorf("decode: no EOF at end: obj = %#v  err = %#v", obj, err)
	}
}

func TestZeroLengthData(t *testing.T) {
	data := ""
	output, err := decodeLong(data)
	if err != nil {
		t.Errorf("Error from decodeLong - %v\n", err)
	}
	if output.BitLen() > 0 {
		t.Fail()
	}
}

func TestValue1(t *testing.T) {
	data := "\xff\x00"
	output, err := decodeLong(data)
	if err != nil {
		t.Errorf("Error from decodeLong - %v\n", err)
	}
	target := big.NewInt(255)
	if target.Cmp(output) != 0 {
		t.Fail()
	}
}

func TestValue2(t *testing.T) {
	data := "\xff\x7f"
	output, err := decodeLong(data)
	if err != nil {
		t.Errorf("Error from decodeLong - %v\n", err)
	}
	target := big.NewInt(32767)
	if target.Cmp(output) != 0 {
		t.Fail()
	}
}

func TestValue3(t *testing.T) {
	data := "\x00\xff"
	output, err := decodeLong(data)
	if err != nil {
		t.Errorf("Error from decodeLong - %v\n", err)
	}
	target := big.NewInt(256)
	target.Neg(target)
	if target.Cmp(output) != 0 {
		t.Logf("\nGot %v\nExpecting %v\n", output, target)
		t.Fail()
	}
}

func TestValue4(t *testing.T) {
	data := "\x00\x80"
	output, err := decodeLong(data)
	if err != nil {
		t.Errorf("Error from decodeLong - %v\n", err)
	}
	target := big.NewInt(32768)
	target.Neg(target)
	if target.Cmp(output) != 0 {
		t.Logf("\nGot %v\nExpecting %v\n", output, target)
		t.Fail()
	}
}

func TestValue5(t *testing.T) {
	data := "\x80"
	output, err := decodeLong(data)
	if err != nil {
		t.Errorf("Error from decodeLong - %v\n", err)
	}
	target := big.NewInt(128)
	target.Neg(target)
	if target.Cmp(output) != 0 {
		t.Logf("\nGot %v\nExpecting %v\n", output, target)
		t.Fail()
	}
}

func TestValue6(t *testing.T) {
	data := "\x7f"
	output, err := decodeLong(data)
	if err != nil {
		t.Errorf("Error from decodeLong - %v\n", err)
	}
	target := big.NewInt(127)
	if target.Cmp(output) != 0 {
		t.Fail()
	}
}

func BenchmarkSpeed(b *testing.B) {
	for i := 0; i < b.N; i++ {
		data := "\x00\x80"
		_, err := decodeLong(data)
		if err != nil {
			b.Errorf("Error from decodeLong - %v\n", err)
		}
	}
}

func TestMemoOpCode(t *testing.T) {
	buf := bytes.NewBufferString("I5\n\x94.")
	dec := NewDecoder(buf)
	_, err := dec.Decode()
	if err != nil {
		t.Errorf("Error from TestMemoOpCode - %v\n", err)
	}
	if dec.memo["0"] != int64(5) {
		t.Errorf("Error from TestMemoOpCode - Top stack value was not added to memo")
	}

}

// verify that decode of erroneous input produces error
func TestDecodeError(t *testing.T) {
	testv := []string{
		// all kinds of opcodes to read memo but key is not there
		"}g1\n.",
		"}h\x01.",
		"}j\x01\x02\x03\x04.",

		// invalid long format
		"L123\n.",
		"L12qL\n.",
	}
	for _, tt := range testv {
		buf := bytes.NewBufferString(tt)
		dec := NewDecoder(buf)
		v, err := dec.Decode()
		if !(v == nil && err != nil) {
			t.Errorf("%q: no decode error  ; got %#v, %#v", tt, v, err)
		}
	}
}

func TestFuzzCrashers(t *testing.T) {
	crashers := []string{
		"(dS''\n(lc\n\na2a2a22aasS''\na",
		"S\n",
		"((dd",
		"}}}s",
		"(((ld",
		"(dS''\n(lp4\nsg4\n(s",
		"}((tu",
		"}((du",
		"(c\n\nc\n\n\x85Rd",
		"}(U\x040000u",
		"(\x88d",
	}

	for _, c := range crashers {
		buf := bytes.NewBufferString(c)
		dec := NewDecoder(buf)
		dec.Decode()
	}
}

func BenchmarkDecode(b *testing.B) {
	// prepare one large pickle stream from all test pickles
	input := make([]byte, 0)
	for _, test := range tests {
		input = append(input, test.input...)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := bytes.NewBuffer(input)
		dec := NewDecoder(buf)

		j := 0
		for ; ; j++ {
			_, err := dec.Decode()
			if err != nil {
				if err == io.EOF {
					break
				}
				b.Fatal(err)
			}
		}

		if j != len(tests) {
			b.Fatalf("unexpected # of decode steps: got %v  ; want %v", j, len(tests))
		}
	}
}

func BenchmarkEncode(b *testing.B) {
	// prepare one large slice from all test vector values
	input := make([]interface{}, 0)
	approxOutSize := 0
	for _, test := range tests {
		input = append(input, test.expected)
		approxOutSize += len(test.input)
	}

	buf := bytes.NewBuffer(make([]byte, approxOutSize))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		enc := NewEncoder(buf)
		err := enc.Encode(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}
