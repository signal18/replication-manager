package whisper

import (
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"sort"
	"testing"
	"time"
)

func checkBytes(t *testing.T, expected, received []byte) {
	if len(expected) != len(received) {
		t.Fatalf("Invalid number of bytes. Expected %v, received %v", len(expected), len(received))
	}
	for i := range expected {
		if expected[i] != received[i] {
			t.Fatalf("Incorrect byte at %v. Expected %v, received %v", i+1, expected[i], received[i])
		}
	}
}

func testParseRetentionDef(t *testing.T, retentionDef string, expectedPrecision, expectedPoints int, hasError bool) {
	errTpl := fmt.Sprintf("Expected %%v to be %%v but received %%v for retentionDef %v", retentionDef)

	retention, err := ParseRetentionDef(retentionDef)

	if (err == nil && hasError) || (err != nil && !hasError) {
		if hasError {
			t.Fatalf("Expected error but received none for retentionDef %v", retentionDef)
		} else {
			t.Fatalf("Expected no error but received %v for retentionDef %v", err, retentionDef)
		}
	}
	if err == nil {
		if retention.secondsPerPoint != expectedPrecision {
			t.Fatalf(errTpl, "precision", expectedPrecision, retention.secondsPerPoint)
		}
		if retention.numberOfPoints != expectedPoints {
			t.Fatalf(errTpl, "points", expectedPoints, retention.numberOfPoints)
		}
	}
}

func TestParseRetentionDef(t *testing.T) {
	testParseRetentionDef(t, "1s:5m", 1, 300, false)
	testParseRetentionDef(t, "1m:30m", 60, 30, false)
	testParseRetentionDef(t, "1m", 0, 0, true)
	testParseRetentionDef(t, "1m:30m:20s", 0, 0, true)
	testParseRetentionDef(t, "1f:30s", 0, 0, true)
	testParseRetentionDef(t, "1m:30f", 0, 0, true)
}

func TestParseRetentionDefs(t *testing.T) {
	retentions, err := ParseRetentionDefs("1s:5m,1m:30m")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if length := len(retentions); length != 2 {
		t.Fatalf("Expected 2 retentions, received %v", length)
	}
}

func TestSortRetentions(t *testing.T) {
	retentions := Retentions{{300, 12}, {60, 30}, {1, 300}}
	sort.Sort(retentionsByPrecision{retentions})
	if retentions[0].secondsPerPoint != 1 {
		t.Fatalf("Retentions array is not sorted")
	}
}

func setUpCreate() (path string, fileExists func(string) bool, archiveList Retentions, tearDown func()) {
	path = "/tmp/whisper-testing.wsp"
	os.Remove(path)
	fileExists = func(path string) bool {
		fi, _ := os.Lstat(path)
		return fi != nil
	}
	archiveList = Retentions{{1, 300}, {60, 30}, {300, 12}}
	tearDown = func() {
		os.Remove(path)
	}
	return path, fileExists, archiveList, tearDown
}

func TestCreateCreatesFile(t *testing.T) {
	path, fileExists, retentions, tearDown := setUpCreate()
	expected := []byte{
		// Metadata
		0x00, 0x00, 0x00, 0x01, // Aggregation type
		0x00, 0x00, 0x0e, 0x10, // Max retention
		0x3f, 0x00, 0x00, 0x00, // xFilesFactor
		0x00, 0x00, 0x00, 0x03, // Retention count
		// Archive Info
		// Retention 1 (1, 300)
		0x00, 0x00, 0x00, 0x34, // offset
		0x00, 0x00, 0x00, 0x01, // secondsPerPoint
		0x00, 0x00, 0x01, 0x2c, // numberOfPoints
		// Retention 2 (60, 30)
		0x00, 0x00, 0x0e, 0x44, // offset
		0x00, 0x00, 0x00, 0x3c, // secondsPerPoint
		0x00, 0x00, 0x00, 0x1e, // numberOfPoints
		// Retention 3 (300, 12)
		0x00, 0x00, 0x0f, 0xac, // offset
		0x00, 0x00, 0x01, 0x2c, // secondsPerPoint
		0x00, 0x00, 0x00, 0x0c} // numberOfPoints
	whisper, err := Create(path, retentions, Average, 0.5)
	if err != nil {
		t.Fatalf("Failed to create whisper file: %v", err)
	}
	if whisper.aggregationMethod != Average {
		t.Fatalf("Unexpected aggregationMethod %v, expected %v", whisper.aggregationMethod, Average)
	}
	if whisper.maxRetention != 3600 {
		t.Fatalf("Unexpected maxRetention %v, expected 3600", whisper.maxRetention)
	}
	if whisper.xFilesFactor != 0.5 {
		t.Fatalf("Unexpected xFilesFactor %v, expected 0.5", whisper.xFilesFactor)
	}
	if len(whisper.archives) != 3 {
		t.Fatalf("Unexpected archive count %v, expected 3", len(whisper.archives))
	}
	whisper.Close()
	if !fileExists(path) {
		t.Fatalf("File does not exist after create")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Failed to open whisper file")
	}
	contents := make([]byte, len(expected))
	file.Read(contents)

	for i := 0; i < len(contents); i++ {
		if expected[i] != contents[i] {
			// Show what is being written
			// for i := 0; i < 13; i++ {
			// 	for j := 0; j < 4; j ++ {
			// 		fmt.Printf("  %02x", contents[(i*4)+j])
			// 	}
			// 	fmt.Print("\n")
			// }
			t.Fatalf("File is incorrect at character %v, expected %x got %x", i, expected[i], contents[i])
		}
	}

	// test size
	info, err := os.Stat(path)
	if info.Size() != 4156 {
		t.Fatalf("File size is incorrect, expected %v got %v", 4156, info.Size())
	}
	tearDown()
}

