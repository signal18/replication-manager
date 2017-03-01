// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

// +build cairo

package expr

import (
	"bytes"
	"fmt"
	"image/color"
	"io/ioutil"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"bitbucket.org/tebeka/strftime"
	"github.com/evmar/gocairo/cairo"
	"github.com/gogo/protobuf/proto"
	pb "github.com/tanji/replication-manager/graphite/carbonzipper/carbonzipperpb"
)

const haveGraphSupport = true

type graphOptions struct {
	// extra options
	xStep     float64
	color     string
	alpha     float64
	lineWidth float64
	invisible bool

	drawAsInfinite bool
	secondYAxis    bool
	dashed         float64
	hasAlpha       bool
	stacked        bool
	stackName      string
}

type HAlign int

const (
	HAlignLeft   HAlign = 1
	HAlignCenter        = 2
	HAlignRight         = 4
)

type VAlign int

const (
	VAlignTop      VAlign = 8
	VAlignCenter          = 16
	VAlignBottom          = 32
	VAlignBaseline        = 64
)

type LineMode int

const (
	LineModeSlope     LineMode = 1
	LineModeStaircase          = 2
	LineModeConnected          = 4
)

type AreaMode int

const (
	AreaModeNone    AreaMode = 1
	AreaModeFirst            = 2
	AreaModeAll              = 4
	AreaModeStacked          = 8
)

type PieMode int

const (
	PieModeMaximum PieMode = 1
	PieModeMinimum         = 2
	PieModeAverage         = 4
)

type YAxisSide int

const (
	YAxisSideRight YAxisSide = 1
	YAxisSideLeft            = 2
)

type YCoordSide int

const (
	YCoordSideLeft  YCoordSide = 1
	YCoordSideRight            = 2
	YCoordSideNone             = 3
)

type TimeUnit int32

const (
	Second TimeUnit = 1
	Minute          = 60
	Hour            = 60 * Minute
	Day             = 24 * Hour
)

var defaultColorList = []string{"blue", "green", "red", "purple", "brown", "yellow", "aqua", "grey", "magenta", "pink", "gold", "rose"}

type unitPrefix struct {
	prefix string
	size   uint64
}

var unitSystems = map[string][]unitPrefix{
	"binary": {
		{"Pi", 1125899906842624}, // 1024^5
		{"Ti", 1099511627776},    // 1024^4
		{"Gi", 1073741824},       // 1024^3
		{"Mi", 1048576},          // 1024^2
		{"Ki", 1024},
	},
	"si": {
		{"P", 1000000000000000}, // 1000^5
		{"T", 1000000000000},    // 1000^4
		{"G", 1000000000},       // 1000^3
		{"M", 1000000},          // 1000^2
		{"K", 1000},
	},
}

type xAxisStruct struct {
	seconds       float64
	minorGridUnit TimeUnit
	minorGridStep float64
	majorGridUnit TimeUnit
	majorGridStep int32
	labelUnit     TimeUnit
	labelStep     int32
	format        string
	maxInterval   int32
}

var xAxisConfigs = []xAxisStruct{
	{
		seconds:       0.00,
		minorGridUnit: Second,
		minorGridStep: 5,
		majorGridUnit: Minute,
		majorGridStep: 1,
		labelUnit:     Second,
		labelStep:     5,
		format:        "%H:%M:%S",
		maxInterval:   10 * Minute,
	},
	{
		seconds:       0.07,
		minorGridUnit: Second,
		minorGridStep: 10,
		majorGridUnit: Minute,
		majorGridStep: 1,
		labelUnit:     Second,
		labelStep:     10,
		format:        "%H:%M:%S",
		maxInterval:   20 * Minute,
	},
	{
		seconds:       0.14,
		minorGridUnit: Second,
		minorGridStep: 15,
		majorGridUnit: Minute,
		majorGridStep: 1,
		labelUnit:     Second,
		labelStep:     15,
		format:        "%H:%M:%S",
		maxInterval:   30 * Minute,
	},
	{
		seconds:       0.27,
		minorGridUnit: Second,
		minorGridStep: 30,
		majorGridUnit: Minute,
		majorGridStep: 2,
		labelUnit:     Minute,
		labelStep:     1,
		format:        "%H:%M",
		maxInterval:   2 * Hour,
	},
	{
		seconds:       0.5,
		minorGridUnit: Minute,
		minorGridStep: 1,
		majorGridUnit: Minute,
		majorGridStep: 2,
		labelUnit:     Minute,
		labelStep:     1,
		format:        "%H:%M",
		maxInterval:   2 * Hour,
	},
	{
		seconds:       1.2,
		minorGridUnit: Minute,
		minorGridStep: 1,
		majorGridUnit: Minute,
		majorGridStep: 4,
		labelUnit:     Minute,
		labelStep:     2,
		format:        "%H:%M",
		maxInterval:   3 * Hour,
	},
	{
		seconds:       2,
		minorGridUnit: Minute,
		minorGridStep: 1,
		majorGridUnit: Minute,
		majorGridStep: 10,
		labelUnit:     Minute,
		labelStep:     5,
		format:        "%H:%M",
		maxInterval:   6 * Hour,
	},
	{
		seconds:       5,
		minorGridUnit: Minute,
		minorGridStep: 2,
		majorGridUnit: Minute,
		majorGridStep: 10,
		labelUnit:     Minute,
		labelStep:     10,
		format:        "%H:%M",
		maxInterval:   12 * Hour,
	},
	{
		seconds:       10,
		minorGridUnit: Minute,
		minorGridStep: 5,
		majorGridUnit: Minute,
		majorGridStep: 20,
		labelUnit:     Minute,
		labelStep:     20,
		format:        "%H:%M",
		maxInterval:   Day,
	},
	{
		seconds:       30,
		minorGridUnit: Minute,
		minorGridStep: 10,
		majorGridUnit: Hour,
		majorGridStep: 1,
		labelUnit:     Hour,
		labelStep:     1,
		format:        "%H:%M",
		maxInterval:   2 * Day,
	},
	{
		seconds:       60,
		minorGridUnit: Minute,
		minorGridStep: 30,
		majorGridUnit: Hour,
		majorGridStep: 2,
		labelUnit:     Hour,
		labelStep:     2,
		format:        "%H:%M",
		maxInterval:   2 * Day,
	},
	{
		seconds:       100,
		minorGridUnit: Hour,
		minorGridStep: 2,
		majorGridUnit: Hour,
		majorGridStep: 4,
		labelUnit:     Hour,
		labelStep:     4,
		format:        "%a %I%p", // BUG(dgryski): should be %l, but limitation of strftime library
		maxInterval:   6 * Day,
	},
	{
		seconds:       255,
		minorGridUnit: Hour,
		minorGridStep: 6,
		majorGridUnit: Hour,
		majorGridStep: 12,
		labelUnit:     Hour,
		labelStep:     12,
		format:        "%a %I%p", // BUG(dgryski): should be %l, but limitation of strftime library
		maxInterval:   10 * Day,
	},
	{
		seconds:       600,
		minorGridUnit: Hour,
		minorGridStep: 6,
		majorGridUnit: Day,
		majorGridStep: 1,
		labelUnit:     Day,
		labelStep:     1,
		format:        "%m/%d",
		maxInterval:   14 * Day,
	},
	{
		seconds:       1200,
		minorGridUnit: Hour,
		minorGridStep: 12,
		majorGridUnit: Day,
		majorGridStep: 1,
		labelUnit:     Day,
		labelStep:     1,
		format:        "%m/%d",
		maxInterval:   365 * Day,
	},
	{
		seconds:       2000,
		minorGridUnit: Day,
		minorGridStep: 1,
		majorGridUnit: Day,
		majorGridStep: 2,
		labelUnit:     Day,
		labelStep:     2,
		format:        "%m/%d",
		maxInterval:   365 * Day,
	},
	{
		seconds:       4000,
		minorGridUnit: Day,
		minorGridStep: 2,
		majorGridUnit: Day,
		majorGridStep: 4,
		labelUnit:     Day,
		labelStep:     4,
		format:        "%m/%d",
		maxInterval:   365 * Day,
	},
	{
		seconds:       8000,
		minorGridUnit: Day,
		minorGridStep: 3.5,
		majorGridUnit: Day,
		majorGridStep: 7,
		labelUnit:     Day,
		labelStep:     7,
		format:        "%m/%d",
		maxInterval:   365 * Day,
	},
	{
		seconds:       16000,
		minorGridUnit: Day,
		minorGridStep: 7,
		majorGridUnit: Day,
		majorGridStep: 14,
		labelUnit:     Day,
		labelStep:     14,
		format:        "%m/%d",
		maxInterval:   365 * Day,
	},
	{
		seconds:       32000,
		minorGridUnit: Day,
		minorGridStep: 15,
		majorGridUnit: Day,
		majorGridStep: 30,
		labelUnit:     Day,
		labelStep:     30,
		format:        "%m/%d",
		maxInterval:   365 * Day,
	},
	{
		seconds:       64000,
		minorGridUnit: Day,
		minorGridStep: 30,
		majorGridUnit: Day,
		majorGridStep: 60,
		labelUnit:     Day,
		labelStep:     60,
		format:        "%m/%d %Y",
		maxInterval:   365 * Day,
	},
	{
		seconds:       100000,
		minorGridUnit: Day,
		minorGridStep: 60,
		majorGridUnit: Day,
		majorGridStep: 120,
		labelUnit:     Day,
		labelStep:     120,
		format:        "%m/%d %Y",
		maxInterval:   365 * Day,
	},
	{
		seconds:       120000,
		minorGridUnit: Day,
		minorGridStep: 120,
		majorGridUnit: Day,
		majorGridStep: 240,
		labelUnit:     Day,
		labelStep:     240,
		format:        "%m/%d %Y",
		maxInterval:   365 * Day,
	},
}

func getInt(s string, def int) int {
	if s == "" {
		return def
	}

	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return def
	}

	return int(n)
}

func getLogBase(s string) float64 {
	if s == "e" {
		return math.E
	}

	b, err := strconv.ParseFloat(s, 64)
	if err != nil || b < 1 {
		return 0
	}

	return b
}

