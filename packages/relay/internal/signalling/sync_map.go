package signalling

import "sync"

type SyncMapWrapper[K comparable, V any] struct {
	sm sync.Map
}

func NewSyncMapWrapper[K comparable, V any]() *SyncMapWrapper[K, V] {
	return &SyncMapWrapper[K, V]{}
}

func (sw *SyncMapWrapper[K, V]) Store(key K, value V) {
	sw.sm.Store(key, value)
}

func (sw *SyncMapWrapper[K, V]) Load(key K) (V, bool) {
	val, ok := sw.sm.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return val.(V), true
}

func (sw *SyncMapWrapper[K, V]) LoadAndDelete(key K) (V, bool) {
	val, ok := sw.sm.LoadAndDelete(key)
	if !ok {
		var zero V
		return zero, false
	}
	return val.(V), true
}

func (sw *SyncMapWrapper[K, V]) Delete(key K) {
	sw.sm.Delete(key)
}

func (sw *SyncMapWrapper[K, V]) Range(f func(key K, value V) bool) {
	sw.sm.Range(func(key, value interface{}) bool {
		return f(key.(K), value.(V))
	})
}

func (sw *SyncMapWrapper[K, V]) Len() int {
	count := 0
	sw.sm.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

func (sw *SyncMapWrapper[K, V]) Clear() {
	sw.sm.Range(func(key, value interface{}) bool {
		sw.sm.Delete(key)
		return true
	})
}

func (sw *SyncMapWrapper[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	val, loaded := sw.sm.LoadOrStore(key, value)
	return val.(V), loaded
}