func TestCreateFileAlreadyExists(t *testing.T) {
	path, _, retentions, tearDown := setUpCreate()
	os.Create(path)
	_, err := Create(path, retentions, Average, 0.5)
	if err == nil {
		t.Fatalf("Existing file should cause create to fail.")
	}
	tearDown()
}

func TestCreateFileInvalidRetentionDefs(t *testing.T) {
	path, _, retentions, tearDown := setUpCreate()
	// Add a small retention def on the end
	retentions = append(retentions, &Retention{1, 200})
	_, err := Create(path, retentions, Average, 0.5)
	if err == nil {
		t.Fatalf("Invalid retention definitions should cause create to fail.")
	}
	tearDown()
}

func TestOpenFile(t *testing.T) {
	path, _, retentions, tearDown := setUpCreate()
	whisper1, err := Create(path, retentions, Average, 0.5)
	if err != nil {
		fmt.Errorf("Failed to create: %v", err)
	}

	// write some points
	now := int(time.Now().Unix())
	for i := 0; i < 2; i++ {
		whisper1.Update(100, now-(i*1))
	}

	whisper2, err := Open(path)
	if err != nil {
		t.Fatalf("Failed to open whisper file: %v", err)
	}
	if whisper1.aggregationMethod != whisper2.aggregationMethod {
		t.Fatalf("aggregationMethod did not match, expected %v, received %v", whisper1.aggregationMethod, whisper2.aggregationMethod)
	}
	if whisper1.maxRetention != whisper2.maxRetention {
		t.Fatalf("maxRetention did not match, expected %v, received %v", whisper1.maxRetention, whisper2.maxRetention)
	}
	if whisper1.xFilesFactor != whisper2.xFilesFactor {
		t.Fatalf("xFilesFactor did not match, expected %v, received %v", whisper1.xFilesFactor, whisper2.xFilesFactor)
	}
	if len(whisper1.archives) != len(whisper2.archives) {
		t.Fatalf("archive count does not match, expected %v, received %v", len(whisper1.archives), len(whisper2.archives))
	}
	for i := range whisper1.archives {
		if whisper1.archives[i].offset != whisper2.archives[i].offset {
			t.Fatalf("archive mismatch offset at %v [%v, %v]", i, whisper1.archives[i].offset, whisper2.archives[i].offset)
		}
		if whisper1.archives[i].Retention.secondsPerPoint != whisper2.archives[i].Retention.secondsPerPoint {
			t.Fatalf("Retention.secondsPerPoint mismatch offset at %v [%v, %v]", i, whisper1.archives[i].Retention.secondsPerPoint, whisper2.archives[i].Retention.secondsPerPoint)
		}
		if whisper1.archives[i].Retention.numberOfPoints != whisper2.archives[i].Retention.numberOfPoints {
			t.Fatalf("Retention.numberOfPoints mismatch offset at %v [%v, %v]", i, whisper1.archives[i].Retention.numberOfPoints, whisper2.archives[i].Retention.numberOfPoints)
		}

	}

	result1, err := whisper1.Fetch(now-3, now)
	if err != nil {
		t.Fatalf("Error retrieving result from created whisper")
	}
	result2, err := whisper2.Fetch(now-3, now)
	if err != nil {
		t.Fatalf("Error retrieving result from opened whisper")
	}

	if result1.String() != result2.String() {
		t.Fatalf("Results do not match")
	}

	tearDown()
}

