package ds

import (
	"sync"
	"testing"

	"github.com/go-p2p-network/go-p2p/pkg/ds/lockfree"
	syncds "github.com/go-p2p-network/go-p2p/pkg/ds/sync"
)

// =============================================================================
// Queue 벤치마크
// =============================================================================

// BenchmarkQueueMutex_Enqueue는 Mutex 기반 큐의 Enqueue 성능을 측정합니다.
func BenchmarkQueueMutex_Enqueue(b *testing.B) {
	q := syncds.NewQueue()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Enqueue(i)
	}
}

// BenchmarkQueueLockFree_Enqueue는 Lock-free 큐의 Enqueue 성능을 측정합니다.
func BenchmarkQueueLockFree_Enqueue(b *testing.B) {
	q := lockfree.NewQueue()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Enqueue(i)
	}
}

// BenchmarkQueueMutex_Dequeue는 Mutex 기반 큐의 Dequeue 성능을 측정합니다.
func BenchmarkQueueMutex_Dequeue(b *testing.B) {
	q := syncds.NewQueue()

	// 미리 데이터 채우기
	for i := 0; i < b.N; i++ {
		q.Enqueue(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Dequeue()
	}
}

// BenchmarkQueueLockFree_Dequeue는 Lock-free 큐의 Dequeue 성능을 측정합니다.
func BenchmarkQueueLockFree_Dequeue(b *testing.B) {
	q := lockfree.NewQueue()

	// 미리 데이터 채우기
	for i := 0; i < b.N; i++ {
		q.Enqueue(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Dequeue()
	}
}

// =============================================================================
// 동시성 벤치마크 (진짜 차이가 나는 테스트!)
// =============================================================================

// BenchmarkQueueMutex_Concurrent는 Mutex 기반 큐의 동시성 성능을 측정합니다.
//
// [테스트 시나리오]
// - 고루틴 수: 1, 2, 4, 8, 16, 32, 64
// - 각 고루틴이 Enqueue와 Dequeue를 번갈아 수행
// - 경합(contention)이 많을수록 Mutex의 단점이 드러남
func BenchmarkQueueMutex_Concurrent(b *testing.B) {
	for _, goroutines := range []int{1, 2, 4, 8, 16, 32, 64} {
		b.Run(concurrentName(goroutines), func(b *testing.B) {
			q := syncds.NewQueue()

			// 초기 데이터
			for i := 0; i < 1000; i++ {
				q.Enqueue(i)
			}

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					if i%2 == 0 {
						q.Enqueue(i)
					} else {
						q.Dequeue()
					}
					i++
				}
			})
		})
	}
}

// BenchmarkQueueLockFree_Concurrent는 Lock-free 큐의 동시성 성능을 측정합니다.
func BenchmarkQueueLockFree_Concurrent(b *testing.B) {
	for _, goroutines := range []int{1, 2, 4, 8, 16, 32, 64} {
		b.Run(concurrentName(goroutines), func(b *testing.B) {
			q := lockfree.NewQueue()

			// 초기 데이터
			for i := 0; i < 1000; i++ {
				q.Enqueue(i)
			}

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					if i%2 == 0 {
						q.Enqueue(i)
					} else {
						q.Dequeue()
					}
					i++
				}
			})
		})
	}
}

// =============================================================================
// MPSC (Multiple Producer, Single Consumer) 패턴
// =============================================================================

// BenchmarkQueueMutex_MPSC는 Mutex 기반 큐의 MPSC 패턴 성능을 측정합니다.
//
// [MPSC란?]
// P2P 네트워크에서 흔한 패턴:
// - 여러 피어(Producer)가 메시지를 보냄 → 큐에 Enqueue
// - 하나의 처리 고루틴(Consumer)이 메시지 처리 → Dequeue
func BenchmarkQueueMutex_MPSC(b *testing.B) {
	for _, producers := range []int{1, 4, 8, 16} {
		b.Run(mpscName(producers), func(b *testing.B) {
			q := syncds.NewQueue()
			itemsPerProducer := b.N / producers

			var wg sync.WaitGroup

			b.ResetTimer()

			// 생산자들
			for p := 0; p < producers; p++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < itemsPerProducer; i++ {
						q.Enqueue(i)
					}
				}()
			}

			// 소비자 (1개)
			consumed := 0
			go func() {
				for consumed < itemsPerProducer*producers {
					if _, ok := q.Dequeue(); ok {
						consumed++
					}
				}
			}()

			wg.Wait()
		})
	}
}

