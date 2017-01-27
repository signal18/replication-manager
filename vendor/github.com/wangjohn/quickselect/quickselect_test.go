package quickselect

import (
	"sort"
	"testing"
)

type TestData struct {
	Array []int
}

func (t TestData) Len() int {
	return len(t.Array)
}

func (t TestData) Less(i, j int) bool {
	return t.Array[i] < t.Array[j]
}

func (t TestData) Swap(i, j int) {
	t.Array[i], t.Array[j] = t.Array[j], t.Array[i]
}

func TestQuickSelectWithSimpleArray(t *testing.T) {
	fixture := TestData{[]int{50, 20, 30, 25, 45, 2, 6, 10, 3, 4, 5}}
	err := QuickSelect(fixture, 5)
	if err != nil {
		t.Errorf("Shouldn't have raised error: '%s'", err.Error())
	}

	smallestK := fixture.Array[:5]
	expectedK := []int{2, 3, 4, 5, 6}
	if !hasSameElements(smallestK, expectedK) {
		t.Errorf("Expected smallest K elements to be '%s', but got '%s'", expectedK, smallestK)
	}
}

func TestQuickSelectWithRepeatedElements(t *testing.T) {
	fixture := TestData{[]int{2, 10, 5, 3, 2, 6, 2, 6, 10, 3, 4, 5}}
	err := QuickSelect(fixture, 5)
	if err != nil {
		t.Errorf("Shouldn't have raised error: '%s'", err.Error())
	}

	smallestK := fixture.Array[:5]
	expectedK := []int{2, 2, 2, 3, 3}
	if !hasSameElements(smallestK, expectedK) {
		t.Errorf("Expected smallest K elements to be '%s', but got '%s'", expectedK, smallestK)
	}
}

func TestQuickSelectEmptyDataStructure(t *testing.T) {
	fixture := TestData{[]int{}}
	err := QuickSelect(fixture, 0)
	if err == nil {
		t.Errorf("Should have raised error on index outside of array length.")
	}

	err = QuickSelect(fixture, 5)
	if err == nil {
		t.Errorf("Should have raised error on index outside of array length.")
	}

	err = QuickSelect(fixture, -1)
	if err == nil {
		t.Errorf("Should have raised error on index outside of array length.")
	}
}

func TestIntSliceQuickSelect(t *testing.T) {
	fixtures := []struct {
		Array     IntSlice
		ExpectedK []int
	}{
		{[]int{0, 14, 16, 29, 12, 2, 4, 4, 7, 29}, []int{0, 2, 4, 4}},
		{[]int{9, 3, 2, 18}, []int{9, 3, 2, 18}},
		{[]int{16, 29, -11, 25, 28, -14, 10, 4, 7, -27}, []int{-27, -11, -14, 4}},
	}

	for _, fixture := range fixtures {
		err := fixture.Array.QuickSelect(4)
		if err != nil {
			t.Errorf("Shouldn't have raised error: '%s'", err.Error())
		}

		resultK := fixture.Array[:4]
		if !hasSameElements(resultK, fixture.ExpectedK) {
			t.Errorf("Expected smallest K elements to be '%s', but got '%s'", fixture.ExpectedK, resultK)
		}
	}
}

func TestResetCurrentLargest(t *testing.T) {
	fixtures := []struct {
		Array        IntSlice
		ExpectedLast int
	}{
		{[]int{20, 15, 2, 8, 9, 25, 3, 5}, 5},
		{[]int{0, 0, 5, 3, 5, 2}, 2},
		{[]int{3}, 0},
		{[]int{35, 25, 15, 10, 5}, 0},
	}

	for _, fixture := range fixtures {
		indices := make([]int, len(fixture.Array))
		for i := 0; i < len(fixture.Array); i++ {
			indices[i] = i
		}
		resetLargestIndex(indices, IntSlice(fixture.Array))
		lastIndex := indices[len(indices)-1]
		if lastIndex != fixture.ExpectedLast {
			t.Errorf("Expected last index of '%d', but got '%d' instead", fixture.ExpectedLast, lastIndex)
		}
	}
}