func getFloatArray(s string, def []float64) []float64 {
	if s == "" {
		return def
	}

	ss := strings.Split(s, ",")
	var fs []float64
	for _, v := range ss {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return def
		}
		fs = append(fs, f)
	}

	return fs
}

func getStringArray(s string, def []string) []string {
	if s == "" {
		return def
	}

	ss := strings.Split(s, ",")
	var strs []string
	for _, v := range ss {
		strs = append(strs, strings.TrimSpace(v))
	}

	return strs
}

func getFontItalic(s string) cairo.FontSlant {
	if TruthyBool(s) {
		return cairo.FontSlantItalic
	}

	return cairo.FontSlantNormal
}

func getFontWeight(s string) cairo.FontWeight {
	if TruthyBool(s) {
		return cairo.FontWeightBold
	}

	return cairo.FontWeightNormal
}

func getLineMode(s string, def LineMode) LineMode {
	if s == "" {
		return def
	}

	if s == "slope" {
		return LineModeSlope
	}
	if s == "staircase" {
		return LineModeStaircase
	}

	return LineModeConnected
}

func getAreaMode(s string, def AreaMode) AreaMode {
	if s == "" {
		return def
	}

	switch s {
	case "first":
		return AreaModeFirst
	case "all":
		return AreaModeAll
	case "stacked":
		return AreaModeStacked

	}

	return AreaModeNone
}

func getPieMode(s string, def PieMode) PieMode {
	if s == "" {
		return def
	}

	if s == "maximum" {
		return PieModeMaximum
	}
	if s == "minimum" {
		return PieModeMinimum
	}

	return PieModeAverage
}

func getAxisSide(s string, def YAxisSide) YAxisSide {
	if s == "" {
		return def
	}

	if s == "right" {
		return YAxisSideRight
	}

	return YAxisSideLeft
}

func getTimeZone(s string, def *time.Location) *time.Location {
	if s == "" {
		return def
	}

	tz, err := time.LoadLocation(s)
	if err != nil {
		return def
	}

	return tz
}

type Area struct {
	xmin float64
	xmax float64
	ymin float64
	ymax float64
}

type Params struct {
	width      float64
	height     float64
	margin     int
	logBase    float64
	fgColor    color.RGBA
	bgColor    color.RGBA
	majorLine  color.RGBA
	minorLine  color.RGBA
	fontName   string
	fontSize   float64
	fontBold   cairo.FontWeight
	fontItalic cairo.FontSlant

	graphOnly   bool
	hideLegend  bool
	hideGrid    bool
	hideAxes    bool
	hideYAxis   bool
	hideXAxis   bool
	yAxisSide   YAxisSide
	title       string
	vtitle      string
	vtitleRight string
	tz          *time.Location
	timeRange   int32
	startTime   int32
	endTime     int32

	lineMode       LineMode
	areaMode       AreaMode
	areaAlpha      float64
	pieMode        PieMode
	colorList      []string
	lineWidth      float64
	connectedLimit int
	hasStack       bool

	yMin   float64
	yMax   float64
	xMin   float64
	xMax   float64
	yStep  float64
	xStep  float64
	minorY int

	yTop           float64
	yBottom        float64
	ySpan          float64
	graphHeight    float64
	graphWidth     int
	yScaleFactor   float64
	yUnitSystem    string
	yDivisors      []float64
	yLabelValues   []float64
	yLabels        []string
	yLabelWidth    float64
	xScaleFactor   float64
	xFormat        string
	xLabelStep     int32
	xMinorGridStep int32
	xMajorGridStep int32

	minorGridLineColor string
	majorGridLineColor string

	yTopL         float64
	yBottomL      float64
	yLabelValuesL []float64
	yLabelsL      []string
	yLabelWidthL  float64
	yTopR         float64
	yBottomR      float64
	yLabelValuesR []float64
	yLabelsR      []string
	yLabelWidthR  float64
	yStepL        float64
	yStepR        float64
	ySpanL        float64
	ySpanR        float64
	yScaleFactorL float64
	yScaleFactorR float64

	yMaxLeft    float64
	yLimitLeft  float64
	yMaxRight   float64
	yLimitRight float64
	yMinLeft    float64
	yMinRight   float64

	dataLeft  []*MetricData
	dataRight []*MetricData

	rightWidth  float64
	rightDashed bool
	rightColor  string
	leftWidth   float64
	leftDashed  bool
	leftColor   string

	area        Area
	isPng       bool // TODO: png and svg use the same code
	fontExtents cairo.FontExtents

	uniqueLegend   bool
	secondYAxis    bool
	drawNullAsZero bool
	drawAsInfinite bool

	xConf xAxisStruct
}

type cairoSurfaceContext struct {
	context *cairo.Context
}

type cairoBackend int

const (
	cairoPNG cairoBackend = iota
	cairoSVG
)

func evalExprGraph(e *expr, from, until int32, values map[MetricRequest][]*MetricData) ([]*MetricData, error) {

	switch e.target {

	case "color": // color(seriesList, theColor)
		arg, err := getSeriesArg(e.args[0], from, until, values)
		if err != nil {
			return nil, err
		}

		color, err := getStringArg(e, 1) // get color
		if err != nil {
			return nil, err
		}

		var results []*MetricData

		for _, a := range arg {
			r := *a
			r.color = color
			results = append(results, &r)
		}

		return results, nil

	case "stacked": // stacked(seriesList, stackname="__DEFAULT__")
		arg, err := getSeriesArg(e.args[0], from, until, values)
		if err != nil {
			return nil, err
		}

		stackName, err := getStringNamedOrPosArgDefault(e, "stackname", 1, defaultStackName)
		if err != nil {
			return nil, err
		}

		var results []*MetricData

		for _, a := range arg {
			r := *a
			r.stacked = true
			r.stackName = stackName
			results = append(results, &r)
		}

		return results, nil

	case "areaBetween":
		arg, err := getSeriesArg(e.args[0], from, until, values)
		if err != nil {
			return nil, err
		}

		if len(arg) != 2 {
			return nil, fmt.Errorf("areaBetween needs exactly two arguments (%d given)", len(arg))
		}

		name := proto.String(fmt.Sprintf("%s(%s)", e.target, e.argString))

		lower := *arg[0]
		lower.stacked = true
		lower.stackName = defaultStackName
		lower.invisible = true
		lower.Name = name

		upper := *arg[1]
		upper.stacked = true
		upper.stackName = defaultStackName
		upper.Name = name

		vals := make([]float64, len(upper.Values))
		absent := make([]bool, len(upper.Values))

		for i, v := range upper.Values {
			if upper.IsAbsent[i] || lower.IsAbsent[i] {
				absent[i] = true
				continue
			}

			vals[i] = v - lower.Values[i]
		}

		upper.Values = vals
		upper.IsAbsent = absent

		return []*MetricData{&lower, &upper}, nil

	case "alpha": // alpha(seriesList, theAlpha)
		arg, err := getSeriesArg(e.args[0], from, until, values)
		if err != nil {
			return nil, err
		}

		alpha, err := getFloatArg(e, 1)
		if err != nil {
			return nil, err
		}

		var results []*MetricData

		for _, a := range arg {
			r := *a
			r.alpha = alpha
			r.hasAlpha = true
			results = append(results, &r)
		}

		return results, nil

	case "dashed", "drawAsInfinite", "secondYAxis":
		arg, err := getSeriesArg(e.args[0], from, until, values)
		if err != nil {
			return nil, err
		}

		var results []*MetricData

		for _, a := range arg {
			r := *a
			r.Name = proto.String(fmt.Sprintf("%s(%s)", e.target, a.GetName()))

			switch e.target {
			case "dashed":
				d, err := getFloatArgDefault(e, 1, 2.5)
				if err != nil {
					return nil, err
				}
				r.dashed = d
			case "drawAsInfinite":
				r.drawAsInfinite = true
			case "secondYAxis":
				r.secondYAxis = true
			}

			results = append(results, &r)
		}
		return results, nil

	case "threshold": // threshold(value, label=None, color=None)
		// XXX does not match graphite's signature
		// BUG(nnuss): the signature *does* match but there is an edge case because of named argument handling if you use it *just* wrong:
		//			   threshold(value, "gold", label="Aurum")
		//			   will result in:
		//			   value = value
		//			   label = "Aurum" (by named argument)
		//			   color = "" (by default as len(positionalArgs) == 2 and there is no named 'color' arg)

		value, err := getFloatArg(e, 0)

		if err != nil {
			return nil, err
		}

		name, err := getStringNamedOrPosArgDefault(e, "label", 1, fmt.Sprintf("%g", value))
		if err != nil {
			return nil, err
		}

		color, err := getStringNamedOrPosArgDefault(e, "color", 2, "")
		if err != nil {
			return nil, err
		}

		p := MetricData{
			FetchResponse: pb.FetchResponse{
				Name:      proto.String(name),
				StartTime: proto.Int32(from),
				StopTime:  proto.Int32(until),
				StepTime:  proto.Int32(until - from),
				Values:    []float64{value, value},
				IsAbsent:  []bool{false, false},
			},
			graphOptions: graphOptions{color: color},
		}

		return []*MetricData{&p}, nil

	}

	return nil, errUnknownFunction(e.target)
}

func MarshalSVG(r *http.Request, results []*MetricData) []byte {
	return marshalCairo(r, results, cairoSVG)
}

func MarshalPNG(r *http.Request, results []*MetricData) []byte {
	return marshalCairo(r, results, cairoPNG)
}

