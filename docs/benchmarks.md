# Benchmark Results

> go-p2p lock-free data structures vs mutex-based implementations

## Environment

| | |
|---|---|
| OS | Windows 10 |
| CPU | AMD Ryzen 5 3500 (6-Core) |
| Go | 1.21 |
| Benchmark | `go test -bench=. -benchmem -count=3` |

---

## Summary

```
┌────────────────────┬─────────────┬─────────────────────────────────┐
│ Data Structure     │ Winner      │ Notes                           │
├────────────────────┼─────────────┼─────────────────────────────────┤
│ Queue              │ Mutex       │ Go's mutex is highly optimized  │
│ HashMap (read)     │ Lock-free   │ No contention between readers   │
│ HashMap (write)    │ Mutex       │ CAS retry overhead              │
│ Priority Queue     │ Mutex       │ Simple, memory efficient        │
└────────────────────┴─────────────┴─────────────────────────────────┘
```

---

## 1. Queue

### Single Thread

```
Operation   Mutex       Lock-free
─────────────────────────────────────
Enqueue     85 ns       80 ns
Dequeue     15 ns       18 ns
```

### Concurrent (mixed enqueue/dequeue)

```
Goroutines  Mutex       Lock-free   Diff
───────────────────────────────────────────
1           88 ns       144 ns      +64%
4           83 ns       149 ns      +79%
8           83 ns       149 ns      +79%
16          85 ns       143 ns      +68%
32          85 ns       140 ns      +65%
64          82 ns       146 ns      +78%
```

**Mutex wins.** Lock-free allocates a node per enqueue (32 B/op).

### MPSC Pattern

```
Producers   Mutex       Lock-free
─────────────────────────────────────
1           100 ns      185 ns
4           147 ns      303 ns
8           163 ns      297 ns
16          184 ns      322 ns
```

### Why Mutex wins?

**1. Go sync.Mutex 내부 구조**

```go
// src/sync/mutex.go
type Mutex struct {
    state int32   // 락 상태 (locked, woken, starving, waiter count)
    sema  uint32  // 세마포어 (runtime_Semacquire/release)
}
```

Go의 Mutex는 3가지 모드로 동작:
- **Fast path**: `atomic.CompareAndSwapInt32(&m.state, 0, mutexLocked)` - 경합 없으면 CAS 한 번으로 끝
- **Spin**: 짧은 경합이면 `runtime_doSpin()` 호출해서 CPU 양보 없이 대기
- **Sleep**: 오래 걸리면 `runtime_SemacquireMutex()`로 고루틴 파킹

**2. Lock-free Queue가 느린 이유**

```go
// 매 Enqueue마다 힙 할당 발생
newNode := &node{value: value}  // 32 bytes allocation

// CAS 실패 시 재시도 루프
for {
    tail := atomic.LoadPointer(&q.tail)
    next := atomic.LoadPointer(&tail.next)
    if atomic.CompareAndSwapPointer(&tail.next, nil, newNode) {
        // 성공
    }
    // 실패하면 처음부터 다시
}
```

- `&node{}` 할당: Go 힙에 32바이트 할당 → GC 대상 증가
- CAS 실패 시 루프 재시도 → CPU 사이클 낭비
- `atomic.LoadPointer` 호출마다 메모리 배리어 발생

**3. 메모리 배리어 비용**

```
x86-64에서 atomic 연산:
- atomic.Load: MOV (암묵적 acquire 시맨틱)
- atomic.Store: MOV + MFENCE 또는 XCHG
- atomic.CAS: LOCK CMPXCHG (캐시 라인 락)

LOCK prefix는 해당 캐시 라인을 exclusive 상태로 만듦
→ 다른 코어가 같은 라인 접근 시 캐시 미스 발생
```

---

## 2. HashMap

### Single Thread

```
Operation   Mutex       Lock-free
─────────────────────────────────────
Put         1.7 μs      206 μs
Get         193 ns      8.8 μs
```