func TestNaiveSelectionFinding(t *testing.T) {
	fixtures := []struct {
		Array     IntSlice
		ExpectedK []int
	}{
		{[]int{0, 14, 16, 29, 12, 2, 4, 4, 7, 29}, []int{0, 2, 4, 4}},
		{[]int{9, 3, 2, 18}, []int{9, 3, 2, 18}},
		{[]int{16, 29, -11, 25, 28, -14, 10, 4, 7, -27}, []int{-27, -11, -14, 4}},
		{[]int{10, 25, 15, 35, 26, 40, 55}, []int{10, 15, 25, 26}},
		{[]int{2, 10, 5, 3, 2, 6, 2, 6, 10, 3, 4, 5}, []int{2, 2, 2, 3}},
	}

	for _, fixture := range fixtures {
		naiveSelectionFinding(fixture.Array, 4)

		resultK := fixture.Array[:4]
		if !hasSameElements(resultK, fixture.ExpectedK) {
			t.Errorf("Expected smallest K elements to be '%s', but got '%s'", fixture.ExpectedK, resultK)
		}
	}
}

func TestHeapSelectionFinding(t *testing.T) {
	fixtures := []struct {
		Array     IntSlice
		ExpectedK []int
	}{
		{[]int{0, 14, 16, 29, 12, 2, 4, 4, 7, 29}, []int{0, 2, 4, 4}},
		{[]int{9, 3, 2, 18}, []int{9, 3, 2, 18}},
		{[]int{16, 29, -11, 25, 28, -14, 10, 4, 7, -27}, []int{-27, -11, -14, 4}},
		{[]int{10, 25, 15, 35, 26, 40, 55}, []int{10, 15, 25, 26}},
		{[]int{2, 10, 5, 3, 2, 6, 2, 6, 10, 3, 4, 5}, []int{2, 2, 2, 3}},
	}

	for _, fixture := range fixtures {
		heapSelectionFinding(fixture.Array, 4)

		resultK := fixture.Array[:4]
		if !hasSameElements(resultK, fixture.ExpectedK) {
			t.Errorf("Expected smallest K elements to be '%s', but got '%s'", fixture.ExpectedK, resultK)
		}
	}
}

func TestFloat64SliceQuickSelect(t *testing.T) {
	fixtures := []struct {
		Array     Float64Slice
		ExpectedK []float64
	}{
		{[]float64{0.0, 14.3, 16.5, 29.7, 12.6, 2.4, 4.9, 4.2, 7.1, 29.3}, []float64{0.0, 2.4, 4.2, 4.9}},
		{[]float64{9.3, 3.3, 2.7, 18.5}, []float64{9.3, 3.3, 2.7, 18.5}},
		{[]float64{16.1, 29.3, -11.5, 25.3, 28.8, -14.7, 10.5, 4.4, 7.5, -27.9}, []float64{-27.9, -11.5, -14.7, 4.4}},
	}

	for _, fixture := range fixtures {
		err := fixture.Array.QuickSelect(4)
		if err != nil {
			t.Errorf("Shouldn't have raised error: '%s'", err.Error())
		}

		resultK := fixture.Array[:4]
		if !hasSameElementsFloat64(resultK, fixture.ExpectedK) {
			t.Errorf("Expected smallest K elements to be '%s', but got '%s'", fixture.ExpectedK, resultK)
		}
	}
}

func hasSameElements(array1, array2 []int) bool {
	elements := make(map[int]int)

	for _, elem1 := range array1 {
		elements[elem1]++
	}

	for _, elem2 := range array2 {
		elements[elem2]--
	}

	for _, count := range elements {
		if count != 0 {
			return false
		}
	}
	return true
}

func hasSameElementsFloat64(array1, array2 []float64) bool {
	elements := make(map[float64]int)

	for _, elem1 := range array1 {
		elements[elem1]++
	}

	for _, elem2 := range array2 {
		elements[elem2]--
	}

	for _, count := range elements {
		if count != 0 {
			return false
		}
	}
	return true
}

func bench(b *testing.B, size, k int, quickselect bool) {
	b.StopTimer()
	data := make(IntSlice, size)
	x := ^uint32(0)
	for i := 0; i < b.N; i++ {
		for n := size - 3; n <= size+3; n++ {
			for i := 0; i < len(data); i++ {
				x += x
				x ^= 1
				if int32(x) < 0 {
					x ^= 0x88888eef
				}
				data[i] = int(x % uint32(n/5))
			}
			if quickselect {
				b.StartTimer()
				QuickSelect(data, k)
				b.StopTimer()
			} else {
				b.StartTimer()
				sort.Sort(data)
				b.StopTimer()
			}
		}
	}
}