func marshalCairo(r *http.Request, results []*MetricData, backend cairoBackend) []byte {
	var params = Params{
		width:          getFloat64(r.FormValue("width"), 330),
		height:         getFloat64(r.FormValue("height"), 250),
		margin:         getInt(r.FormValue("margin"), 10),
		logBase:        getLogBase(r.FormValue("logBase")),
		fgColor:        string2RGBA(getString(r.FormValue("fgcolor"), "white")),
		bgColor:        string2RGBA(getString(r.FormValue("bgcolor"), "black")),
		majorLine:      string2RGBA(getString(r.FormValue("majorLine"), "rose")),
		minorLine:      string2RGBA(getString(r.FormValue("minorLine"), "grey")),
		fontName:       getString(r.FormValue("fontName"), "Sans"),
		fontSize:       getFloat64(r.FormValue("fontSize"), 10.0),
		fontBold:       getFontWeight(r.FormValue("fontBold")),
		fontItalic:     getFontItalic(r.FormValue("fontItalic")),
		graphOnly:      getBool(r.FormValue("graphOnly"), false),
		hideLegend:     getBool(r.FormValue("hideLegend"), false),
		hideGrid:       getBool(r.FormValue("hideGrid"), false),
		hideAxes:       getBool(r.FormValue("hideAxes"), false),
		hideYAxis:      getBool(r.FormValue("hideYAxis"), false),
		hideXAxis:      getBool(r.FormValue("hideXAxis"), false),
		yAxisSide:      getAxisSide(r.FormValue("yAxisSide"), YAxisSideLeft),
		connectedLimit: getInt(r.FormValue("connectedLimit"), math.MaxUint32),
		lineMode:       getLineMode(r.FormValue("lineMode"), LineModeSlope),
		areaMode:       getAreaMode(r.FormValue("areaMode"), AreaModeNone),
		areaAlpha:      getFloat64(r.FormValue("areaAlpha"), math.NaN()),
		pieMode:        getPieMode(r.FormValue("pieMode"), PieModeAverage),
		lineWidth:      getFloat64(r.FormValue("lineWidth"), 1.2),

		rightWidth:  getFloat64(r.FormValue("rightWidth"), 1.2),
		rightDashed: getBool(r.FormValue("rightDashed"), false),
		rightColor:  getString(r.FormValue("rightColor"), ""),

		leftWidth:  getFloat64(r.FormValue("leftWidth"), 1.2),
		leftDashed: getBool(r.FormValue("leftDashed"), false),
		leftColor:  getString(r.FormValue("leftColor"), ""),

		title:       getString(r.FormValue("title"), ""),
		vtitle:      getString(r.FormValue("vtitle"), ""),
		vtitleRight: getString(r.FormValue("vtitleRight"), ""),
		tz:          getTimeZone(r.FormValue("tz"), time.Local),

		colorList: getStringArray(r.FormValue("colorList"), defaultColorList),
		isPng:     true,

		majorGridLineColor: getString(r.FormValue("majorGridLineColor"), "rose"),
		minorGridLineColor: getString(r.FormValue("minorGridLineColor"), "grey"),

		uniqueLegend:   getBool(r.FormValue("uniqueLegend"), false),
		drawNullAsZero: getBool(r.FormValue("drawNullAsZero"), false),
		drawAsInfinite: getBool(r.FormValue("drawAsInfinite"), false),
		yMin:           getFloat64(r.FormValue("yMin"), math.NaN()),
		yMax:           getFloat64(r.FormValue("yMax"), math.NaN()),
		yStep:          getFloat64(r.FormValue("yStep"), math.NaN()),
		xMin:           getFloat64(r.FormValue("xMin"), math.NaN()),
		xMax:           getFloat64(r.FormValue("xMax"), math.NaN()),
		xStep:          getFloat64(r.FormValue("xStep"), math.NaN()),
		xFormat:        getString(r.FormValue("xFormat"), ""),
		minorY:         getInt(r.FormValue("minorY"), 1),

		yMinLeft:    getFloat64(r.FormValue("yMinLeft"), math.NaN()),
		yMinRight:   getFloat64(r.FormValue("yMinRight"), math.NaN()),
		yMaxLeft:    getFloat64(r.FormValue("yMaxLeft"), math.NaN()),
		yMaxRight:   getFloat64(r.FormValue("yMaxRight"), math.NaN()),
		yStepL:      getFloat64(r.FormValue("yStepLeft"), math.NaN()),
		yStepR:      getFloat64(r.FormValue("yStepRight"), math.NaN()),
		yLimitLeft:  getFloat64(r.FormValue("yLimitLeft"), math.NaN()),
		yLimitRight: getFloat64(r.FormValue("yLimitRight"), math.NaN()),

		yUnitSystem: getString(r.FormValue("yUnitSystem"), "si"),
		yDivisors:   getFloatArray(r.FormValue("yDivisors"), []float64{4, 5, 6}),
	}

	margin := float64(params.margin)
	params.area.xmin = margin + 10
	params.area.xmax = params.width - margin
	params.area.ymin = margin
	params.area.ymax = params.height - margin
	params.hideLegend = getBool(r.FormValue("hideLegend"), len(results) > 10)

	var cr cairoSurfaceContext
	var surface *cairo.Surface
	var tmpfile *os.File
	switch backend {
	case cairoSVG:
		var err error
		tmpfile, err = ioutil.TempFile("/dev/shm", "cairosvg")
		if err != nil {
			return nil
		}
		defer os.Remove(tmpfile.Name())
		s := cairo.SVGSurfaceCreate(tmpfile.Name(), params.width, params.height)
		surface = s.Surface
	case cairoPNG:
		s := cairo.ImageSurfaceCreate(cairo.FormatARGB32, int(params.width), int(params.height))
		surface = s.Surface
	}
	cr.context = cairo.Create(surface)

	// Setting font parameters
	/*
		fontOpts := cairo.FontOptionsCreate()
		cr.context.GetFontOptions(fontOpts)
		fontOpts.SetAntialias(cairo.AntialiasGray)
		cr.context.SetFontOptions(fontOpts)
	*/

	setColor(&cr, params.bgColor)
	drawRectangle(&cr, &params, 0, 0, params.width, params.height, true)

	drawGraph(&cr, &params, results)

	surface.Flush()

	var b []byte

	switch backend {
	case cairoPNG:
		var buf bytes.Buffer
		surface.WriteToPNG(&buf)
		surface.Finish()
		b = buf.Bytes()
	case cairoSVG:
		surface.Finish()
		b, _ = ioutil.ReadFile(tmpfile.Name())
		// NOTE(dgryski): This is the dumbest thing ever, but needed
		// for compatibility.  I'm not doing the rest of the svg
		// munging that graphite does.
		// We could speed this up with Index(`pt"`) and overwriting the
		// `t` twice
		b = bytes.Replace(b, []byte(`pt"`), []byte(`px"`), 2)
	}

	return b
}

func drawGraph(cr *cairoSurfaceContext, params *Params, results []*MetricData) {
	var minNumberOfPoints, maxNumberOfPoints int32
	params.secondYAxis = false

	params.startTime = -1
	params.endTime = -1
	minNumberOfPoints = -1
	maxNumberOfPoints = -1
	for _, res := range results {
		tmp := res.GetStartTime()
		if params.startTime == -1 || params.startTime > tmp {
			params.startTime = tmp
		}
		tmp = res.GetStopTime()
		if params.endTime == -1 || params.endTime > tmp {
			params.endTime = tmp
		}

		tmp = int32(len(res.Values))
		if minNumberOfPoints == -1 || tmp < minNumberOfPoints {
			minNumberOfPoints = tmp
		}
		if maxNumberOfPoints == -1 || tmp > maxNumberOfPoints {
			maxNumberOfPoints = tmp
		}

	}
	params.timeRange = params.endTime - params.startTime

	if params.timeRange <= 0 {
		x := params.width / 2.0
		y := params.height / 2.0
		setColor(cr, string2RGBA("red"))
		fontSize := math.Log(params.width * params.height)
		setFont(cr, params, fontSize)
		drawText(cr, params, "No Data", x, y, HAlignCenter, VAlignTop, 0)

		return
	}

	for _, res := range results {
		if res.secondYAxis {
			params.dataRight = append(params.dataRight, res)
		} else {
			params.dataLeft = append(params.dataLeft, res)
		}
	}

	if len(params.dataRight) > 0 {
		params.secondYAxis = true
		params.yAxisSide = YAxisSideLeft
	}

	if params.graphOnly {
		params.hideLegend = true
		params.hideGrid = true
		params.hideAxes = true
		params.hideYAxis = true
	}

	if params.yAxisSide == YAxisSideRight {
		params.margin = int(params.width)
	}

	if params.lineMode == LineModeSlope && minNumberOfPoints == 1 {
		params.lineMode = LineModeStaircase
	}

	var colorsCur int
	for _, res := range results {
		if res.color != "" {
			// already has a color defined -- skip
			continue
		}
		if params.secondYAxis && res.secondYAxis {
			res.lineWidth = params.rightWidth
			if params.rightDashed && res.dashed == 0 {
				res.dashed = 2.5
			}
			res.color = params.rightColor
		} else if params.secondYAxis {
			res.lineWidth = params.leftWidth
			if params.leftDashed && res.dashed == 0 {
				res.dashed = 2.5
			}
			res.color = params.leftColor
		}
		if res.color == "" {
			res.color = params.colorList[colorsCur]
			colorsCur++
			if colorsCur >= len(params.colorList) {
				colorsCur = 0
			}
		}
	}

	if params.title != "" || params.vtitle != "" || params.vtitleRight != "" {
		titleSize := params.fontSize + math.Floor(math.Log(params.fontSize))

		setColor(cr, params.fgColor)
		setFont(cr, params, titleSize)
	}

	if params.title != "" {
		drawTitle(cr, params)
	}
	if params.vtitle != "" {
		drawVTitle(cr, params, params.vtitle, false)
	}
	if params.secondYAxis && params.vtitleRight != "" {
		drawVTitle(cr, params, params.vtitleRight, true)
	}

	setFont(cr, params, params.fontSize)
	if !params.hideLegend {
		drawLegend(cr, params, results)
	}

	// Setup axes, labels and grid
	// First we adjust the drawing area size to fit X-axis labels
	if !params.hideAxes {
		params.area.ymax -= params.fontExtents.Ascent * 2
	}

	if !(params.lineMode == LineModeStaircase || ((minNumberOfPoints == maxNumberOfPoints) && (minNumberOfPoints == 2))) {
		params.endTime = 0
		for _, res := range results {
			tmp := res.GetStopTime() - res.GetStepTime()
			if params.endTime < tmp {
				params.endTime = tmp
			}
		}
		params.timeRange = params.endTime - params.startTime
		if params.timeRange < 0 {
			panic("startTime > endTime!!!")
		}
	}

	// look for at least one stacked value
	for _, r := range results {
		if r.stacked {
			params.hasStack = true
			break
		}
	}

	// check if we need to stack all the things
	if params.areaMode == AreaModeStacked {
		params.hasStack = true
		for _, r := range results {
			r.stacked = true
			r.stackName = "stack"
		}
	}

	if params.hasStack {
		sort.Stable(ByStacked(results))
		// perform all aggregations / summations up so the rest of the graph drawing code doesn't need to care

		var stackName = results[0].stackName
		var total []float64
		for _, r := range results {
			if r.drawAsInfinite {
				continue
			}

			// reached the end of the stacks -- we're done
			if !r.stacked {
				break
			}

			if r.stackName != stackName {
				// got to a new named stack -- reset accumulator
				total = total[:0]
				stackName = r.stackName
			}

			absent := r.AggregatedAbsent()
			vals := r.AggregatedValues()
			for i, v := range vals {

				if len(total) <= i {
					total = append(total, 0)
				}

				if !absent[i] {
					vals[i] += total[i]
					total[i] += v
				}
			}
			// replace the values for the metric with our newly calculated ones
			// since these are now post-aggregation, reset the valuesPerPoint
			r.valuesPerPoint = 1
			r.Values = vals
			r.IsAbsent = absent
		}
	}

	consolidateDataPoints(params, results)

	currentXMin := params.area.xmin
	currentXMax := params.area.xmax
	if params.secondYAxis {
		setupTwoYAxes(cr, params, results)
	} else {
		setupYAxis(cr, params, results)
	}

	for currentXMin != params.area.xmin || currentXMax != params.area.xmax {
		consolidateDataPoints(params, results)
		currentXMin = params.area.xmin
		currentXMax = params.area.xmax
		if params.secondYAxis {
			setupTwoYAxes(cr, params, results)
		} else {
			setupYAxis(cr, params, results)
		}
	}

	setupXAxis(cr, params, results)

	if !params.hideAxes {
		setColor(cr, params.fgColor)
		drawLabels(cr, params, results)
		if !params.hideGrid {
			drawGridLines(cr, params, results)
		}
	}

	drawLines(cr, params, results)
}