Lock-free uses sorted linked list internally → O(n) traversal.

### Concurrent (75% read, 25% write)

```
Goroutines  Mutex       Lock-free   Diff
───────────────────────────────────────────
1           377 ns      194 ns      -49%
4           375 ns      189 ns      -50%
8           377 ns      188 ns      -50%
16          371 ns      184 ns      -50%
32          377 ns      197 ns      -48%
```

**Lock-free wins (2x faster).** No lock contention on reads.

### Why this result?

**1. 단일 스레드에서 Mutex가 빠른 이유**

```go
// Mutex HashMap - Go 내장 map 사용
type HashMap struct {
    data map[string]interface{}  // O(1) 해시 테이블
    mu   sync.RWMutex
}

// Lock-free HashMap - 정렬된 연결 리스트
type HashMap struct {
    head unsafe.Pointer  // *hashNode → *hashNode → *hashNode → ...
}
```

Go 내장 `map`은 해시 테이블:
- `runtime/map.go`에서 버킷 배열 + 체이닝으로 구현
- Get/Put: O(1) 평균
- 해시 충돌 시에도 버킷 내 8개 슬롯 순차 검색 (캐시 친화적)

Lock-free는 Split-Ordered List:
- 모든 요소가 하나의 정렬된 리스트에 저장
- Get/Put: O(n) 최악, O(n/buckets) 평균
- 포인터 체이싱 → 캐시 미스 많음

**2. 동시성에서 Lock-free가 빠른 이유**

```go
// RWMutex 구조
type RWMutex struct {
    w           Mutex   // 쓰기 락
    writerSem   uint32  // 쓰기 대기 세마포어
    readerSem   uint32  // 읽기 대기 세마포어
    readerCount int32   // 현재 읽기 락 개수
    readerWait  int32   // 쓰기 대기 중 읽기 완료 대기 수
}
```

RWMutex.RLock() 내부:
```go
func (rw *RWMutex) RLock() {
    // 읽기 카운터 증가 (atomic)
    if atomic.AddInt32(&rw.readerCount, 1) < 0 {
        // 쓰기 락 대기 중이면 세마포어에서 대기
        runtime_SemacquireMutex(&rw.readerSem, false, 0)
    }
}
```

문제점:
- `atomic.AddInt32(&rw.readerCount, 1)` → 모든 읽기가 같은 캐시 라인 경합
- 쓰기 대기 중이면 새 읽기도 블로킹됨

Lock-free Get:
```go
func (m *HashMap) Get(key string) (interface{}, bool) {
    curr := atomic.LoadPointer(&m.head)
    for curr != nil {
        if curr.key == key && atomic.LoadInt32(&curr.deleted) == 0 {
            return atomic.LoadPointer(&curr.value), true
        }
        curr = atomic.LoadPointer(&curr.next)
    }
    return nil, false
}
```

- 읽기끼리 전혀 경합 없음
- 각자 독립적으로 리스트 순회
- 쓰기가 있어도 읽기는 블로킹 안 됨

**3. 캐시 라인 경합 (False Sharing)**

```
┌─────────────────────────────────────────────────────────────┐
│                    Cache Line (64 bytes)                     │
├─────────────────────────────────────────────────────────────┤
│  RWMutex.readerCount  │  RWMutex.readerWait  │  ...         │
└─────────────────────────────────────────────────────────────┘
         ↑                        ↑
       Core 0                   Core 1
       RLock()                  RLock()

두 코어가 같은 캐시 라인의 readerCount를 수정
→ 캐시 라인이 Core 0 → Core 1 → Core 0 핑퐁
→ 매번 ~100 사이클 손실
```

---

## 3. Priority Queue

### Single Thread

```
Operation   Mutex(Heap) Lock-free(SkipList)
──────────────────────────────────────────────
Push        613 ns      608 ns
Pop         282 ns      214 ns
```

### Concurrent

