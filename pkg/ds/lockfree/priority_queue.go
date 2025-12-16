package lockfree

import (
	"math"
	"math/rand"
	"sync/atomic"
	"unsafe"
)

// =============================================================================
// Lock-free Priority Queue (Skip List 기반)
// =============================================================================

// PriorityQueue는 Lock-free 우선순위 큐입니다.
//
// [왜 Skip List?]
// 힙(Heap)은 Lock-free로 만들기 어렵습니다:
// - heapify 연산이 여러 노드를 동시에 수정
// - CAS 한 번으로 처리 불가능
//
// Skip List는 Lock-free 구현이 가능합니다:
// - 삽입/삭제가 로컬 포인터 수정만 필요
// - CAS로 처리 가능
//
// [Skip List란?]
// 정렬된 연결 리스트 + 여러 레벨의 "익스프레스 레인"
//
// 예시:
// Level 3: HEAD ─────────────────────────────────→ 100 ─────→ NIL
// Level 2: HEAD ─────────→ 30 ─────────────────→ 100 ─────→ NIL
// Level 1: HEAD ───→ 10 ─→ 30 ─────→ 50 ───→ 80 ─→ 100 ─────→ NIL
// Level 0: HEAD → 5 → 10 → 30 → 40 → 50 → 70 → 80 → 100 → 120 → NIL
//
// [시간 복잡도]
// - 검색: O(log n) 평균
// - 삽입: O(log n) 평균
// - 삭제: O(log n) 평균
//
// [확률적 레벨 결정]
// 새 노드의 레벨은 동전 던지기로 결정:
// - Level 1: 100% (항상)
// - Level 2: 50% (동전 앞면이면)
// - Level 3: 25% (연속 2번 앞면)
// - ...
type PriorityQueue struct {
	// head는 Skip List의 헤드 노드입니다.
	// 모든 레벨에서 시작점 역할
	head unsafe.Pointer // *skipNode

	// maxLevel은 현재 최대 레벨입니다.
	maxLevel int32

	// count는 현재 저장된 항목 수입니다.
	count int64
}

// MaxLevel은 Skip List의 최대 레벨입니다.
const MaxLevel = 32

// skipNode는 Skip List의 노드입니다.
type skipNode struct {
	// priority는 우선순위입니다 (높을수록 앞에 위치).
	priority int64

	// value는 저장된 값입니다.
	value unsafe.Pointer // *interface{}

	// level은 이 노드의 레벨입니다 (1 ~ MaxLevel).
	level int

	// next는 각 레벨별 다음 노드 포인터 배열입니다.
	// next[0]이 가장 낮은 레벨 (모든 노드 연결)
	// next[level-1]이 가장 높은 레벨
	next [MaxLevel]unsafe.Pointer // []*skipNode

	// marked는 논리적 삭제 표시입니다.
	marked int32
}

// Item은 우선순위 큐의 항목입니다.
type Item struct {
	Value    interface{}
	Priority int64
}

// NewPriorityQueue는 새로운 Lock-free 우선순위 큐를 생성합니다.
func NewPriorityQueue() *PriorityQueue {
	// 헤드 노드 생성 (최대 우선순위로 설정)
	head := &skipNode{
		priority: math.MaxInt64,
		level:    MaxLevel,
	}

	pq := &PriorityQueue{
		head:     unsafe.Pointer(head),
		maxLevel: 1,
	}

	return pq
}

// randomLevel은 새 노드의 레벨을 랜덤하게 결정합니다.
//
// [확률]
// Level 1: 100%
// Level 2: 50%
// Level 3: 25%
// ...
// 평균 레벨: 약 1.4
func randomLevel() int {
	level := 1
	// rand.Float64() < 0.5 = 동전 던져서 앞면
	for level < MaxLevel && rand.Float64() < 0.5 {
		level++
	}
	return level
}

// Push는 항목을 큐에 추가합니다. (Lock-free)
//
// [알고리즘]
// 1. 랜덤 레벨 생성
// 2. 각 레벨에서 삽입 위치 찾기 (preds, succs 배열)
// 3. 새 노드 생성
// 4. 가장 낮은 레벨부터 CAS로 연결
// 5. 높은 레벨로 올라가며 연결
//
// [코드 플로우 상세]
// 삽입할 값: priority=50
//
// 1. 위치 찾기
//    Level 2: HEAD ─────────→ 100
//                    ↑ pred   ↑ succ
//    Level 1: HEAD ───→ 30 ─→ 100
//                        ↑ pred ↑ succ
//    Level 0: HEAD → 5 → 30 → 100
//                         ↑ pred ↑ succ
//
// 2. 새 노드 연결 (Level 0부터)
//    Level 0: HEAD → 5 → 30 → [50] → 100
//                         ↑ CAS ↑
func (pq *PriorityQueue) Push(value interface{}, priority int64) {
	level := randomLevel()
	newNode := &skipNode{
		priority: priority,
		value:    unsafe.Pointer(&value),
		level:    level,
	}

	for {
		// 삽입 위치 찾기
		preds, succs := pq.findPosition(priority)

		// 가장 낮은 레벨에서 먼저 연결
		newNode.next[0] = unsafe.Pointer(succs[0])

		// CAS로 Level 0 연결
		pred := preds[0]
		if pred == nil {
			continue // 재시도
		}

		if !atomic.CompareAndSwapPointer(&pred.next[0], unsafe.Pointer(succs[0]), unsafe.Pointer(newNode)) {
			continue // 실패, 재시도
		}

		// 높은 레벨 연결
		for i := 1; i < level; i++ {
			for {
				pred := preds[i]
				succ := succs[i]

				if pred == nil {
					break
				}

				newNode.next[i] = unsafe.Pointer(succ)

				if atomic.CompareAndSwapPointer(&pred.next[i], unsafe.Pointer(succ), unsafe.Pointer(newNode)) {
					break
				}

				// 실패하면 위치 다시 찾기
				preds, succs = pq.findPosition(priority)
			}
		}

		atomic.AddInt64(&pq.count, 1)

		// maxLevel 업데이트
		for {
			oldMax := atomic.LoadInt32(&pq.maxLevel)
			if int32(level) <= oldMax {
				break
			}
			if atomic.CompareAndSwapInt32(&pq.maxLevel, oldMax, int32(level)) {
				break
			}
		}

		return
	}
}