func consolidateDataPoints(params *Params, results []*MetricData) {
	numberOfPixels := params.area.xmax - params.area.xmin - (params.lineWidth + 1)
	params.graphWidth = int(numberOfPixels)

	for _, series := range results {
		numberOfDataPoints := math.Floor(float64(params.timeRange / series.GetStepTime()))
		// minXStep := params.minXStep
		minXStep := 1.0
		divisor := float64(params.timeRange) / float64(series.GetStepTime())
		bestXStep := numberOfPixels / divisor
		if bestXStep < minXStep {
			drawableDataPoints := int(numberOfPixels / minXStep)
			pointsPerPixel := math.Ceil(numberOfDataPoints / float64(drawableDataPoints))
			// dumb variable naming :(
			series.setValuesPerPoint(int(pointsPerPixel))
			series.xStep = (numberOfPixels * pointsPerPixel) / numberOfDataPoints
		} else {
			series.setValuesPerPoint(1)
			series.xStep = bestXStep
		}
	}
}

func setupTwoYAxes(cr *cairoSurfaceContext, params *Params, results []*MetricData) {

	var Ldata []*MetricData
	var Rdata []*MetricData

	var seriesWithMissingValuesL []*MetricData
	var seriesWithMissingValuesR []*MetricData

	Ldata = params.dataLeft
	Rdata = params.dataRight

	for _, s := range Ldata {
		for _, v := range s.IsAbsent {
			if v {
				seriesWithMissingValuesL = append(seriesWithMissingValuesL, s)
				break
			}
		}
	}

	for _, s := range Rdata {
		for _, v := range s.IsAbsent {
			if v {
				seriesWithMissingValuesR = append(seriesWithMissingValuesR, s)
				break
			}
		}

	}

	yMinValueL := math.Inf(1)
	if params.drawNullAsZero && len(seriesWithMissingValuesL) > 0 {
		yMinValueL = 0
	} else {
		for _, s := range Ldata {
			if s.drawAsInfinite {
				continue
			}
			absent := s.AggregatedAbsent()
			for i, v := range s.AggregatedValues() {
				if absent[i] {
					continue
				}
				if v < yMinValueL {
					yMinValueL = v
				}
			}
		}
	}

	yMinValueR := math.Inf(1)
	if params.drawNullAsZero && len(seriesWithMissingValuesR) > 0 {
		yMinValueR = 0
	} else {
		for _, s := range Rdata {
			if s.drawAsInfinite {
				continue
			}
			absent := s.AggregatedAbsent()
			for i, v := range s.AggregatedValues() {
				if absent[i] {
					continue
				}
				if v < yMinValueR {
					yMinValueR = v
				}
			}
		}
	}

	var yMaxValueL, yMaxValueR float64
	yMaxValueL = math.Inf(-1)
	for _, s := range Ldata {
		absent := s.AggregatedAbsent()
		for i, v := range s.AggregatedValues() {
			if absent[i] {
				continue
			}

			if v > yMaxValueL {
				yMaxValueL = v
			}
		}
	}

	yMaxValueR = math.Inf(-1)
	for _, s := range Rdata {
		absent := s.AggregatedAbsent()
		for i, v := range s.AggregatedValues() {
			if absent[i] {
				continue
			}

			if v > yMaxValueR {
				yMaxValueR = v
			}
		}
	}

	if math.IsInf(yMinValueL, 1) {
		yMinValueL = 0
	}

	if math.IsInf(yMinValueR, 1) {
		yMinValueR = 0
	}

	if math.IsInf(yMaxValueL, -1) {
		yMaxValueL = 0
	}
	if math.IsInf(yMaxValueR, -1) {
		yMaxValueR = 0
	}

	if !math.IsNaN(params.yMaxLeft) {
		yMaxValueL = params.yMaxLeft
	}
	if !math.IsNaN(params.yMaxRight) {
		yMaxValueR = params.yMaxRight
	}

	if !math.IsNaN(params.yLimitLeft) && params.yLimitLeft < yMaxValueL {
		yMaxValueL = params.yLimitLeft
	}
	if !math.IsNaN(params.yLimitRight) && params.yLimitRight < yMaxValueR {
		yMaxValueR = params.yLimitRight
	}

	if !math.IsNaN(params.yMinLeft) {
		yMinValueL = params.yMinLeft
	}
	if !math.IsNaN(params.yMinRight) {
		yMinValueR = params.yMinRight
	}

	if yMaxValueL <= yMinValueL {
		yMaxValueL = yMinValueL + 1
	}
	if yMaxValueR <= yMinValueR {
		yMaxValueR = yMinValueR + 1
	}

	yVarianceL := yMaxValueL - yMinValueL
	yVarianceR := yMaxValueR - yMinValueR

	var orderL float64
	var orderFactorL float64
	if params.yUnitSystem == "binary" {
		orderL = math.Log2(yVarianceL)
		orderFactorL = math.Pow(2, math.Floor(orderL))
	} else {
		orderL = math.Log10(yVarianceL)
		orderFactorL = math.Pow(10, math.Floor(orderL))
	}

	var orderR float64
	var orderFactorR float64
	if params.yUnitSystem == "binary" {
		orderR = math.Log2(yVarianceR)
		orderFactorR = math.Pow(2, math.Floor(orderR))
	} else {
		orderR = math.Log10(yVarianceR)
		orderFactorR = math.Pow(10, math.Floor(orderR))
	}

	vL := yVarianceL / orderFactorL // we work with a scaled down yVariance for simplicity
	vR := yVarianceR / orderFactorR

	yDivisors := params.yDivisors

	prettyValues := []float64{0.1, 0.2, 0.25, 0.5, 1.0, 1.2, 1.25, 1.5, 2.0, 2.25, 2.5}

	var divinfoL divisorInfo
	var divinfoR divisorInfo

	for _, d := range yDivisors {
		qL := vL / d                                                              // our scaled down quotient, must be in the open interval (0,10)
		qR := vR / d                                                              // our scaled down quotient, must be in the open interval (0,10)
		pL := closest(qL, prettyValues)                                           // the prettyValue our quotient is closest to
		pR := closest(qR, prettyValues)                                           // the prettyValue our quotient is closest to
		divinfoL = append(divinfoL, yaxisDivisor{p: pL, diff: math.Abs(qL - pL)}) // make a  list so we can find the prettiest of the pretty
		divinfoR = append(divinfoR, yaxisDivisor{p: pR, diff: math.Abs(qR - pR)}) // make a  list so we can find the prettiest of the pretty
	}

	sort.Sort(divinfoL)
	sort.Sort(divinfoR)

	prettyValueL := divinfoL[0].p
	yStepL := prettyValueL * orderFactorL

	prettyValueR := divinfoR[0].p
	yStepR := prettyValueR * orderFactorR

	if !math.IsNaN(params.yStepL) {
		yStepL = params.yStepL
	}
	if !math.IsNaN(params.yStepR) {
		yStepR = params.yStepR
	}

	params.yStepL = yStepL
	params.yStepR = yStepR

	params.yBottomL = params.yStepL * math.Floor(yMinValueL/params.yStepL)
	params.yTopL = params.yStepL * math.Ceil(yMaxValueL/params.yStepL)

	params.yBottomR = params.yStepR * math.Floor(yMinValueR/params.yStepR)
	params.yTopR = params.yStepR * math.Ceil(yMaxValueR/params.yStepR)

	if params.logBase != 0 {
		if yMinValueL > 0 && yMinValueR > 0 {
			params.yBottomL = math.Pow(params.logBase, math.Floor(math.Log(yMinValueL)/math.Log(params.logBase)))
			params.yTopL = math.Pow(params.logBase, math.Ceil(math.Log(yMaxValueL/math.Log(params.logBase))))
			params.yBottomR = math.Pow(params.logBase, math.Floor(math.Log(yMinValueR)/math.Log(params.logBase)))
			params.yTopR = math.Pow(params.logBase, math.Ceil(math.Log(yMaxValueR/math.Log(params.logBase))))
		} else {
			panic("logscale with minvalue <= 0")
		}
	}

	if !math.IsNaN(params.yMaxLeft) {
		params.yTopL = params.yMaxLeft
	}
	if !math.IsNaN(params.yMaxRight) {
		params.yTopR = params.yMaxRight
	}
	if !math.IsNaN(params.yMinLeft) {
		params.yBottomL = params.yMinLeft
	}
	if !math.IsNaN(params.yMinRight) {
		params.yBottomR = params.yMinRight
	}

	params.ySpanL = params.yTopL - params.yBottomL
	params.ySpanR = params.yTopR - params.yBottomR

	if params.ySpanL == 0 {
		params.yTopL++
		params.ySpanL++
	}
	if params.ySpanR == 0 {
		params.yTopR++
		params.ySpanR++
	}

	params.graphHeight = params.area.ymax - params.area.ymin
	params.yScaleFactorL = params.graphHeight / params.ySpanL
	params.yScaleFactorR = params.graphHeight / params.ySpanR

	params.yLabelValuesL = getYLabelValues(params, params.yBottomL, params.yTopL, params.yStepL)
	params.yLabelValuesR = getYLabelValues(params, params.yBottomR, params.yTopR, params.yStepR)

	params.yLabelsL = make([]string, len(params.yLabelValuesL))
	for i, v := range params.yLabelValuesL {
		params.yLabelsL[i] = makeLabel(v, params.yStepL, params.ySpanL, params.yUnitSystem)
	}

	params.yLabelsR = make([]string, len(params.yLabelValuesR))
	for i, v := range params.yLabelValuesR {
		params.yLabelsR[i] = makeLabel(v, params.yStepR, params.ySpanR, params.yUnitSystem)
	}

	params.yLabelWidthL = 0
	for _, label := range params.yLabelsL {
		t := getTextExtents(cr, label)
		if t.Width > params.yLabelWidthL {
			params.yLabelWidthL = t.Width
		}
	}

	params.yLabelWidthR = 0
	for _, label := range params.yLabelsR {
		t := getTextExtents(cr, label)
		if t.Width > params.yLabelWidthR {
			params.yLabelWidthR = t.Width
		}
	}

	xMin := float64(params.margin) + (params.yLabelWidthL * 1.02)
	if params.area.xmin < xMin {
		params.area.xmin = xMin
	}

	xMax := params.width - (params.yLabelWidthR * 1.02)
	if params.area.xmax > xMax {
		params.area.xmax = xMax
	}
}