/*
  Test the full cycle of creating a whisper file, adding some
  data points to it and then fetching a time series.
*/
func testCreateUpdateFetch(t *testing.T, aggregationMethod AggregationMethod, xFilesFactor float32, secondsAgo, fromAgo, fetchLength, step int, currentValue, increment float64) *TimeSeries {
	var whisper *Whisper
	var err error
	path, _, archiveList, tearDown := setUpCreate()
	whisper, err = Create(path, archiveList, aggregationMethod, xFilesFactor)
	if err != nil {
		t.Fatalf("Failed create: %v", err)
	}
	defer whisper.Close()
	oldestTime := whisper.StartTime()
	now := int(time.Now().Unix())

	if (now - whisper.maxRetention) != oldestTime {
		t.Fatalf("Invalid whisper start time, expected %v, received %v", oldestTime, now-whisper.maxRetention)
	}

	for i := 0; i < secondsAgo; i++ {
		err = whisper.Update(currentValue, now-secondsAgo+i)
		if err != nil {
			t.Fatalf("Unexpected error for %v: %v", i, err)
		}
		currentValue += increment
	}

	fromTime := now - fromAgo
	untilTime := fromTime + fetchLength

	timeSeries, err := whisper.Fetch(fromTime, untilTime)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !validTimestamp(timeSeries.fromTime, fromTime, step) {
		t.Fatalf("Invalid fromTime [%v/%v], expected %v, received %v", secondsAgo, fromAgo, fromTime, timeSeries.fromTime)
	}
	if !validTimestamp(timeSeries.untilTime, untilTime, step) {
		t.Fatalf("Invalid untilTime [%v/%v], expected %v, received %v", secondsAgo, fromAgo, untilTime, timeSeries.untilTime)
	}
	if timeSeries.step != step {
		t.Fatalf("Invalid step [%v/%v], expected %v, received %v", secondsAgo, fromAgo, step, timeSeries.step)
	}
	tearDown()

	return timeSeries
}

func validTimestamp(value, stamp, step int) bool {
	return value == nearestStep(stamp, step) || value == nearestStep(stamp, step)+step
}
func nearestStep(stamp, step int) int {
	return stamp - (stamp % step) + step
}

func assertFloatAlmostEqual(t *testing.T, received, expected, slop float64) {
	if math.Abs(expected-received) > slop {
		t.Fatalf("Expected %v to be within %v of %v", expected, slop, received)
	}
}

func assertFloatEqual(t *testing.T, received, expected float64) {
	if math.Abs(expected-received) > 0.00001 {
		t.Fatalf("Expected %v, received %v", expected, received)
	}
}

func TestFetchEmptyTimeseries(t *testing.T) {
	path, _, archiveList, tearDown := setUpCreate()
	whisper, err := Create(path, archiveList, Sum, 0.5)
	if err != nil {
		t.Fatalf("Failed create: %v", err)
	}
	defer whisper.Close()

	now := int(time.Now().Unix())
	result, err := whisper.Fetch(now-3, now)
	for _, point := range result.Points() {
		if !math.IsNaN(point.Value) {
			t.Fatalf("Expecting NaN values got '%v'", point.Value)
		}
	}

	tearDown()
}

func TestCreateUpdateFetch(t *testing.T) {
	var timeSeries *TimeSeries
	timeSeries = testCreateUpdateFetch(t, Average, 0.5, 3500, 3500, 1000, 300, 0.5, 0.2)
	assertFloatAlmostEqual(t, timeSeries.values[1], 150.1, 58.0)
	assertFloatAlmostEqual(t, timeSeries.values[2], 210.75, 28.95)

	timeSeries = testCreateUpdateFetch(t, Sum, 0.5, 600, 600, 500, 60, 0.5, 0.2)
	assertFloatAlmostEqual(t, timeSeries.values[0], 18.35, 5.95)
	assertFloatAlmostEqual(t, timeSeries.values[1], 30.35, 5.95)
	// 4 is a crazy one because it fluctuates between 60 and ~4k
	assertFloatAlmostEqual(t, timeSeries.values[5], 4356.05, 500.0)

	timeSeries = testCreateUpdateFetch(t, Last, 0.5, 300, 300, 200, 1, 0.5, 0.2)
	assertFloatAlmostEqual(t, timeSeries.values[0], 0.7, 0.001)
	assertFloatAlmostEqual(t, timeSeries.values[10], 2.7, 0.001)
	assertFloatAlmostEqual(t, timeSeries.values[20], 4.7, 0.001)

}

