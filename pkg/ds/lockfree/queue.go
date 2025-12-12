// Package lockfree는 Lock-free 자료구조를 제공합니다.
// CAS(Compare-And-Swap) 연산을 사용하여 Mutex 없이 동시성을 처리합니다.
package lockfree

import (
	"sync/atomic"
	"unsafe"
)

// =============================================================================
// Lock-free Queue (Michael-Scott Queue)
// =============================================================================

// Queue는 Lock-free MPSC(Multiple Producer, Single Consumer) 큐입니다.
//
// [Michael-Scott Queue란?]
// 1996년 Maged M. Michael과 Michael L. Scott이 발표한 알고리즘입니다.
// CAS 연산만으로 동시성을 처리하는 연결 리스트 기반 큐입니다.
//
// [구조]
// head → [dummy] → [node1] → [node2] → [node3] → nil
//                                         ↑
//                                        tail
//
// [특징]
// 1. 더미 노드: head는 항상 더미 노드를 가리킴 (초기화 단순화)
// 2. tail은 실제 마지막 노드 또는 그 이전 노드를 가리킬 수 있음
// 3. Lock-free: 어떤 스레드도 무한 대기하지 않음
// 4. Wait-free 아님: 특정 스레드가 무한 재시도할 수 있음 (실제로는 드묾)
//
// [왜 Lock-free가 빠른가?]
// 1. 컨텍스트 스위칭 없음: CAS 실패해도 고루틴이 sleep 안 함
// 2. 캐시 효율: 락 데이터 구조체 없어서 캐시 라인 절약
// 3. 공정성: 어떤 고루틴도 굶주리지 않음 (진행 보장)
//
// [단점]
// 1. 구현 복잡
// 2. ABA 문제 주의 필요
// 3. 메모리 재사용 복잡 (이 구현에서는 GC에 의존)
type Queue struct {
	// head는 더미 노드(sentinel)를 가리킵니다.
	//
	// [더미 노드의 역할]
	// - 빈 큐 처리 단순화 (head == tail 검사 쉬움)
	// - Dequeue 시 head.next에서 값을 꺼냄
	// - head 자체는 절대 nil이 아님
	//
	// [unsafe.Pointer 사용 이유]
	// - atomic.LoadPointer/StorePointer 사용하려면 unsafe.Pointer 필요
	// - *node를 직접 사용하면 atomic 연산 불가
	head unsafe.Pointer // *node

	// _pad1은 false sharing을 방지합니다.
	//
	// [False Sharing이란?]
	// CPU 캐시는 "캐시 라인" 단위로 데이터를 읽음 (보통 64바이트)
	// head와 tail이 같은 캐시 라인에 있으면:
	// - CPU1이 head 수정 → 캐시 라인 무효화
	// - CPU2가 tail 읽으려면 다시 메모리에서 로드
	// - 성능 저하!
	//
	// 패딩으로 head와 tail을 다른 캐시 라인에 배치
	_pad1 [56]byte // 64 - 8(pointer) = 56

	// tail은 마지막 노드(또는 그 이전)를 가리킵니다.
	//
	// [tail이 정확하지 않을 수 있는 이유]
	// Enqueue 도중 다른 고루틴이 끼어들 수 있음:
	// 1. A가 새 노드 연결 (tail.next = newNode)
	// 2. B가 또 새 노드 연결
	// 3. A가 tail 업데이트 (tail = newNode)
	// → 이 순간 tail은 실제 끝이 아님
	//
	// 그래서 tail.next가 nil인지 확인하고 도움(helping) 필요
	tail unsafe.Pointer // *node

	_pad2 [56]byte
}

// node는 큐의 노드입니다.
//
// [구조]
// ┌───────────────────┐
// │  value: 저장된 값  │
// │  next: 다음 노드   │  → [다음 노드] → ...
// └───────────────────┘
type node struct {
	value interface{}
	next  unsafe.Pointer // *node
}

// NewQueue는 새로운 Lock-free 큐를 생성합니다.
//
// [초기 상태]
// head → [dummy(nil)] ← tail
//           ↓
//          nil
//
// head와 tail이 같은 더미 노드를 가리킴
// 더미 노드의 value는 nil, next도 nil
func NewQueue() *Queue {
	// 더미 노드 생성
	dummy := &node{}

	q := &Queue{}

	// head와 tail 모두 더미 노드를 가리키도록 초기화
	// unsafe.Pointer로 변환해서 저장
	atomic.StorePointer(&q.head, unsafe.Pointer(dummy))
	atomic.StorePointer(&q.tail, unsafe.Pointer(dummy))

	return q
}

