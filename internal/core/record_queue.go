package core

import "slices"

// LogRecordView presents pinned history followed by the two spans of a ring.
// Its slices are read-only and valid only during the RenderRecords callback.
type LogRecordView [3][]LogRecord

func (v LogRecordView) Len() int {
	return len(v[0]) + len(v[1]) + len(v[2])
}

func (v LogRecordView) All(yield func(int, LogRecord) bool) {
	index := 0
	for _, span := range v {
		for _, record := range span {
			if !yield(index, record) {
				return
			}
			index++
		}
	}
}

func (v LogRecordView) at(index int) LogRecord {
	for _, span := range v {
		if index < len(span) {
			return span[index]
		}
		index -= len(span)
	}
	panic("log record index out of range")
}

func (v LogRecordView) clone() []LogRecord { return slices.Concat(v[:]...) }

func (v LogRecordView) texts() []string {
	lines := make([]string, v.Len())
	for index, record := range v.All {
		lines[index] = record.Text
	}
	return lines
}

// recordQueue grows only when its retained count grows. Once full, removing
// and replacing the oldest record reuses the same backing storage.
type recordQueue struct {
	rows        []LogRecord
	head, count int
}

func (q *recordQueue) view(prefix []LogRecord) LogRecordView {
	first := min(q.count, len(q.rows)-q.head)
	return LogRecordView{prefix, q.rows[q.head : q.head+first], q.rows[:q.count-first]}
}

func (q *recordQueue) push(record LogRecord) {
	if q.count == len(q.rows) {
		view := q.view(nil)
		q.rows = make([]LogRecord, max(8, q.count+q.count/2))
		copied := copy(q.rows, view[1])
		copy(q.rows[copied:], view[2])
		q.head = 0
	}
	q.rows[(q.head+q.count)%len(q.rows)] = record
	q.count++
}

func (q *recordQueue) pop() LogRecord {
	record := q.rows[q.head]
	q.rows[q.head] = LogRecord{}
	q.head = (q.head + 1) % len(q.rows)
	q.count--
	return record
}

type recordList struct {
	prefix []LogRecord
	tail   recordQueue
}

func (l *recordList) view() LogRecordView { return l.tail.view(l.prefix) }

func (l *recordList) reset() {
	for _, span := range l.view() {
		clear(span)
	}
	l.prefix = l.prefix[:0]
	l.tail.head, l.tail.count = 0, 0
}