// Test for a bug in python whisper library: https://github.com/graphite-project/whisper/pull/136
func TestCreateUpdateFetchOneValue(t *testing.T) {
	var timeSeries *TimeSeries
	timeSeries = testCreateUpdateFetch(t, Average, 0.5, 3500, 3500, 1, 300, 0.5, 0.2)
	if len(timeSeries.values) > 1 {
		t.Fatalf("More then one point fetched\n")
	}
}

func BenchmarkCreateUpdateFetch(b *testing.B) {
	path, _, archiveList, tearDown := setUpCreate()
	var err error
	var whisper *Whisper
	var secondsAgo, now, fromTime, untilTime int
	var currentValue, increment float64
	for i := 0; i < b.N; i++ {
		whisper, err = Create(path, archiveList, Average, 0.5)
		if err != nil {
			b.Fatalf("Failed create %v", err)
		}

		secondsAgo = 3500
		currentValue = 0.5
		increment = 0.2
		now = int(time.Now().Unix())

		for i := 0; i < secondsAgo; i++ {
			err = whisper.Update(currentValue, now-secondsAgo+i)
			if err != nil {
				b.Fatalf("Unexpected error for %v: %v", i, err)
			}
			currentValue += increment
		}

		fromTime = now - secondsAgo
		untilTime = fromTime + 1000

		whisper.Fetch(fromTime, untilTime)
		whisper.Close()
		tearDown()
	}
}

func BenchmarkFairCreateUpdateFetch(b *testing.B) {
	path, _, archiveList, tearDown := setUpCreate()
	var err error
	var whisper *Whisper
	var secondsAgo, now, fromTime, untilTime int
	var currentValue, increment float64
	for i := 0; i < b.N; i++ {
		whisper, err = Create(path, archiveList, Average, 0.5)
		if err != nil {
			b.Fatalf("Failed create %v", err)
		}
		whisper.Close()

		secondsAgo = 3500
		currentValue = 0.5
		increment = 0.2
		now = int(time.Now().Unix())

		for i := 0; i < secondsAgo; i++ {
			whisper, err = Open(path)
			err = whisper.Update(currentValue, now-secondsAgo+i)
			if err != nil {
				b.Fatalf("Unexpected error for %v: %v", i, err)
			}
			currentValue += increment
			whisper.Close()
		}

		fromTime = now - secondsAgo
		untilTime = fromTime + 1000

		whisper, err = Open(path)
		whisper.Fetch(fromTime, untilTime)
		whisper.Close()
		tearDown()
	}
}

func testCreateUpdateManyFetch(t *testing.T, aggregationMethod AggregationMethod, xFilesFactor float32, points []*TimeSeriesPoint, fromAgo, fetchLength int) *TimeSeries {
	var whisper *Whisper
	var err error
	path, _, archiveList, tearDown := setUpCreate()
	whisper, err = Create(path, archiveList, aggregationMethod, xFilesFactor)
	if err != nil {
		t.Fatalf("Failed create: %v", err)
	}
	defer whisper.Close()
	now := int(time.Now().Unix())

	whisper.UpdateMany(points)

	fromTime := now - fromAgo
	untilTime := fromTime + fetchLength

	timeSeries, err := whisper.Fetch(fromTime, untilTime)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	tearDown()

	return timeSeries
}

func makeGoodPoints(count, step int, value func(int) float64) []*TimeSeriesPoint {
	points := make([]*TimeSeriesPoint, count)
	now := int(time.Now().Unix())
	for i := 0; i < count; i++ {
		points[i] = &TimeSeriesPoint{now - (i * step), value(i)}
	}
	return points
}

func makeBadPoints(count, minAge int) []*TimeSeriesPoint {
	points := make([]*TimeSeriesPoint, count)
	now := int(time.Now().Unix())
	for i := 0; i < count; i++ {
		points[i] = &TimeSeriesPoint{now - (minAge + i), 123.456}
	}
	return points
}

func printPoints(points []*TimeSeriesPoint) {
	fmt.Print("[")
	for i, point := range points {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Printf("%v", point)
	}
	fmt.Println("]")
}