// Pop은 가장 높은 우선순위 항목을 꺼냅니다. (Lock-free)
//
// [알고리즘]
// 1. head.next[0]이 가장 높은 우선순위 노드
// 2. 해당 노드를 논리적 삭제 (marked = true)
// 3. 물리적 삭제 (링크 수정)
//
// [왜 논리적 삭제 먼저?]
// 다른 고루틴이 같은 노드를 Pop하려 할 수 있음
// marked 플래그로 "이미 삭제됨"을 표시
func (pq *PriorityQueue) Pop() (*Item, bool) {
	for {
		head := (*skipNode)(atomic.LoadPointer(&pq.head))
		first := (*skipNode)(atomic.LoadPointer(&head.next[0]))

		if first == nil {
			return nil, false // 비어있음
		}

		// 논리적 삭제 시도
		if atomic.CompareAndSwapInt32(&first.marked, 0, 1) {
			// 물리적 삭제 (Level 0)
			next := (*skipNode)(atomic.LoadPointer(&first.next[0]))
			atomic.CompareAndSwapPointer(&head.next[0], unsafe.Pointer(first), unsafe.Pointer(next))

			// 높은 레벨도 삭제 (lazy하게)
			for i := 1; i < first.level; i++ {
				for {
					pred := head
					curr := (*skipNode)(atomic.LoadPointer(&pred.next[i]))

					for curr != nil && curr != first {
						pred = curr
						curr = (*skipNode)(atomic.LoadPointer(&curr.next[i]))
					}

					if curr == first {
						next := (*skipNode)(atomic.LoadPointer(&first.next[i]))
						if atomic.CompareAndSwapPointer(&pred.next[i], unsafe.Pointer(first), unsafe.Pointer(next)) {
							break
						}
					} else {
						break
					}
				}
			}

			atomic.AddInt64(&pq.count, -1)

			// 값 반환
			valPtr := atomic.LoadPointer(&first.value)
			if valPtr != nil {
				return &Item{
					Value:    *(*interface{})(valPtr),
					Priority: first.priority,
				}, true
			}
			continue // 값이 없으면 다음 시도
		}
		// 이미 삭제된 노드, 다시 시도
	}
}

// Peek은 가장 높은 우선순위 항목을 제거하지 않고 반환합니다.
func (pq *PriorityQueue) Peek() (*Item, bool) {
	head := (*skipNode)(atomic.LoadPointer(&pq.head))
	first := (*skipNode)(atomic.LoadPointer(&head.next[0]))

	// 삭제되지 않은 첫 번째 노드 찾기
	for first != nil && atomic.LoadInt32(&first.marked) == 1 {
		first = (*skipNode)(atomic.LoadPointer(&first.next[0]))
	}

	if first == nil {
		return nil, false
	}

	valPtr := atomic.LoadPointer(&first.value)
	if valPtr != nil {
		return &Item{
			Value:    *(*interface{})(valPtr),
			Priority: first.priority,
		}, true
	}

	return nil, false
}

// Len은 큐의 크기를 반환합니다.
func (pq *PriorityQueue) Len() int {
	return int(atomic.LoadInt64(&pq.count))
}

// IsEmpty는 큐가 비어있는지 확인합니다.
func (pq *PriorityQueue) IsEmpty() bool {
	return pq.Len() == 0
}

// findPosition은 삽입/검색 위치를 찾습니다.
//
// [반환값]
// - preds: 각 레벨에서 삽입 위치의 이전 노드
// - succs: 각 레벨에서 삽입 위치의 다음 노드
func (pq *PriorityQueue) findPosition(priority int64) ([MaxLevel]*skipNode, [MaxLevel]*skipNode) {
	var preds, succs [MaxLevel]*skipNode

	head := (*skipNode)(atomic.LoadPointer(&pq.head))

	pred := head
	for level := int(atomic.LoadInt32(&pq.maxLevel)) - 1; level >= 0; level-- {
		curr := (*skipNode)(atomic.LoadPointer(&pred.next[level]))

		// 현재 레벨에서 삽입 위치 찾기
		// priority가 높은 게 앞에 오므로 > 비교
		for curr != nil && curr.priority > priority {
			// 삭제된 노드 스킵
			if atomic.LoadInt32(&curr.marked) == 0 {
				pred = curr
			}
			curr = (*skipNode)(atomic.LoadPointer(&curr.next[level]))
		}

		preds[level] = pred
		succs[level] = curr
	}

	return preds, succs
}

// Clear는 큐를 비웁니다.
func (pq *PriorityQueue) Clear() {
	head := (*skipNode)(atomic.LoadPointer(&pq.head))

	// 모든 노드를 논리적 삭제
	curr := (*skipNode)(atomic.LoadPointer(&head.next[0]))
	for curr != nil {
		atomic.StoreInt32(&curr.marked, 1)
		curr = (*skipNode)(atomic.LoadPointer(&curr.next[0]))
	}

	// 헤드의 next 포인터 초기화
	for i := 0; i < MaxLevel; i++ {
		atomic.StorePointer(&head.next[i], nil)
	}

	atomic.StoreInt64(&pq.count, 0)
}
