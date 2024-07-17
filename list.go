package moqtransport

import (
	"iter"
	"sync"
	"sync/atomic"
)

type rwlockwrapper struct {
	lock *sync.RWMutex
}

func (l *rwlockwrapper) Lock() {
	l.lock.RLock()
}

func (l *rwlockwrapper) Unlock() {
	l.lock.RUnlock()
}

const defaultSize = 1024

type list[E any] struct {
	elements []E
	lock     sync.RWMutex
	cv       sync.Cond
	closed   atomic.Bool
}

func (l *list[E]) init(size int) *list[E] {
	l.elements = make([]E, 0, size)
	l.lock = sync.RWMutex{}
	l.cv = *sync.NewCond(&rwlockwrapper{
		lock: &l.lock,
	})
	l.closed = atomic.Bool{}
	return l
}

func newListWithSize[E any](size int) *list[E] {
	return new(list[E]).init(size)
}

func newList[E any]() *list[E] {
	return newListWithSize[E](defaultSize)
}

func (l *list[E]) append(e E) E {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.elements = append(l.elements, e)
	l.cv.Broadcast()
	return e
}

func (l *list[E]) close() {
	l.closed.Store(true)
	l.cv.Broadcast()
}

func (l *list[E]) Read() iter.Seq[E] {
	return func(yield func(E) bool) {
		i := 0
		for {
			l.lock.RLock()
			for len(l.elements) <= i {
				l.cv.Wait()
			}
			if l.closed.Load() {
				l.lock.Unlock()
				return
			}
			e := l.elements[i]
			l.lock.Unlock()
			if !yield(e) {
				return
			}
			i++
		}
	}
}