func TestCreateUpdateManyFetch(t *testing.T) {
	var timeSeries *TimeSeries

	points := makeGoodPoints(1000, 2, func(i int) float64 { return float64(i) })
	points = append(points, points[len(points)-1])
	timeSeries = testCreateUpdateManyFetch(t, Sum, 0.5, points, 1000, 800)

	// fmt.Println(timeSeries)

	assertFloatAlmostEqual(t, timeSeries.values[0], 455, 15)

	// all the ones
	points = makeGoodPoints(10000, 1, func(_ int) float64 { return 1 })
	timeSeries = testCreateUpdateManyFetch(t, Sum, 0.5, points, 10000, 10000)
	for i := 0; i < 6; i++ {
		assertFloatEqual(t, timeSeries.values[i], 1)
	}
	for i := 6; i < 10; i++ {
		assertFloatEqual(t, timeSeries.values[i], 5)
	}
}

// should not panic if all points are out of range
func TestCreateUpdateManyOnly_old_points(t *testing.T) {
	points := makeBadPoints(1, 10000)

	path, _, archiveList, tearDown := setUpCreate()
	whisper, err := Create(path, archiveList, Sum, 0.5)
	if err != nil {
		t.Fatalf("Failed create: %v", err)
	}
	defer whisper.Close()

	whisper.UpdateMany(points)

	tearDown()
}

func Test_extractPoints(t *testing.T) {
	points := makeGoodPoints(100, 1, func(i int) float64 { return float64(i) })
	now := int(time.Now().Unix())
	currentPoints, remainingPoints := extractPoints(points, now, 50)
	if length := len(currentPoints); length != 50 {
		t.Fatalf("First: %v", length)
	}
	if length := len(remainingPoints); length != 50 {
		t.Fatalf("Second: %v", length)
	}
}

// extractPoints should return empty slices if the first point is out of range
func Test_extractPoints_only_old_points(t *testing.T) {
	now := int(time.Now().Unix())
	points := makeBadPoints(1, 100)

	currentPoints, remainingPoints := extractPoints(points, now, 50)
	if length := len(currentPoints); length != 0 {
		t.Fatalf("First: %v", length)
	}
	if length := len(remainingPoints); length != 1 {
		t.Fatalf("Second2: %v", length)
	}
}

func test_aggregate(t *testing.T, method AggregationMethod, expected float64) {
	received := aggregate(method, []float64{1.0, 2.0, 3.0, 5.0, 4.0})
	if expected != received {
		t.Fatalf("Expected %v, received %v", expected, received)
	}
}
func Test_aggregateAverage(t *testing.T) {
	test_aggregate(t, Average, 3.0)
}

func Test_aggregateSum(t *testing.T) {
	test_aggregate(t, Sum, 15.0)
}

func Test_aggregateLast(t *testing.T) {
	test_aggregate(t, Last, 4.0)
}

func Test_aggregateMax(t *testing.T) {
	test_aggregate(t, Max, 5.0)
}

func Test_aggregateMin(t *testing.T) {
	test_aggregate(t, Min, 1.0)
}

func TestDataPointBytes(t *testing.T) {
	point := dataPoint{1234, 567.891}
	b := []byte{0, 0, 4, 210, 64, 129, 191, 32, 196, 155, 165, 227}
	checkBytes(t, b, point.Bytes())
}

func TestTimeSeriesPoints(t *testing.T) {
	ts := TimeSeries{fromTime: 1348003785, untilTime: 1348003795, step: 1, values: []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}}
	points := ts.Points()
	if length := len(points); length != 10 {
		t.Fatalf("Unexpected number of points in time series, %v", length)
	}
}

func TestUpdateManyWithManyRetentions(t *testing.T) {
	path, _, archiveList, tearDown := setUpCreate()
	lastArchive := archiveList[len(archiveList)-1]

	valueMin := 41
	valueMax := 43

	whisper, err := Create(path, archiveList, Average, 0.5)
	if err != nil {
		t.Fatalf("Failed create: %v", err)
	}

	points := make([]*TimeSeriesPoint, 1)

	now := int(time.Now().Unix())
	for i := 0; i < lastArchive.secondsPerPoint*2; i++ {
		points[0] = &TimeSeriesPoint{
			Time:  now - i,
			Value: float64(valueMin*(i%2) + valueMax*((i+1)%2)), // valueMin, valueMax, valueMin...
		}
		whisper.UpdateMany(points)
	}

	whisper.Close()

	// check data in last archive
	whisper, err = Open(path)
	if err != nil {
		t.Fatalf("Failed open: %v", err)
	}

	result, err := whisper.Fetch(now-lastArchive.numberOfPoints*lastArchive.secondsPerPoint, now)
	if err != nil {
		t.Fatalf("Failed fetch: %v", err)
	}

	foundValues := 0
	for i := 0; i < len(result.values); i++ {
		if !math.IsNaN(result.values[i]) {
			if result.values[i] >= float64(valueMin) &&
				result.values[i] <= float64(valueMax) {
				foundValues++
			}
		}
	}
	if foundValues < 2 {
		t.Fatalf("Not found values in archive %#v", lastArchive)
	}

	whisper.Close()

	tearDown()
}

