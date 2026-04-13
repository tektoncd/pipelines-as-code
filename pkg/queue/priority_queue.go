package queue

import (
	"container/heap"
)

type (
	key = string
)

type item struct {
	key      string
	priority int64
	sequence int64
	index    int
}

type priorityQueue struct {
	items     []*item
	itemByKey map[string]*item
	sequence  int64
}

func (pq *priorityQueue) isPending(key key) bool {
	if _, ok := pq.itemByKey[key]; ok {
		return true
	}
	return false
}

func (pq *priorityQueue) add(key key, priority int64) {
	if _, ok := pq.itemByKey[key]; ok {
		return
	}
	heap.Push(pq, &item{key: key, priority: priority, sequence: pq.sequence})
	pq.sequence++
}

func (pq *priorityQueue) remove(key key) {
	if item, ok := pq.itemByKey[key]; ok {
		heap.Remove(pq, item.index)
		delete(pq.itemByKey, key)
	}
}

func (pq *priorityQueue) pop() *item {
	item, _ := heap.Pop(pq).(*item)
	return item
}

func (pq *priorityQueue) peek() *item {
	return pq.items[0]
}

func (pq priorityQueue) Len() int { return len(pq.items) }

func (pq priorityQueue) Less(i, j int) bool {
	if pq.items[i].priority == pq.items[j].priority {
		return pq.items[i].sequence < pq.items[j].sequence
	}
	return pq.items[i].priority < pq.items[j].priority
}

func (pq priorityQueue) Swap(i, j int) {
	pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
	pq.items[i].index = i
	pq.items[j].index = j
}

func (pq *priorityQueue) Push(x any) {
	n := len(pq.items)
	item, _ := x.(*item)
	item.index = n
	pq.items = append(pq.items, item)
	pq.itemByKey[item.key] = item
}

func (pq *priorityQueue) Pop() any {
	old := pq.items
	n := len(old)
	item := old[n-1]
	item.index = -1
	pq.items = old[0 : n-1]
	delete(pq.itemByKey, item.key)
	return item
}