```
Goroutines  Mutex       Lock-free
─────────────────────────────────────
1           166 ns      ~200 ns
4           184 ns      ~200 ns
8           182 ns      ~200 ns
16          184 ns      ~200 ns
```

**Mutex wins.** Skip list has high memory overhead (311 B/op per node).

### Why Mutex wins?

**1. Heap vs Skip List 메모리 구조**

```go
// Mutex - container/heap (배열 기반)
type itemHeap []*Item  // 연속된 메모리, 캐시 친화적

// 부모-자식 관계가 인덱스로 계산됨
// parent(i) = (i-1)/2
// left(i)   = 2*i + 1
// right(i)  = 2*i + 2

// Lock-free - Skip List (포인터 기반)
type skipNode struct {
    priority int64
    value    unsafe.Pointer
    level    int
    next     [32]unsafe.Pointer  // 최대 32개 레벨 포인터!
    marked   int32
}
// 노드당 최소 8 + 8 + 4 + 256 + 4 = 280+ bytes
```

Heap Push/Pop:
```go
// heap.Push - O(log n) heapify-up
func up(h Interface, j int) {
    for {
        i := (j - 1) / 2  // parent
        if i == j || !h.Less(j, i) {
            break
        }
        h.Swap(i, j)  // 배열 내 스왑 (캐시 히트 높음)
        j = i
    }
}
```

Skip List Push:
```go
// 각 레벨에서 삽입 위치 찾기
for level := maxLevel - 1; level >= 0; level-- {
    curr := atomic.LoadPointer(&pred.next[level])  // 포인터 체이싱
    for curr != nil && curr.priority > priority {
        pred = curr
        curr = atomic.LoadPointer(&curr.next[level])  // 또 포인터 체이싱
    }
}
// 레벨 수만큼 CAS 필요
```

**2. 캐시 효율성**

```
Heap (배열):
┌────┬────┬────┬────┬────┬────┬────┬────┐
│ 0  │ 1  │ 2  │ 3  │ 4  │ 5  │ 6  │ 7  │  ← 연속 메모리
└────┴────┴────┴────┴────┴────┴────┴────┘
  ↑ L1 캐시에 한 번에 로드

Skip List (포인터):
┌────┐     ┌────┐     ┌────┐     ┌────┐
│ A  │────→│ B  │────→│ C  │────→│ D  │  ← 흩어진 메모리
└────┘     └────┘     └────┘     └────┘
  ↑           ↑           ↑
 메모리 A    메모리 B    메모리 C

각 노드 접근마다 캐시 미스 가능성
```

**3. Go GC 영향**

```go
// Heap - 단일 슬라이스
items := make([]*Item, 0, 1000)
// GC가 추적할 객체: 슬라이스 1개 + Item 포인터들

// Skip List - 노드마다 할당
for each push {
    node := &skipNode{...}  // 새 객체 할당
    // GC가 추적할 객체가 계속 증가
}
```

Go GC는 mark-and-sweep 방식:
- 힙 객체가 많을수록 마킹 시간 증가
- Skip List는 노드 수 = 요소 수
- Heap은 슬라이스 재사용으로 할당 최소화

---

## Recommendation

```
Component        Implementation
────────────────────────────────────
PeerManager      sync.Map
MessageQueue     Mutex + slice
Mempool          Mutex + heap
BroadcastCache   sync.Map
```

---

## Run Benchmarks

```bash
# all
go test -bench=. -benchmem ./pkg/ds/...

# queue only
go test -bench=BenchmarkQueue -benchmem ./pkg/ds/...

# hashmap only
go test -bench=BenchmarkHashMap -benchmem ./pkg/ds/...

# priority queue only
go test -bench=BenchmarkPriorityQueue -benchmem ./pkg/ds/...

# with count (for stable results)
go test -bench=. -benchmem -count=5 ./pkg/ds/...

# cpu profile
go test -bench=BenchmarkQueue -cpuprofile=cpu.prof ./pkg/ds/...
go tool pprof cpu.prof
```