type yaxisDivisor struct {
	p    float64
	diff float64
}

type divisorInfo []yaxisDivisor

func (d divisorInfo) Len() int               { return len(d) }
func (d divisorInfo) Less(i int, j int) bool { return d[i].diff < d[j].diff }
func (d divisorInfo) Swap(i int, j int)      { d[i], d[j] = d[j], d[i] }

func makeLabel(yValue, yStep, ySpan float64, yUnitSystem string) string {
	yValue, prefix := formatUnits(yValue, yStep, yUnitSystem)
	ySpan, spanPrefix := formatUnits(ySpan, yStep, yUnitSystem)

	switch {
	case yValue < 0.1:
		return fmt.Sprintf("%.9g %s", yValue, prefix)
	case yValue < 1.0:
		return fmt.Sprintf("%.2f %s", yValue, prefix)
	case ySpan > 10 || spanPrefix != prefix:
		if yValue-math.Floor(yValue) < 0.00000000001 {
			return fmt.Sprintf("%.1f %s", yValue, prefix)
		}
		return fmt.Sprintf("%d %s ", int(yValue), prefix)
	case ySpan > 3:
		return fmt.Sprintf("%.1f %s ", yValue, prefix)
	case ySpan > 0.1:
		return fmt.Sprintf("%.2f %s ", yValue, prefix)
	default:
		return fmt.Sprintf("%g %s", yValue, prefix)
	}
}

func setupYAxis(cr *cairoSurfaceContext, params *Params, results []*MetricData) {
	var seriesWithMissingValues []*MetricData

	var yMinValue, yMaxValue float64

	yMinValue, yMaxValue = math.NaN(), math.NaN()
	for _, r := range results {
		if r.drawAsInfinite {
			continue
		}
		pushed := false
		absent := r.AggregatedAbsent()
		for i, v := range r.AggregatedValues() {
			if absent[i] && !pushed {
				seriesWithMissingValues = append(seriesWithMissingValues, r)
				pushed = true
			} else {
				if absent[i] {
					continue
				}
				if !math.IsInf(v, 0) && (math.IsNaN(yMinValue) || yMinValue > v) {
					yMinValue = v
				}
				if !math.IsInf(v, 0) && (math.IsNaN(yMaxValue) || yMaxValue < v) {
					yMaxValue = v
				}
			}
		}
	}

	if yMinValue > 0 && params.drawNullAsZero && len(seriesWithMissingValues) > 0 {
		yMinValue = 0
	}

	if yMaxValue < 0 && params.drawNullAsZero && len(seriesWithMissingValues) > 0 {
		yMaxValue = 0
	}

	// FIXME: Do we really need this check? It should be impossible to meet this conditions
	if math.IsNaN(yMinValue) {
		yMinValue = 0
	}
	if math.IsNaN(yMaxValue) {
		yMaxValue = 1
	}

	if !math.IsNaN(params.yMax) {
		yMaxValue = params.yMax
	}
	if !math.IsNaN(params.yMin) {
		yMinValue = params.yMin
	}

	if yMaxValue <= yMinValue {
		yMaxValue = yMinValue + 1
	}

	yVariance := yMaxValue - yMinValue

	var order float64
	var orderFactor float64
	if params.yUnitSystem == "binary" {
		order = math.Log2(yVariance)
		orderFactor = math.Pow(2, math.Floor(order))
	} else {
		order = math.Log10(yVariance)
		orderFactor = math.Pow(10, math.Floor(order))
	}

	v := yVariance / orderFactor // we work with a scaled down yVariance for simplicity

	yDivisors := params.yDivisors

	prettyValues := []float64{0.1, 0.2, 0.25, 0.5, 1.0, 1.2, 1.25, 1.5, 2.0, 2.25, 2.5}

	var divinfo divisorInfo

	for _, d := range yDivisors {
		q := v / d                                                           // our scaled down quotient, must be in the open interval (0,10)
		p := closest(q, prettyValues)                                        // the prettyValue our quotient is closest to
		divinfo = append(divinfo, yaxisDivisor{p: p, diff: math.Abs(q - p)}) // make a  list so we can find the prettiest of the pretty
	}

	sort.Sort(divinfo) // sort our pretty values by 'closeness to a factor"

	prettyValue := divinfo[0].p        // our winner! Y-axis will have labels placed at multiples of our prettyValue
	yStep := prettyValue * orderFactor // scale it back up to the order of yVariance

	if !math.IsNaN(params.yStep) {
		yStep = params.yStep
	}

	params.yStep = yStep

	params.yBottom = params.yStep * math.Floor(yMinValue/params.yStep) // start labels at the greatest multiple of yStep <= yMinValue
	params.yTop = params.yStep * math.Ceil(yMaxValue/params.yStep)     // Extend the top of our graph to the lowest yStep multiple >= yMaxValue

	if params.logBase != 0 {
		if yMinValue > 0 {
			params.yBottom = math.Pow(params.logBase, math.Floor(math.Log(yMinValue)/math.Log(params.logBase)))
			params.yTop = math.Pow(params.logBase, math.Ceil(math.Log(yMaxValue/math.Log(params.logBase))))
		} else {
			panic("logscale with minvalue <= 0")
			// raise GraphError('Logarithmic scale specified with a dataset with a minimum value less than or equal to zero')
		}
	}

	/*
	   if 'yMax' in self.params:
	     if self.params['yMax'] == 'max':
	       scale = 1.0 * yMaxValue / self.yTop
	       self.yStep *= (scale - 0.000001)
	       self.yTop = yMaxValue
	     else:
	       self.yTop = self.params['yMax'] * 1.0
	   if 'yMin' in self.params:
	     self.yBottom = self.params['yMin']
	*/

	params.ySpan = params.yTop - params.yBottom

	if params.ySpan == 0 {
		params.yTop++
		params.ySpan++
	}

	params.graphHeight = params.area.ymax - params.area.ymin
	params.yScaleFactor = params.graphHeight / params.ySpan

	if !params.hideAxes {
		// Create and measure the Y-labels

		params.yLabelValues = getYLabelValues(params, params.yBottom, params.yTop, params.yStep)

		params.yLabels = make([]string, len(params.yLabelValues))
		for i, v := range params.yLabelValues {
			params.yLabels[i] = makeLabel(v, params.yStep, params.ySpan, params.yUnitSystem)
		}

		params.yLabelWidth = 0
		for _, label := range params.yLabels {
			t := getTextExtents(cr, label)
			if t.Width > params.yLabelWidth {
				params.yLabelWidth = t.Width
			}
		}

		if !params.hideYAxis {
			if params.yAxisSide == YAxisSideLeft { // scoot the graph over to the left just enough to fit the y-labels
				xMin := float64(params.margin) + float64(params.yLabelWidth)*1.02
				if params.area.xmin < xMin {
					params.area.xmin = xMin
				}
			} else { // scoot the graph over to the right just enough to fit the y-labels
				// xMin := 0 // TODO(dgryski): bug?  Why is this set?
				xMax := float64(params.margin) - float64(params.yLabelWidth)*1.02
				if params.area.xmax >= xMax {
					params.area.xmax = xMax
				}
			}
		}
	} else {
		params.yLabelValues = nil
		params.yLabels = nil
		params.yLabelWidth = 0.0
	}
}

func getFontExtents(cr *cairoSurfaceContext) cairo.FontExtents {
	// TODO(dgryski): allow font options
	/*
	   if fontOptions:
	     self.setFont(**fontOptions)
	*/
	var F cairo.FontExtents
	cr.context.FontExtents(&F)
	return F
}