// Enqueue는 큐에 값을 추가합니다. (Lock-free)
//
// [알고리즘 단계]
// 1. 새 노드 생성
// 2. 현재 tail 읽기
// 3. tail.next 읽기
// 4. tail이 아직 유효한지 확인
// 5. tail.next가 nil이면 CAS로 새 노드 연결
// 6. tail을 새 노드로 CAS 업데이트
//
// [코드 플로우 상세]
//
// 초기 상태:
// head → [dummy] ← tail
//           ↓
//          nil
//
// Enqueue("A") 후:
// head → [dummy] → [A] ← tail
//                    ↓
//                   nil
//
// Enqueue("B") 후:
// head → [dummy] → [A] → [B] ← tail
//                          ↓
//                         nil
func (q *Queue) Enqueue(value interface{}) {
	// 1. 새 노드 생성
	//
	// [메모리 할당]
	// - Go 힙에 할당됨
	// - GC가 관리하므로 수동 해제 불필요
	// - 고빈도 할당 시 sync.Pool로 최적화 가능
	newNode := &node{value: value}

	// 무한 루프 (CAS 성공할 때까지)
	//
	// [왜 무한 루프?]
	// - CAS 실패 = 다른 고루틴이 먼저 수정함
	// - 실패하면 다시 시도
	// - 실제로는 보통 1-2회 내에 성공
	for {
		// 2. 현재 tail 읽기 (atomic)
		//
		// [atomic.LoadPointer]
		// - CPU 수준에서 원자적 읽기
		// - 다른 CPU의 캐시와 동기화됨
		// - x86: MOV 명령어 (이미 원자적)
		// - ARM: LDAR 명령어
		tail := (*node)(atomic.LoadPointer(&q.tail))

		// 3. tail의 다음 노드 읽기 (atomic)
		next := (*node)(atomic.LoadPointer(&tail.next))

		// 4. tail이 여전히 유효한지 확인
		//
		// [왜 다시 확인?]
		// 2번과 3번 사이에 다른 고루틴이 tail을 바꿨을 수 있음
		// 일관성 없는 상태에서 작업하면 안 됨
		if tail == (*node)(atomic.LoadPointer(&q.tail)) {

			if next == nil {
				// 5. tail.next가 nil이면 여기에 새 노드 연결 시도
				//
				// [CAS (Compare-And-Swap)]
				// "현재 값이 expected면 new로 바꾸고 true 반환"
				// "아니면 아무것도 안 하고 false 반환"
				//
				// [CPU 명령어]
				// - x86: CMPXCHG (Compare and Exchange)
				// - ARM: LDREX/STREX (Load/Store Exclusive)
				//
				// [파라미터]
				// - &tail.next: 수정할 메모리 주소
				// - unsafe.Pointer(next): 예상 값 (nil)
				// - unsafe.Pointer(newNode): 새 값
				if atomic.CompareAndSwapPointer(
					&tail.next,
					unsafe.Pointer(next),     // expected: nil
					unsafe.Pointer(newNode),  // new: newNode
				) {
					// 6. 성공! tail도 새 노드로 업데이트 시도
					//
					// [이 CAS가 실패해도 괜찮은 이유]
					// - 다른 고루틴이 대신 업데이트해줄 것임 (helping)
					// - 아래 else 브랜치에서 처리
					atomic.CompareAndSwapPointer(
						&q.tail,
						unsafe.Pointer(tail),
						unsafe.Pointer(newNode),
					)
					return // 완료!
				}
				// CAS 실패: 다른 고루틴이 먼저 추가함 → 재시도
			} else {
				// tail.next가 nil이 아님 = tail이 뒤처져 있음
				//
				// [Helping (협력)]
				// 다른 고루틴이 5번까지만 하고 6번 전에 멈췄을 수 있음
				// 우리가 대신 tail을 앞으로 이동시켜줌
				//
				// [왜 이게 필요?]
				// Lock-free의 핵심: 진행 보장 (progress guarantee)
				// 한 고루틴이 멈춰도 다른 고루틴이 도와서 진행
				atomic.CompareAndSwapPointer(
					&q.tail,
					unsafe.Pointer(tail),
					unsafe.Pointer(next),
				)
			}
		}
		// tail이 바뀌었으면 처음부터 재시도
	}
}

