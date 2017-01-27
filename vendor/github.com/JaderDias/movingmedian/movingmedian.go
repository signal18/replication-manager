// Package movingmedian computes the median of a windowed stream of data.
package movingmedian

import "container/heap"

type item struct {
	f         float64
	heapIndex int
}

type itemHeap []*item

func (h itemHeap) Len() int { return len(h) }
func (h itemHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *itemHeap) Push(x interface{}) {
	e := x.(*item)
	e.heapIndex = len(*h)
	*h = append(*h, e)
}

func (h *itemHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

type minItemHeap struct {
	itemHeap
}

func (h minItemHeap) Less(i, j int) bool { return h.itemHeap[i].f < h.itemHeap[j].f }

type maxItemHeap struct {
	itemHeap
}

func (h maxItemHeap) Less(i, j int) bool { return h.itemHeap[i].f > h.itemHeap[j].f }

// MovingMedian computes the moving median of a windowed stream of numbers.
type MovingMedian struct {
	queueIndex int
	nitems     int
	queue      []item
	maxHeap    maxItemHeap
	minHeap    minItemHeap
}

// NewMovingMedian returns a MovingMedian with the given window size.
func NewMovingMedian(size int) MovingMedian {
	m := MovingMedian{
		queue:   make([]item, size),
		maxHeap: maxItemHeap{},
		minHeap: minItemHeap{},
	}

	heap.Init(&m.maxHeap)
	heap.Init(&m.minHeap)
	return m
}

// Push adds an element to the stream, removing old data which has expired from the window.  It runs in O(log windowSize).
func (m *MovingMedian) Push(v float64) {
	if len(m.queue) == 1 {
		m.queue[0].f = v
		return
	}

	itemPtr := &m.queue[m.queueIndex]
	m.queueIndex++
	if m.queueIndex >= len(m.queue) {
		m.queueIndex = 0
	}

	if m.nitems == len(m.queue) {
		minAbove := m.minHeap.itemHeap[0].f
		maxBelow := m.maxHeap.itemHeap[0].f
		itemPtr.f = v
		if itemPtr.heapIndex < m.minHeap.Len() && itemPtr == m.minHeap.itemHeap[itemPtr.heapIndex] {
			if v >= maxBelow {
				heap.Fix(&m.minHeap, itemPtr.heapIndex)
				return
			}

			rotate(&m.maxHeap, &m.minHeap, m.maxHeap.itemHeap, m.minHeap.itemHeap, itemPtr)
			return
		}

		if v <= minAbove {
			heap.Fix(&m.maxHeap, itemPtr.heapIndex)
			return
		}

		rotate(&m.minHeap, &m.maxHeap, m.minHeap.itemHeap, m.maxHeap.itemHeap, itemPtr)
		return
	}

	m.nitems++
	itemPtr.f = v
	if m.minHeap.Len() == 0 || v > m.minHeap.itemHeap[0].f {
		heap.Push(&m.minHeap, itemPtr)
		rebalance(&m.minHeap, &m.maxHeap)
	} else {
		heap.Push(&m.maxHeap, itemPtr)
		rebalance(&m.maxHeap, &m.minHeap)
	}
}

func rebalance(heapA, heapB heap.Interface) {
	if heapA.Len() == (heapB.Len() + 2) {
		moveItem := heap.Pop(heapA)
		heap.Push(heapB, moveItem)
	}
}

func rotate(heapA, heapB heap.Interface, itemHeapA, itemHeapB itemHeap, itemPtr *item) {
	moveItem := itemHeapA[0]
	moveItem.heapIndex = itemPtr.heapIndex
	itemHeapB[itemPtr.heapIndex] = moveItem
	itemHeapA[0] = itemPtr
	heap.Fix(heapB, itemPtr.heapIndex)
	itemPtr.heapIndex = 0
	heap.Fix(heapA, 0)
}

// Median returns the current value of the median from the window.
func (m *MovingMedian) Median() float64 {
	if len(m.queue) == 1 {
		return m.queue[0].f
	}

	if m.maxHeap.Len() == m.minHeap.Len() {
		return (m.maxHeap.itemHeap[0].f + m.minHeap.itemHeap[0].f) / 2
	}

	if m.maxHeap.Len() > m.minHeap.Len() {
		return m.maxHeap.itemHeap[0].f
	}

	return m.minHeap.itemHeap[0].f
}