func getTextExtents(cr *cairoSurfaceContext, text string) cairo.TextExtents {
	// TODO(dgryski): allow font options
	/*
	   if fontOptions:
	     self.setFont(**fontOptions)
	*/
	var T cairo.TextExtents
	cr.context.TextExtents(text, &T)
	return T
}

// formatUnits formats the given value according to the given unit prefix system
func formatUnits(v, step float64, system string) (float64, string) {

	var condition func(float64) bool

	if step == math.NaN() {
		condition = func(size float64) bool { return math.Abs(v) >= size }
	} else {
		condition = func(size float64) bool { return math.Abs(v) >= size && step >= size }
	}

	unitsystem := unitSystems[system]

	for _, p := range unitsystem {
		fsize := float64(p.size)
		if condition(fsize) {
			v2 := v / fsize
			if (v2-math.Floor(v2)) < 0.00000000001 && v > 1 {
				v2 = math.Floor(v2)
			}
			return v2, p.prefix
		}
	}

	if (v-math.Floor(v)) < 0.00000000001 && v > 1 {
		v = math.Floor(v)
	}
	return v, ""
}

func getYLabelValues(params *Params, minYValue, maxYValue, yStep float64) []float64 {
	if params.logBase != 0 {
		return logrange(params.logBase, minYValue, maxYValue)
	}

	return frange(minYValue, maxYValue, yStep)
}

func logrange(base, scaleMin, scaleMax float64) []float64 {
	current := scaleMin
	if scaleMin > 0 {
		current = math.Floor(math.Log(scaleMin) / math.Log(base))
	}
	factor := current
	var vals []float64
	for current < scaleMax {
		current = math.Pow(base, factor)
		vals = append(vals, current)
		factor++
	}
	return vals
}

func frange(start, end, step float64) []float64 {
	var vals []float64
	f := start
	for f <= end {
		vals = append(vals, f)
		f += step
		// Protect against rounding errors on very small float ranges
		if f == start {
			vals = append(vals, end)
			break
		}
	}
	return vals
}

func closest(number float64, neighbours []float64) float64 {
	distance := math.Inf(1)
	var closestNeighbor float64
	for _, n := range neighbours {
		d := math.Abs(n - number)
		if d < distance {
			distance = d
			closestNeighbor = n
		}
	}

	return closestNeighbor
}

func setupXAxis(cr *cairoSurfaceContext, params *Params, results []*MetricData) {

	/*
	   if self.userTimeZone:
	     tzinfo = pytz.timezone(self.userTimeZone)
	   else:
	     tzinfo = pytz.timezone(settings.TIME_ZONE)
	*/

	/*

		self.start_dt = datetime.fromtimestamp(self.startTime, tzinfo)
		self.end_dt = datetime.fromtimestamp(self.endTime, tzinfo)
	*/

	secondsPerPixel := float64(params.timeRange) / float64(params.graphWidth)
	params.xScaleFactor = float64(params.graphWidth) / float64(params.timeRange)

	for _, c := range xAxisConfigs {
		if c.seconds <= secondsPerPixel && c.maxInterval >= params.timeRange {
			params.xConf = c
		}
	}

	if params.xConf.seconds == 0 {
		params.xConf = xAxisConfigs[len(xAxisConfigs)-1]
	}

	params.xLabelStep = int32(params.xConf.labelUnit) * params.xConf.labelStep
	params.xMinorGridStep = int32(float64(params.xConf.minorGridUnit) * params.xConf.minorGridStep)
	params.xMajorGridStep = int32(params.xConf.majorGridUnit) * params.xConf.majorGridStep
}

func drawLabels(cr *cairoSurfaceContext, params *Params, results []*MetricData) {
	if !params.hideYAxis {
		drawYAxis(cr, params, results)
	}
	if !params.hideXAxis {
		drawXAxis(cr, params, results)
	}
}

func drawYAxis(cr *cairoSurfaceContext, params *Params, results []*MetricData) {
	var x float64
	if params.secondYAxis {

		for _, value := range params.yLabelValuesL {
			label := makeLabel(value, params.yStepL, params.ySpanL, params.yUnitSystem)
			y := getYCoord(params, value, YCoordSideLeft)
			if y < 0 {
				y = 0
			}

			x = params.area.xmin - float64(params.yLabelWidthL)*0.02
			drawText(cr, params, label, x, y, HAlignRight, VAlignCenter, 0)

		}

		for _, value := range params.yLabelValuesR {
			label := makeLabel(value, params.yStepR, params.ySpanR, params.yUnitSystem)
			y := getYCoord(params, value, YCoordSideRight)
			if y < 0 {
				y = 0
			}

			x = params.area.xmax + float64(params.yLabelWidth)*0.02
			drawText(cr, params, label, x, y, HAlignLeft, VAlignCenter, 0)
		}
		return
	}

	for _, value := range params.yLabelValues {
		label := makeLabel(value, params.yStep, params.ySpan, params.yUnitSystem)
		y := getYCoord(params, value, YCoordSideNone)
		if y < 0 {
			y = 0
		}

		if params.yAxisSide == YAxisSideLeft {
			x = params.area.xmin - float64(params.yLabelWidth)*0.02
			drawText(cr, params, label, x, y, HAlignRight, VAlignCenter, 0)
		} else {
			x = params.area.xmax + float64(params.yLabelWidth)*0.02
			drawText(cr, params, label, x, y, HAlignLeft, VAlignCenter, 0)
		}
	}
}

func findXTimes(start int32, unit TimeUnit, step float64) (int32, int32) {

	t := time.Unix(int64(start), 0)

	var d time.Duration

	switch unit {
	case Second:
		d = time.Second
	case Minute:
		d = time.Minute
	case Hour:
		d = time.Hour
	case Day:
		d = 24 * time.Hour
	default:
		panic("invalid unit")
	}

	d *= time.Duration(step)
	t = t.Truncate(d)

	for t.Unix() < int64(start) {
		t = t.Add(d)
	}

	return int32(t.Unix()), int32(d / time.Second)
}

func drawXAxis(cr *cairoSurfaceContext, params *Params, results []*MetricData) {

	dt, xDelta := findXTimes(params.startTime, params.xConf.labelUnit, float64(params.xConf.labelStep))

	xFormat := params.xFormat
	if xFormat == "" {
		xFormat = params.xConf.format
	}

	maxAscent := getFontExtents(cr).Ascent

	for dt < params.endTime {
		label, _ := strftime.Format(xFormat, time.Unix(int64(dt), 0).In(params.tz))
		x := params.area.xmin + float64(dt-params.startTime)*params.xScaleFactor
		y := params.area.ymax + maxAscent
		drawText(cr, params, label, x, y, HAlignCenter, VAlignTop, 0)
		dt += xDelta
	}
}

func drawGridLines(cr *cairoSurfaceContext, params *Params, results []*MetricData) {
	// Horizontal grid lines
	leftside := params.area.xmin
	rightside := params.area.xmax
	top := params.area.ymin
	bottom := params.area.ymax

	var labels []float64
	if params.secondYAxis {
		labels = params.yLabelValuesL
	} else {
		labels = params.yLabelValues
	}

	for i, value := range labels {
		cr.context.SetLineWidth(0.4)
		setColor(cr, string2RGBA(params.majorGridLineColor))

		var y float64
		if params.secondYAxis {
			y = getYCoord(params, value, YCoordSideLeft)
		} else {
			y = getYCoord(params, value, YCoordSideNone)
		}

		if math.IsNaN(y) || y < 0 {
			continue
		}

		cr.context.MoveTo(leftside, y)
		cr.context.LineTo(rightside, y)
		cr.context.Stroke()

		// draw minor gridlines if this isn't the last label
		if params.minorY >= 1 && i < len(labels)-1 {
			valueLower, valueUpper := value, labels[i+1]

			// each minor gridline is 1/minorY apart from the nearby gridlines.
			// we calculate that distance, for adding to the value in the loop.
			distance := ((valueUpper - valueLower) / float64(1+params.minorY))

			// starting from the initial valueLower, we add the minor distance
			// for each minor gridline that we wish to draw, and then draw it.
			for minor := 0; minor < params.minorY; minor++ {
				cr.context.SetLineWidth(0.3)
				setColor(cr, string2RGBA(params.minorGridLineColor))

				// the current minor gridline value is halfway between the current and next major gridline values
				value = (valueLower + ((1 + float64(minor)) * distance))

				var yTopFactor float64
				if params.logBase != 0 {
					yTopFactor = params.logBase * params.logBase
				} else {
					yTopFactor = 1
				}

				if params.secondYAxis {
					if value >= (yTopFactor * params.yTopL) {
						continue
					}
				} else {
					if value >= (yTopFactor * params.yTop) {
						continue
					}

				}

				if params.secondYAxis {
					y = getYCoord(params, value, YCoordSideLeft)
				} else {
					y = getYCoord(params, value, YCoordSideNone)
				}

				if math.IsNaN(y) || y < 0 {
					continue
				}

				cr.context.MoveTo(leftside, y)
				cr.context.LineTo(rightside, y)
				cr.context.Stroke()
			}

		}

	}

	// Vertical grid lines

	// First we do the minor grid lines (majors will paint over them)
	cr.context.SetLineWidth(0.25)
	setColor(cr, string2RGBA(params.minorGridLineColor))
	dt, xMinorDelta := findXTimes(params.startTime, params.xConf.minorGridUnit, params.xConf.minorGridStep)

	for dt < params.endTime {
		x := params.area.xmin + float64(dt-params.startTime)*params.xScaleFactor

		if x < params.area.xmax {
			cr.context.MoveTo(x, bottom)
			cr.context.LineTo(x, top)
			cr.context.Stroke()
		}

		dt += xMinorDelta
	}

	// Now we do the major grid lines
	cr.context.SetLineWidth(0.33)
	setColor(cr, string2RGBA(params.majorGridLineColor))
	dt, xMajorDelta := findXTimes(params.startTime, params.xConf.majorGridUnit, float64(params.xConf.majorGridStep))

	for dt < params.endTime {
		x := params.area.xmin + float64(dt-params.startTime)*params.xScaleFactor

		if x < params.area.xmax {
			cr.context.MoveTo(x, bottom)
			cr.context.LineTo(x, top)
			cr.context.Stroke()
		}

		dt += xMajorDelta
	}

	// Draw side borders for our graph area
	cr.context.SetLineWidth(0.5)
	cr.context.MoveTo(params.area.xmax, bottom)
	cr.context.LineTo(params.area.xmax, top)
	cr.context.MoveTo(params.area.xmin, bottom)
	cr.context.LineTo(params.area.xmin, top)
	cr.context.Stroke()
}