// Benchmarks for QuickSelect
func BenchmarkQuickSelectSize1e2K1e1(b *testing.B) { bench(b, 1e2, 1e1, true) }

func BenchmarkQuickSelectSize1e3K1e1(b *testing.B) { bench(b, 1e3, 1e1, true) }
func BenchmarkQuickSelectSize1e3K1e2(b *testing.B) { bench(b, 1e3, 1e2, true) }

func BenchmarkQuickSelectSize1e4K1e1(b *testing.B) { bench(b, 1e4, 1e1, true) }
func BenchmarkQuickSelectSize1e4K1e2(b *testing.B) { bench(b, 1e4, 1e2, true) }
func BenchmarkQuickSelectSize1e4K1e3(b *testing.B) { bench(b, 1e4, 1e3, true) }

func BenchmarkQuickSelectSize1e5K1e1(b *testing.B) { bench(b, 1e5, 1e1, true) }
func BenchmarkQuickSelectSize1e5K1e2(b *testing.B) { bench(b, 1e5, 1e2, true) }
func BenchmarkQuickSelectSize1e5K1e3(b *testing.B) { bench(b, 1e5, 1e3, true) }
func BenchmarkQuickSelectSize1e5K1e4(b *testing.B) { bench(b, 1e5, 1e4, true) }

func BenchmarkQuickSelectSize1e6K1e1(b *testing.B) { bench(b, 1e6, 1e1, true) }
func BenchmarkQuickSelectSize1e6K1e2(b *testing.B) { bench(b, 1e6, 1e2, true) }
func BenchmarkQuickSelectSize1e6K1e3(b *testing.B) { bench(b, 1e6, 1e3, true) }
func BenchmarkQuickSelectSize1e6K1e4(b *testing.B) { bench(b, 1e6, 1e4, true) }
func BenchmarkQuickSelectSize1e6K1e5(b *testing.B) { bench(b, 1e6, 1e5, true) }

func BenchmarkQuickSelectSize1e7K1e1(b *testing.B) { bench(b, 1e7, 1e1, true) }
func BenchmarkQuickSelectSize1e7K1e2(b *testing.B) { bench(b, 1e7, 1e2, true) }
func BenchmarkQuickSelectSize1e7K1e3(b *testing.B) { bench(b, 1e7, 1e3, true) }
func BenchmarkQuickSelectSize1e7K1e4(b *testing.B) { bench(b, 1e7, 1e4, true) }
func BenchmarkQuickSelectSize1e7K1e5(b *testing.B) { bench(b, 1e7, 1e5, true) }
func BenchmarkQuickSelectSize1e7K1e6(b *testing.B) { bench(b, 1e7, 1e6, true) }

func BenchmarkQuickSelectSize1e8K1e1(b *testing.B) { bench(b, 1e8, 1e1, true) }
func BenchmarkQuickSelectSize1e8K1e2(b *testing.B) { bench(b, 1e8, 1e2, true) }
func BenchmarkQuickSelectSize1e8K1e3(b *testing.B) { bench(b, 1e8, 1e3, true) }
func BenchmarkQuickSelectSize1e8K1e4(b *testing.B) { bench(b, 1e8, 1e4, true) }
func BenchmarkQuickSelectSize1e8K1e5(b *testing.B) { bench(b, 1e8, 1e5, true) }
func BenchmarkQuickSelectSize1e8K1e6(b *testing.B) { bench(b, 1e8, 1e6, true) }
func BenchmarkQuickSelectSize1e8K1e7(b *testing.B) { bench(b, 1e8, 1e7, true) }

// Benchmarks for sorting
func BenchmarkSortSize1e2K1e1(b *testing.B) { bench(b, 1e2, 1e1, false) }
func BenchmarkSortSize1e3K1e1(b *testing.B) { bench(b, 1e3, 1e1, false) }
func BenchmarkSortSize1e4K1e1(b *testing.B) { bench(b, 1e4, 1e1, false) }
func BenchmarkSortSize1e5K1e1(b *testing.B) { bench(b, 1e5, 1e1, false) }
func BenchmarkSortSize1e6K1e1(b *testing.B) { bench(b, 1e6, 1e1, false) }
func BenchmarkSortSize1e7K1e1(b *testing.B) { bench(b, 1e7, 1e1, false) }
func BenchmarkSortSize1e8K1e1(b *testing.B) { bench(b, 1e8, 1e1, false) }