// BenchmarkQueueLockFree_MPSC는 Lock-free 큐의 MPSC 패턴 성능을 측정합니다.
func BenchmarkQueueLockFree_MPSC(b *testing.B) {
	for _, producers := range []int{1, 4, 8, 16} {
		b.Run(mpscName(producers), func(b *testing.B) {
			q := lockfree.NewQueue()
			itemsPerProducer := b.N / producers

			var wg sync.WaitGroup

			b.ResetTimer()

			// 생산자들
			for p := 0; p < producers; p++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < itemsPerProducer; i++ {
						q.Enqueue(i)
					}
				}()
			}

			// 소비자 (1개)
			consumed := 0
			go func() {
				for consumed < itemsPerProducer*producers {
					if _, ok := q.Dequeue(); ok {
						consumed++
					}
				}
			}()

			wg.Wait()
		})
	}
}

// =============================================================================
// 유틸리티 함수
// =============================================================================

func concurrentName(goroutines int) string {
	return "goroutines-" + itoa(goroutines)
}

func mpscName(producers int) string {
	return "producers-" + itoa(producers)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string('0'+byte(i%10)) + s
		i /= 10
	}
	return s
}

// =============================================================================
// HashMap 벤치마크
// =============================================================================

// BenchmarkHashMapMutex_Put은 Mutex 기반 해시맵의 Put 성능을 측정합니다.
func BenchmarkHashMapMutex_Put(b *testing.B) {
	m := syncds.NewHashMap()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Put(itoa(i), i)
	}
}

// BenchmarkHashMapLockFree_Put은 Lock-free 해시맵의 Put 성능을 측정합니다.
func BenchmarkHashMapLockFree_Put(b *testing.B) {
	m := lockfree.NewHashMap(64)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Put(itoa(i), i)
	}
}

// BenchmarkHashMapMutex_Get은 Mutex 기반 해시맵의 Get 성능을 측정합니다.
func BenchmarkHashMapMutex_Get(b *testing.B) {
	m := syncds.NewHashMap()

	// 미리 데이터 채우기
	for i := 0; i < 10000; i++ {
		m.Put(itoa(i), i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Get(itoa(i % 10000))
	}
}

// BenchmarkHashMapLockFree_Get은 Lock-free 해시맵의 Get 성능을 측정합니다.
func BenchmarkHashMapLockFree_Get(b *testing.B) {
	m := lockfree.NewHashMap(64)

	// 미리 데이터 채우기
	for i := 0; i < 10000; i++ {
		m.Put(itoa(i), i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Get(itoa(i % 10000))
	}
}

// BenchmarkHashMapMutex_Concurrent는 Mutex 기반 해시맵의 동시성 성능을 측정합니다.
func BenchmarkHashMapMutex_Concurrent(b *testing.B) {
	for _, goroutines := range []int{1, 4, 8, 16, 32} {
		b.Run(concurrentName(goroutines), func(b *testing.B) {
			m := syncds.NewHashMap()

			// 초기 데이터
			for i := 0; i < 1000; i++ {
				m.Put(itoa(i), i)
			}

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					key := itoa(i % 1000)
					if i%4 == 0 {
						m.Put(key, i)
					} else {
						m.Get(key)
					}
					i++
				}
			})
		})
	}
}