func str2linecap(s string) cairo.LineCap {
	switch s {
	case "butt":
		return cairo.LineCapButt
	case "round":
		return cairo.LineCapRound
	case "square":
		return cairo.LineCapSquare
	}
	return cairo.LineCapButt
}

func str2linejoin(s string) cairo.LineJoin {
	switch s {
	case "miter":
		return cairo.LineJoinMiter
	case "round":
		return cairo.LineJoinRound
	case "bevel":
		return cairo.LineJoinBevel
	}
	return cairo.LineJoinMiter
}

func getYCoord(params *Params, value float64, side YCoordSide) (y float64) {

	var yLabelValues []float64
	var yTop float64
	var yBottom float64

	switch side {
	case YCoordSideLeft:
		yLabelValues = params.yLabelValuesL
		yTop = params.yTopL
		yBottom = params.yBottomL
	case YCoordSideRight:
		yLabelValues = params.yLabelValuesR
		yTop = params.yTopR
		yBottom = params.yBottomR
	default:
		yLabelValues = params.yLabelValues
		yTop = params.yTop
		yBottom = params.yBottom
	}

	var highestValue float64
	var lowestValue float64

	if yLabelValues != nil {
		highestValue = yLabelValues[len(yLabelValues)-1]
		lowestValue = yLabelValues[0]
	} else {
		highestValue = yTop
		lowestValue = yBottom
	}
	pixelRange := params.area.ymax - params.area.ymin
	relativeValue := (value - lowestValue)
	valueRange := (highestValue - lowestValue)
	if params.logBase != 0 {
		if value <= 0 {
			return math.NaN()
		}
		relativeValue = (math.Log(value) / math.Log(params.logBase)) - (math.Log(lowestValue) / math.Log(params.logBase))
		valueRange = (math.Log(highestValue) / math.Log(params.logBase)) - (math.Log(lowestValue) / math.Log(params.logBase))
	}
	pixelToValueRatio := (pixelRange / valueRange)
	valueInPixels := (pixelToValueRatio * relativeValue)
	return params.area.ymax - valueInPixels
}

func drawLines(cr *cairoSurfaceContext, params *Params, results []*MetricData) {

	linecap := "butt"
	linejoin := "miter"

	cr.context.SetLineWidth(params.lineWidth)

	originalWidth := params.lineWidth

	cr.context.SetDash(nil, 0)

	cr.context.SetLineCap(str2linecap(linecap))
	cr.context.SetLineJoin(str2linejoin(linejoin))

	// TODO(dgryski): areaMode all, first

	if !math.IsNaN(params.areaAlpha) {
		alpha := params.areaAlpha
		var strokeSeries []*MetricData
		for _, r := range results {
			if r.stacked {
				r.alpha = alpha
				r.hasAlpha = true

				newSeries := MetricData{
					FetchResponse: pb.FetchResponse{
						Name:      r.Name,
						StopTime:  proto.Int32(r.GetStopTime()),
						StartTime: proto.Int32(r.GetStartTime()),
						StepTime:  proto.Int32(r.AggregatedTimeStep()),
						Values:    make([]float64, len(r.AggregatedValues())),
						IsAbsent:  make([]bool, len(r.AggregatedValues())),
					},
					valuesPerPoint: 1,
					graphOptions: graphOptions{
						color:       r.color,
						xStep:       r.xStep,
						secondYAxis: r.secondYAxis,
					},
				}
				copy(newSeries.Values, r.AggregatedValues())
				copy(newSeries.IsAbsent, r.AggregatedAbsent())
				strokeSeries = append(strokeSeries, &newSeries)
			}
		}
		if len(strokeSeries) > 0 {
			results = append(results, strokeSeries...)
		}
	}

	cr.context.SetLineWidth(1.0)
	cr.context.Rectangle(params.area.xmin, params.area.ymin, (params.area.xmax - params.area.xmin), (params.area.ymax - params.area.ymin))
	cr.context.Clip()
	cr.context.SetLineWidth(originalWidth)

	cr.context.Save()
	clipRestored := false
	for _, series := range results {

		if !series.stacked && !clipRestored {
			cr.context.Restore()
			clipRestored = true
		}

		cr.context.SetLineWidth(params.lineWidth)

		if series.dashed != 0 {
			cr.context.SetDash([]float64{series.dashed}, 1)
		}

		if series.invisible {
			setColorAlpha(cr, color.RGBA{0, 0, 0, 0}, 0)
		} else if series.hasAlpha {
			setColorAlpha(cr, string2RGBA(series.color), series.alpha)
		} else {
			setColor(cr, string2RGBA(series.color))
		}

		missingPoints := float64(series.GetStartTime()-params.startTime) / float64(series.GetStepTime())
		startShift := series.xStep * (missingPoints / float64(series.valuesPerPoint))
		x := float64(params.area.xmin) + startShift + (params.lineWidth / 2.0)
		y := float64(params.area.ymin)
		origX := x
		startX := x

		absent := series.AggregatedAbsent()
		consecutiveNones := 0
		for index, value := range series.AggregatedValues() {
			x = origX + (float64(index) * series.xStep)

			if absent[index] {
				value = math.NaN()
			}

			if params.drawNullAsZero && math.IsNaN(value) {
				value = 0
			}

			if math.IsNaN(value) {
				if consecutiveNones == 0 {
					cr.context.LineTo(x, y)
					if series.stacked {
						if params.secondYAxis {
							if series.secondYAxis {
								fillAreaAndClip(cr, params, x, y, startX, getYCoord(params, 0, YCoordSideRight))
							} else {
								fillAreaAndClip(cr, params, x, y, startX, getYCoord(params, 0, YCoordSideLeft))
							}
						} else {
							fillAreaAndClip(cr, params, x, y, startX, getYCoord(params, 0, YCoordSideNone))
						}
					}
				}
				consecutiveNones++
			} else {
				if params.secondYAxis {
					if series.secondYAxis {
						y = getYCoord(params, value, YCoordSideRight)
					} else {
						y = getYCoord(params, value, YCoordSideLeft)
					}
				} else {
					y = getYCoord(params, value, YCoordSideNone)
				}
				if math.IsNaN(y) {
					value = y
				} else {
					if y < 0 {
						y = 0
					}
				}
				if series.drawAsInfinite && value > 0 {
					cr.context.MoveTo(x, params.area.ymax)
					cr.context.LineTo(x, params.area.ymin)
					cr.context.Stroke()
					continue
				}
				if consecutiveNones > 0 {
					startX = x
				}

				if !math.IsNaN(y) {
					switch params.lineMode {

					case LineModeStaircase:
						if consecutiveNones > 0 {
							cr.context.MoveTo(x, y)
						} else {
							cr.context.LineTo(x, y)
						}
					case LineModeSlope:
						if consecutiveNones > 0 {
							cr.context.MoveTo(x, y)
						}
					case LineModeConnected:
						if consecutiveNones > params.connectedLimit || consecutiveNones == index {
							cr.context.MoveTo(x, y)
						}
					}

					cr.context.LineTo(x, y)
				}
				consecutiveNones = 0
			}
		}

		if series.stacked {
			var areaYFrom float64
			if params.secondYAxis {
				if series.secondYAxis {
					areaYFrom = getYCoord(params, 0, YCoordSideRight)
				} else {
					areaYFrom = getYCoord(params, 0, YCoordSideLeft)
				}
			} else {
				areaYFrom = getYCoord(params, 0, YCoordSideNone)
			}
			fillAreaAndClip(cr, params, x, y, startX, areaYFrom)
		} else {
			cr.context.Stroke()
		}
		cr.context.SetLineWidth(originalWidth)

		if series.dashed != 0 {
			cr.context.SetDash(nil, 0)
		}
	}
}

type SeriesLegend struct {
	name        *string
	color       *string
	secondYAxis bool
}