// Dequeue는 큐에서 값을 꺼냅니다. (Lock-free)
//
// [알고리즘 단계]
// 1. 현재 head, tail, head.next 읽기
// 2. head가 유효한지 확인
// 3. head == tail이면 (빈 큐 또는 tail이 뒤처짐)
//    - head.next가 nil이면 빈 큐
//    - 아니면 tail 업데이트 (helping)
// 4. head != tail이면 값 꺼내고 head를 다음으로 CAS
//
// [반환값]
// - interface{}: 꺼낸 값 (비어있으면 nil)
// - bool: 성공 여부
func (q *Queue) Dequeue() (interface{}, bool) {
	for {
		// 1. 현재 상태 읽기 (모두 atomic)
		head := (*node)(atomic.LoadPointer(&q.head))
		tail := (*node)(atomic.LoadPointer(&q.tail))
		next := (*node)(atomic.LoadPointer(&head.next))

		// 2. head가 유효한지 확인
		if head == (*node)(atomic.LoadPointer(&q.head)) {

			// 3. head == tail (빈 큐이거나 tail이 뒤처짐)
			if head == tail {
				if next == nil {
					// 진짜 빈 큐
					//
					// [상태]
					// head → [dummy] ← tail
					//           ↓
					//          nil
					return nil, false
				}

				// tail이 뒤처져 있음 → 도와주기
				//
				// [상태]
				// head → [dummy] → [A] → ...
				//           ↑        ↑
				//          tail     next
				//
				// tail을 next로 이동시켜줌
				atomic.CompareAndSwapPointer(
					&q.tail,
					unsafe.Pointer(tail),
					unsafe.Pointer(next),
				)
			} else {
				// 4. head != tail → 값이 있음
				//
				// [상태]
				// head → [dummy] → [A] → [B] → ...
				//           ↑                    ↑
				//         head                 tail
				//
				// dummy의 next인 [A]에서 값을 꺼내고
				// head를 [A]로 이동 ([A]가 새로운 dummy가 됨)

				// next에서 값 먼저 읽기
				// [왜 CAS 전에 읽나?]
				// CAS 성공 후에 읽으면 다른 고루틴이 next를 수정했을 수 있음
				value := next.value

				// head를 다음 노드로 CAS
				if atomic.CompareAndSwapPointer(
					&q.head,
					unsafe.Pointer(head),
					unsafe.Pointer(next),
				) {
					// 성공!
					// [메모리 관리]
					// - 이전 head(dummy)는 이제 참조 없음
					// - GC가 수거할 것임
					// - C/C++이면 여기서 free() 해야 함 (ABA 문제 주의!)
					return value, true
				}
				// CAS 실패: 다른 고루틴이 먼저 Dequeue함 → 재시도
			}
		}
		// head가 바뀌었으면 처음부터 재시도
	}
}

// Peek은 첫 번째 항목을 제거하지 않고 반환합니다.
//
// [주의]
// Lock-free 환경에서 Peek 후 바로 Dequeue하면
// 다른 고루틴이 먼저 Dequeue했을 수 있음
// Peek은 "힌트" 정도로만 사용
func (q *Queue) Peek() (interface{}, bool) {
	head := (*node)(atomic.LoadPointer(&q.head))
	next := (*node)(atomic.LoadPointer(&head.next))

	if next == nil {
		return nil, false
	}

	return next.value, true
}

// IsEmpty는 큐가 비어있는지 확인합니다.
//
// [주의]
// 이 함수가 true를 반환해도 바로 다음에 누가 Enqueue할 수 있음
// false를 반환해도 바로 다음에 누가 Dequeue할 수 있음
func (q *Queue) IsEmpty() bool {
	head := (*node)(atomic.LoadPointer(&q.head))
	next := (*node)(atomic.LoadPointer(&head.next))
	return next == nil
}

// =============================================================================
// ABA 문제 설명
// =============================================================================

// [ABA 문제란?]
//
// CAS는 "값이 같으면 바꾼다"인데, 문제는:
// 1. A가 값 X를 읽음
// 2. B가 값을 X → Y → X로 바꿈
// 3. A가 CAS(X, Z) 시도 → 성공! (값이 X니까)
// 4. 하지만 중간에 Y였던 적이 있음... 의도한 게 아닐 수 있음
//
// [큐에서의 ABA]
// 1. A가 head를 읽음 (node1)
// 2. B가 node1을 Dequeue
// 3. B가 새 노드 Enqueue (메모리 재사용으로 같은 주소!)
// 4. A가 CAS(&head, node1, next) → 성공하지만 잘못된 next!
//
// [해결 방법]
// 1. 버전 번호 추가 (포인터 + 카운터)
// 2. Hazard Pointers
// 3. Epoch-based Reclamation
// 4. GC 사용 (Go는 이 방식!) ← 우리 구현
//
// Go의 GC 덕분에:
// - 참조가 있는 한 메모리 재사용 안 됨
// - ABA 문제 자동 해결!
// - 대신 GC 오버헤드가 있음