// BenchmarkHashMapLockFree_Concurrent는 Lock-free 해시맵의 동시성 성능을 측정합니다.
func BenchmarkHashMapLockFree_Concurrent(b *testing.B) {
	for _, goroutines := range []int{1, 4, 8, 16, 32} {
		b.Run(concurrentName(goroutines), func(b *testing.B) {
			m := lockfree.NewHashMap(64)

			// 초기 데이터
			for i := 0; i < 1000; i++ {
				m.Put(itoa(i), i)
			}

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					key := itoa(i % 1000)
					if i%4 == 0 {
						m.Put(key, i)
					} else {
						m.Get(key)
					}
					i++
				}
			})
		})
	}
}

// =============================================================================
// Go sync.Map과 비교 (baseline)
// =============================================================================

// BenchmarkSyncMap_Concurrent는 Go 표준 sync.Map의 성능을 측정합니다.
func BenchmarkSyncMap_Concurrent(b *testing.B) {
	for _, goroutines := range []int{1, 4, 8, 16, 32} {
		b.Run(concurrentName(goroutines), func(b *testing.B) {
			var m sync.Map

			// 초기 데이터
			for i := 0; i < 1000; i++ {
				m.Store(itoa(i), i)
			}

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					key := itoa(i % 1000)
					if i%4 == 0 {
						m.Store(key, i)
					} else {
						m.Load(key)
					}
					i++
				}
			})
		})
	}
}

// =============================================================================
// PriorityQueue 벤치마크
// =============================================================================

// BenchmarkPriorityQueueMutex_Push는 Mutex 기반 우선순위 큐의 Push 성능을 측정합니다.
func BenchmarkPriorityQueueMutex_Push(b *testing.B) {
	pq := syncds.NewPriorityQueue()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.Push(i, int64(i))
	}
}

// BenchmarkPriorityQueueLockFree_Push는 Lock-free 우선순위 큐의 Push 성능을 측정합니다.
func BenchmarkPriorityQueueLockFree_Push(b *testing.B) {
	pq := lockfree.NewPriorityQueue()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.Push(i, int64(i))
	}
}

// BenchmarkPriorityQueueMutex_Pop는 Mutex 기반 우선순위 큐의 Pop 성능을 측정합니다.
func BenchmarkPriorityQueueMutex_Pop(b *testing.B) {
	pq := syncds.NewPriorityQueue()

	// 미리 데이터 채우기
	for i := 0; i < b.N; i++ {
		pq.Push(i, int64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.Pop()
	}
}

// BenchmarkPriorityQueueLockFree_Pop는 Lock-free 우선순위 큐의 Pop 성능을 측정합니다.
func BenchmarkPriorityQueueLockFree_Pop(b *testing.B) {
	pq := lockfree.NewPriorityQueue()

	// 미리 데이터 채우기
	for i := 0; i < b.N; i++ {
		pq.Push(i, int64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.Pop()
	}
}

// BenchmarkPriorityQueueMutex_Concurrent는 Mutex 기반 우선순위 큐의 동시성 성능을 측정합니다.
func BenchmarkPriorityQueueMutex_Concurrent(b *testing.B) {
	for _, goroutines := range []int{1, 4, 8, 16} {
		b.Run(concurrentName(goroutines), func(b *testing.B) {
			pq := syncds.NewPriorityQueue()

			// 초기 데이터
			for i := 0; i < 1000; i++ {
				pq.Push(i, int64(i))
			}

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					if i%2 == 0 {
						pq.Push(i, int64(i%1000))
					} else {
						pq.Pop()
					}
					i++
				}
			})
		})
	}
}

// BenchmarkPriorityQueueLockFree_Concurrent는 Lock-free 우선순위 큐의 동시성 성능을 측정합니다.
func BenchmarkPriorityQueueLockFree_Concurrent(b *testing.B) {
	for _, goroutines := range []int{1, 4, 8, 16} {
		b.Run(concurrentName(goroutines), func(b *testing.B) {
			pq := lockfree.NewPriorityQueue()

			// 초기 데이터
			for i := 0; i < 1000; i++ {
				pq.Push(i, int64(i))
			}

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					if i%2 == 0 {
						pq.Push(i, int64(i%1000))
					} else {
						pq.Pop()
					}
					i++
				}
			})
		})
	}
}