func TestUpdateManyWithEqualTimestamp(t *testing.T) {
	now := int(time.Now().Unix())
	points := []*TimeSeriesPoint{}

	// add points
	// now timestamp: 0,99,2,97,...,3,99,1
	// now-1 timestamp: 100,1,98,...,97,2,99

	for i := 0; i < 100; i++ {
		if i%2 == 0 {
			points = append(points, &TimeSeriesPoint{now, float64(i)})
			points = append(points, &TimeSeriesPoint{now - 1, float64(100 - i)})
		} else {
			points = append(points, &TimeSeriesPoint{now, float64(100 - i)})
			points = append(points, &TimeSeriesPoint{now - 1, float64(i)})
		}
	}

	result := testCreateUpdateManyFetch(t, Average, 0.5, points, 2, 10)

	if result.values[0] != 99.0 {
		t.Fatalf("Incorrect saved value. Expected %v, received %v", 99.0, result.values[0])
	}
	if result.values[1] != 1.0 {
		t.Fatalf("Incorrect saved value. Expected %v, received %v", 1.0, result.values[1])
	}
}

func TestOpenValidatation(t *testing.T) {

	testOpen := func(data []byte) {
		path, _, _, tearDown := setUpCreate()
		defer tearDown()

		err := ioutil.WriteFile(path, data, 0777)
		if err != nil {
			t.Fatal(err)
		}

		wsp, err := Open(path)
		if wsp != nil {
			t.Fatal("Opened bad file")
		}
		if err == nil {
			t.Fatal("No error with file")
		}
	}

	testWrite := func(data []byte) {
		path, _, _, tearDown := setUpCreate()
		defer tearDown()

		err := ioutil.WriteFile(path, data, 0777)
		if err != nil {
			t.Fatal(err)
		}

		wsp, err := Open(path)
		if wsp == nil || err != nil {
			t.Fatal("Open error")
		}

		err = wsp.Update(42, int(time.Now().Unix()))
		if err == nil {
			t.Fatal("Update broken wsp without error")
		}

		points := makeGoodPoints(1000, 2, func(i int) float64 { return float64(i) })
		err = wsp.UpdateMany(points)
		if err == nil {
			t.Fatal("Update broken wsp without error")
		}
	}

	// Bad file with archiveCount = 1296223489
	testOpen([]byte{
		0xb8, 0x81, 0xd1, 0x1,
		0xc, 0x0, 0x1, 0x2,
		0x2e, 0x0, 0x0, 0x0,
		0x4d, 0x42, 0xcd, 0x1, // archiveCount
		0xc, 0x0, 0x2, 0x2,
	})

	fullHeader := []byte{
		// Metadata
		0x00, 0x00, 0x00, 0x01, // Aggregation type
		0x00, 0x00, 0x0e, 0x10, // Max retention
		0x3f, 0x00, 0x00, 0x00, // xFilesFactor
		0x00, 0x00, 0x00, 0x03, // Retention count
		// Archive Info
		// Retention 1 (1, 300)
		0x00, 0x00, 0x00, 0x34, // offset
		0x00, 0x00, 0x00, 0x01, // secondsPerPoint
		0x00, 0x00, 0x01, 0x2c, // numberOfPoints
		// Retention 2 (60, 30)
		0x00, 0x00, 0x0e, 0x44, // offset
		0x00, 0x00, 0x00, 0x3c, // secondsPerPoint
		0x00, 0x00, 0x00, 0x1e, // numberOfPoints
		// Retention 3 (300, 12)
		0x00, 0x00, 0x0f, 0xac, // offset
		0x00, 0x00, 0x01, 0x2c, // secondsPerPoint
		0x00, 0x00, 0x00, 0x0c, // numberOfPoints
	}

	for i := 0; i < len(fullHeader); i++ {
		testOpen(fullHeader[:i])
	}

	testWrite(fullHeader)
}