func drawLegend(cr *cairoSurfaceContext, params *Params, results []*MetricData) {
	const (
		padding = 5
	)
	var longestName *string
	var longestNameLen int
	var uniqueNames map[string]bool
	var numRight int
	var legend []SeriesLegend
	if params.uniqueLegend {
		uniqueNames = make(map[string]bool)
	}

	for _, res := range results {
		nameLen := len(res.GetName())
		if nameLen == 0 {
			continue
		}
		if nameLen > longestNameLen {
			longestNameLen = nameLen
			longestName = res.Name
		}
		if res.secondYAxis {
			numRight++
		}
		if params.uniqueLegend {
			if _, ok := uniqueNames[res.GetName()]; !ok {
				var tmp = SeriesLegend{
					res.Name,
					&res.color,
					res.secondYAxis,
				}
				uniqueNames[res.GetName()] = true
				legend = append(legend, tmp)
			}
		} else {
			var tmp = SeriesLegend{
				res.Name,
				&res.color,
				res.secondYAxis,
			}
			legend = append(legend, tmp)
		}
	}

	rightSideLabels := false
	testSizeName := *longestName + " " + *longestName
	var textExtents cairo.TextExtents
	cr.context.TextExtents(testSizeName, &textExtents)
	testWidth := textExtents.Width + 2*(params.fontExtents.Height+padding)
	if testWidth+50 < params.width {
		rightSideLabels = true
	}

	cr.context.TextExtents(*longestName, &textExtents)
	boxSize := params.fontExtents.Height - 1
	lineHeight := params.fontExtents.Height + 1
	labelWidth := textExtents.Width + 2*(boxSize+padding)
	cr.context.SetLineWidth(1.0)
	x := params.area.xmin

	if params.secondYAxis && rightSideLabels {
		columns := math.Max(1, math.Floor(math.Floor((params.width-params.area.xmin)/labelWidth)/2.0))
		numberOfLines := math.Max(float64(len(results)-numRight), float64(numRight))
		legendHeight := math.Max(1, (numberOfLines/columns)) * (lineHeight + padding)
		params.area.ymax -= legendHeight
		y := params.area.ymax + (2 * padding)

		xRight := params.area.xmax - params.area.xmin
		yRight := y
		nRight := 0
		n := 0
		for _, item := range legend {
			setColor(cr, string2RGBA(*item.color))
			if item.secondYAxis {
				nRight++
				drawRectangle(cr, params, xRight-padding, yRight, boxSize, boxSize, true)
				color := colors["darkgray"]
				setColor(cr, color)
				drawRectangle(cr, params, xRight-padding, yRight, boxSize, boxSize, false)
				setColor(cr, params.fgColor)
				drawText(cr, params, *item.name, xRight-boxSize, yRight, HAlignRight, VAlignTop, 0.0)
				xRight -= labelWidth
				if nRight%int(columns) == 0 {
					xRight = params.area.xmax - params.area.xmin
					yRight += lineHeight
				}
			} else {
				n++
				drawRectangle(cr, params, x, y, boxSize, boxSize, true)
				color := colors["darkgray"]
				setColor(cr, color)
				drawRectangle(cr, params, x, y, boxSize, boxSize, false)
				setColor(cr, params.fgColor)
				drawText(cr, params, *item.name, x+boxSize+padding, y, HAlignLeft, VAlignTop, 0.0)
				x += labelWidth
				if n%int(columns) == 0 {
					x = params.area.xmin
					y += lineHeight
				}
			}
		}
		return
	}
	// else
	columns := math.Max(1, math.Floor(params.width/labelWidth))
	numberOfLines := math.Ceil(float64(len(results)) / columns)
	legendHeight := numberOfLines * (lineHeight + padding)
	params.area.ymax -= legendHeight
	y := params.area.ymax + (2 * padding)
	cnt := 0
	for _, item := range legend {
		setColor(cr, string2RGBA(*item.color))
		if item.secondYAxis {
			drawRectangle(cr, params, x+labelWidth+padding, y, boxSize, boxSize, true)
			color := colors["darkgray"]
			setColor(cr, color)
			drawRectangle(cr, params, x+labelWidth+padding, y, boxSize, boxSize, false)
			setColor(cr, params.fgColor)
			drawText(cr, params, *item.name, x+labelWidth, y, HAlignRight, VAlignTop, 0.0)
			x += labelWidth
		} else {
			drawRectangle(cr, params, x, y, boxSize, boxSize, true)
			color := colors["darkgray"]
			setColor(cr, color)
			drawRectangle(cr, params, x, y, boxSize, boxSize, false)
			setColor(cr, params.fgColor)
			drawText(cr, params, *item.name, x+boxSize+padding, y, HAlignLeft, VAlignTop, 0.0)
			x += labelWidth
		}
		if (cnt+1)%int(columns) == 0 {
			x = params.area.xmin
			y += lineHeight
		}
		cnt++
	}
	return
}

func drawTitle(cr *cairoSurfaceContext, params *Params) {
	y := params.area.ymin
	x := params.width / 2.0
	lines := strings.Split(params.title, "\n")
	lineHeight := params.fontExtents.Height

	for _, line := range lines {
		drawText(cr, params, line, x, y, HAlignCenter, VAlignTop, 0.0)
		y += lineHeight
	}
	params.area.ymin = y
	if params.yAxisSide != YAxisSideRight {
		params.area.ymin += float64(params.margin)
	}
}

func drawVTitle(cr *cairoSurfaceContext, params *Params, title string, rightAlign bool) {
	lineHeight := params.fontExtents.Height

	if rightAlign {
		x := params.area.xmax - lineHeight
		y := params.height / 2.0
		for _, line := range strings.Split(title, "\n") {
			drawText(cr, params, line, x, y, HAlignCenter, VAlignBaseline, 90.0)
			x -= lineHeight
		}
		params.area.xmax = x - float64(params.margin) - lineHeight
	} else {
		x := params.area.xmin + lineHeight
		y := params.height / 2.0
		for _, line := range strings.Split(title, "\n") {
			drawText(cr, params, line, x, y, HAlignCenter, VAlignBaseline, 270.0)
			x += lineHeight
		}
		params.area.xmin = x + float64(params.margin) + lineHeight
	}
}

func radians(angle float64) float64 {
	const x = math.Pi / 180
	return angle * x
}

func drawText(cr *cairoSurfaceContext, params *Params, text string, x, y float64, align HAlign, valign VAlign, rotate float64) {
	var hAlign, vAlign float64
	var textExtents cairo.TextExtents
	var fontExtents cairo.FontExtents
	var origMatrix cairo.Matrix
	cr.context.TextExtents(text, &textExtents)
	cr.context.FontExtents(&fontExtents)

	cr.context.GetMatrix(&origMatrix)
	angle := radians(rotate)
	angleSin, angleCos := math.Sincos(angle)

	switch align {
	case HAlignLeft:
		hAlign = 0.0
	case HAlignCenter:
		hAlign = textExtents.Width / 2.0
	case HAlignRight:
		hAlign = textExtents.Width
	}
	switch valign {
	case VAlignTop:
		vAlign = fontExtents.Ascent
	case VAlignCenter:
		vAlign = fontExtents.Height/2.0 - fontExtents.Descent/2.0
	case VAlignBottom:
		vAlign = -fontExtents.Descent
	case VAlignBaseline:
		vAlign = 0.0
	}

	cr.context.MoveTo(x, y)
	cr.context.RelMoveTo(angleSin*(-vAlign), angleCos*vAlign)
	cr.context.Rotate(angle)
	cr.context.RelMoveTo(-hAlign, 0)
	cr.context.TextPath(text)
	cr.context.Fill()
	cr.context.SetMatrix(&origMatrix)
}

func setColorAlpha(cr *cairoSurfaceContext, color color.RGBA, alpha float64) {
	r, g, b, _ := color.RGBA()
	cr.context.SetSourceRGBA(float64(r)/65536, float64(g)/65536, float64(b)/65536, alpha)
}

func setColor(cr *cairoSurfaceContext, color color.RGBA) {
	r, g, b, a := color.RGBA()
	cr.context.SetSourceRGBA(float64(r)/65536, float64(g)/65536, float64(b)/65536, float64(a)/65536)
}

func setFont(cr *cairoSurfaceContext, params *Params, size float64) {
	cr.context.SelectFontFace(params.fontName, params.fontItalic, params.fontBold)
	cr.context.SetFontSize(size)
	cr.context.FontExtents(&params.fontExtents)
}

func drawRectangle(cr *cairoSurfaceContext, params *Params, x float64, y float64, w float64, h float64, fill bool) {
	if !fill {
		offset := cr.context.GetLineWidth() / 2.0
		x += offset
		y += offset
		h -= offset
		w -= offset
	}
	cr.context.Rectangle(x, y, w, h)
	if fill {
		cr.context.Fill()
	} else {
		cr.context.SetDash(nil, 0)
		cr.context.Stroke()
	}
}

func fillAreaAndClip(cr *cairoSurfaceContext, params *Params, x, y, startX, areaYFrom float64) {

	if math.IsNaN(startX) {
		startX = params.area.xmin
	}

	if math.IsNaN(areaYFrom) {
		areaYFrom = params.area.ymax
	}

	pattern := cr.context.CopyPath()

	// fill
	cr.context.LineTo(x, areaYFrom)      // bottom endX
	cr.context.LineTo(startX, areaYFrom) // bottom startX
	cr.context.ClosePath()
	cr.context.Fill()

	// clip above y axis
	cr.context.AppendPath(pattern)
	cr.context.LineTo(x, areaYFrom)                       // yZero endX
	cr.context.LineTo(params.area.xmax, areaYFrom)        // yZero right
	cr.context.LineTo(params.area.xmax, params.area.ymin) // top right
	cr.context.LineTo(params.area.xmin, params.area.ymin) // top left
	cr.context.LineTo(params.area.xmin, areaYFrom)        // yZero left
	cr.context.LineTo(startX, areaYFrom)                  // yZero startX

	// clip below y axis
	cr.context.LineTo(x, areaYFrom)                       // yZero endX
	cr.context.LineTo(params.area.xmax, areaYFrom)        // yZero right
	cr.context.LineTo(params.area.xmax, params.area.ymax) // bottom right
	cr.context.LineTo(params.area.xmin, params.area.ymax) // bottom left
	cr.context.LineTo(params.area.xmin, areaYFrom)        // yZero left
	cr.context.LineTo(startX, areaYFrom)                  // yZero startX
	cr.context.ClosePath()
	cr.context.Clip()
}

func string2RGBA(clr string) color.RGBA {
	if c, ok := colors[clr]; ok {
		return c
	}
	return hexToRGBA(clr)
}

// https://code.google.com/p/sadbox/source/browse/color/hex.go
// hexToColor converts an Hex string to a RGB triple.
func hexToRGBA(h string) color.RGBA {
	var r, g, b uint8
	if len(h) > 0 && h[0] == '#' {
		h = h[1:]
	}

	if len(h) == 3 {
		h = h[:1] + h[:1] + h[1:2] + h[1:2] + h[2:] + h[2:]
	}

	alpha := byte(255)

	if len(h) == 6 {
		if rgb, err := strconv.ParseUint(string(h), 16, 32); err == nil {
			r = uint8(rgb >> 16)
			g = uint8(rgb >> 8)
			b = uint8(rgb)
		}
	}

	if len(h) == 8 {
		if rgb, err := strconv.ParseUint(string(h), 16, 32); err == nil {
			r = uint8(rgb >> 24)
			g = uint8(rgb >> 16)
			b = uint8(rgb >> 8)
			alpha = uint8(rgb)
		}
	}

	return color.RGBA{r, g, b, alpha}
}

type ByStacked []*MetricData

func (b ByStacked) Len() int { return len(b) }

func (b ByStacked) Less(i int, j int) bool {
	return (b[i].stacked && !b[j].stacked) || (b[i].stacked && b[j].stacked && b[i].stackName < b[j].stackName)
}

func (b ByStacked) Swap(i int, j int) { b[i], b[j] = b[j], b[i] }
