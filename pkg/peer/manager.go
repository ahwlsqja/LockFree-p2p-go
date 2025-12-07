package peer

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// =============================================================================
// PeerManager - 피어 연결 관리자
// =============================================================================

// ManagerConfig는 PeerManager 설정입니다.
type ManagerConfig struct {
	// MaxPeers는 최대 피어 수입니다.
	//
	// [왜 제한이 필요한가?]
	// - 각 피어 연결은 리소스 소비 (fd, 메모리, CPU)
	// - 무제한 연결 허용 시 리소스 고갈 가능
	// - 일반적인 블록체인 노드: 25~50개
	MaxPeers int

	// MaxInboundPeers는 최대 인바운드 피어 수입니다.
	//
	// [왜 별도 제한인가?]
	// - 인바운드는 통제 불가 (누구나 연결 가능)
	// - 악의적 노드가 대량 연결로 리소스 고갈 시도 가능
	// - 아웃바운드는 우리가 선택한 신뢰할 수 있는 피어
	MaxInboundPeers int

	// MaxOutboundPeers는 최대 아웃바운드 피어 수입니다.
	MaxOutboundPeers int
}

// DefaultManagerConfig는 기본 설정입니다.
var DefaultManagerConfig = ManagerConfig{
	MaxPeers:         50,
	MaxInboundPeers:  25,
	MaxOutboundPeers: 25,
}

// PeerManager는 피어 연결들을 관리합니다.
//
// [현재 구현: sync.Map 기반]
// 이 버전은 Mutex 기반 대비 성능이 좋지만,
// 진정한 Lock-free는 아님 (sync.Map 내부적으로 락 사용)
// Phase 3에서 Lock-free hashmap으로 교체 예정
//
// [sync.Map vs map+Mutex]
// sync.Map이 유리한 경우:
// - 읽기가 쓰기보다 훨씬 많을 때
// - 키 집합이 안정적일 때 (자주 추가/삭제 안 함)
// - 여러 고루틴이 서로 다른 키에 접근할 때
//
// map+Mutex가 유리한 경우:
// - 쓰기가 빈번할 때
// - Range 순회가 잦을 때
// - 모든 키에 대한 일괄 처리가 필요할 때
//
// P2P 피어 관리는 sync.Map에 적합:
// - 피어 조회가 대부분 (메시지 라우팅)
// - 피어 추가/삭제는 상대적으로 드묾
type PeerManager struct {
	// peers는 ID로 피어를 찾는 맵입니다.
	// key: ID.String(), value: *Peer
	//
	// [sync.Map 내부 구조]
	// - read map: 원자적 읽기용 (대부분의 읽기는 여기서)
	// - dirty map: 쓰기용 (락 필요)
	// - 일정 횟수 이상 dirty 접근 시 read로 승격
	peers sync.Map

	// peersByAddr은 주소로 피어를 찾는 맵입니다.
	// key: addr (string), value: *Peer
	// 중복 연결 방지에 사용
	peersByAddr sync.Map

	// 피어 수 카운터 (atomic)
	//
	// [왜 sync.Map의 길이를 안 쓰나?]
	// sync.Map은 O(1) 길이 조회를 지원하지 않음
	// Range로 순회해야 하므로 O(n)
	// 자주 체크하는 값이므로 별도 카운터 유지
	totalCount    int64 // atomic
	inboundCount  int64 // atomic
	outboundCount int64 // atomic

	// config는 매니저 설정입니다.
	config ManagerConfig

	// eventHandler는 피어 이벤트 핸들러입니다.
	// 피어 추가/제거 시 호출됨
	eventHandler PeerEventHandler

	// mu는 설정 변경 등 전체 동기화가 필요한 경우 사용합니다.
	mu sync.RWMutex
}

// PeerEventHandler는 피어 이벤트 핸들러 인터페이스입니다.
type PeerEventHandler interface {
	// OnPeerConnected는 새 피어가 연결되었을 때 호출됩니다.
	OnPeerConnected(peer *Peer)

	// OnPeerDisconnected는 피어 연결이 끊겼을 때 호출됩니다.
	OnPeerDisconnected(peer *Peer)
}

// NewPeerManager는 새로운 PeerManager를 생성합니다.
func NewPeerManager(config ManagerConfig) *PeerManager {
	return &PeerManager{
		config: config,
	}
}

// NewPeerManagerWithDefaults는 기본 설정으로 PeerManager를 생성합니다.
func NewPeerManagerWithDefaults() *PeerManager {
	return NewPeerManager(DefaultManagerConfig)
}

// =============================================================================
// 피어 추가/제거
// =============================================================================

// AddPeer는 새 피어를 추가합니다.
//
// [반환값]
// - error: 추가 실패 시 (피어 수 초과, 중복 연결 등)
//
// [검사 사항]
// 1. 총 피어 수 초과 여부
// 2. Inbound/Outbound 별 제한 초과 여부
// 3. 이미 같은 ID의 피어가 있는지
// 4. 이미 같은 주소의 피어가 있는지 (중복 연결 방지)
func (m *PeerManager) AddPeer(peer *Peer) error {
	// 1. 피어 수 제한 확인
	//
	// [왜 atomic.LoadInt64를 쓰나?]
	// - 여러 고루틴에서 동시에 AddPeer 호출 가능
	// - 락 없이 현재 값을 안전하게 읽음
	// - 정확한 체크는 아래 CAS로 수행
	totalCount := atomic.LoadInt64(&m.totalCount)
	if totalCount >= int64(m.config.MaxPeers) {
		return fmt.Errorf("최대 피어 수 초과: %d >= %d", totalCount, m.config.MaxPeers)
	}

	// 2. 방향별 제한 확인
	if peer.Direction() == Inbound {
		inboundCount := atomic.LoadInt64(&m.inboundCount)
		if inboundCount >= int64(m.config.MaxInboundPeers) {
			return fmt.Errorf("최대 인바운드 피어 수 초과: %d >= %d",
				inboundCount, m.config.MaxInboundPeers)
		}
	} else {
		outboundCount := atomic.LoadInt64(&m.outboundCount)
		if outboundCount >= int64(m.config.MaxOutboundPeers) {
			return fmt.Errorf("최대 아웃바운드 피어 수 초과: %d >= %d",
				outboundCount, m.config.MaxOutboundPeers)
		}
	}

	// 3. ID 중복 확인 및 추가 (atomic하게)
	idKey := peer.ID().String()
	if _, loaded := m.peers.LoadOrStore(idKey, peer); loaded {
		return fmt.Errorf("이미 연결된 피어: %s", peer.ID().ShortString())
	}

	// 4. 주소 중복 확인 (주소가 있는 경우만)
	if peer.Addr() != "" {
		if _, loaded := m.peersByAddr.LoadOrStore(peer.Addr(), peer); loaded {
			// 롤백: ID 맵에서 제거
			m.peers.Delete(idKey)
			return fmt.Errorf("이미 연결된 주소: %s", peer.Addr())
		}
	}

	// 5. 카운터 증가
	//
	// [atomic.AddInt64]
	// - 락 없이 원자적으로 증가
	// - 반환값은 증가 후의 값
	atomic.AddInt64(&m.totalCount, 1)
	if peer.Direction() == Inbound {
		atomic.AddInt64(&m.inboundCount, 1)
	} else {
		atomic.AddInt64(&m.outboundCount, 1)
	}

	// 6. 이벤트 핸들러 호출
	if m.eventHandler != nil {
		m.eventHandler.OnPeerConnected(peer)
	}

	return nil
}

// RemovePeer는 피어를 제거합니다.
//
// [호출 시점]
// - 피어가 연결을 끊었을 때
// - 피어가 비정상 동작할 때 (악의적 행동, 타임아웃 등)
// - 노드 종료 시
func (m *PeerManager) RemovePeer(id ID) *Peer {
	idKey := id.String()

	// 1. ID 맵에서 제거
	value, loaded := m.peers.LoadAndDelete(idKey)
	if !loaded {
		return nil // 없는 피어
	}

	peer := value.(*Peer)

	// 2. 주소 맵에서도 제거
	if peer.Addr() != "" {
		m.peersByAddr.Delete(peer.Addr())
	}

	// 3. 카운터 감소
	atomic.AddInt64(&m.totalCount, -1)
	if peer.Direction() == Inbound {
		atomic.AddInt64(&m.inboundCount, -1)
	} else {
		atomic.AddInt64(&m.outboundCount, -1)
	}

	// 4. 이벤트 핸들러 호출
	if m.eventHandler != nil {
		m.eventHandler.OnPeerDisconnected(peer)
	}

	return peer
}

// =============================================================================
// 피어 조회
// =============================================================================

// GetPeer는 ID로 피어를 찾습니다.
//
// [시간 복잡도] O(1) - 해시맵 조회
//
// [sync.Map.Load 내부 동작]
// 1. read map에서 먼저 찾음 (락 없음)
// 2. 없으면 dirty map에서 찾음 (락 필요)
// 3. dirty 접근이 많아지면 dirty를 read로 승격
func (m *PeerManager) GetPeer(id ID) *Peer {
	value, ok := m.peers.Load(id.String())
	if !ok {
		return nil
	}
	return value.(*Peer)
}

// GetPeerByAddr은 주소로 피어를 찾습니다.
func (m *PeerManager) GetPeerByAddr(addr string) *Peer {
	value, ok := m.peersByAddr.Load(addr)
	if !ok {
		return nil
	}
	return value.(*Peer)
}

// HasPeer는 해당 ID의 피어가 있는지 확인합니다.
func (m *PeerManager) HasPeer(id ID) bool {
	_, ok := m.peers.Load(id.String())
	return ok
}

// HasPeerByAddr은 해당 주소의 피어가 있는지 확인합니다.
func (m *PeerManager) HasPeerByAddr(addr string) bool {
	_, ok := m.peersByAddr.Load(addr)
	return ok
}

// =============================================================================
// 피어 순회
// =============================================================================

// GetAllPeers는 모든 피어 목록을 반환합니다.
//
// [주의] 이 메서드는 O(n) 시간이 걸립니다.
// 피어 수가 많으면 성능에 영향을 줄 수 있음
//
// [sync.Map.Range 특성]
// - 일관성 보장 안 함: 순회 중 추가/삭제된 피어가 포함되거나 빠질 수 있음
// - 각 키는 최대 한 번만 방문
// - 콜백이 false 반환하면 순회 중단
func (m *PeerManager) GetAllPeers() []*Peer {
	// 예상 크기로 슬라이스 미리 할당 (재할당 방지)
	peers := make([]*Peer, 0, atomic.LoadInt64(&m.totalCount))

	m.peers.Range(func(key, value interface{}) bool {
		peers = append(peers, value.(*Peer))
		return true // 계속 순회
	})

	return peers
}

// GetConnectedPeers는 연결된 피어만 반환합니다.
func (m *PeerManager) GetConnectedPeers() []*Peer {
	peers := make([]*Peer, 0, atomic.LoadInt64(&m.totalCount))

	m.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if peer.IsConnected() {
			peers = append(peers, peer)
		}
		return true
	})

	return peers
}

// GetInboundPeers는 인바운드 피어만 반환합니다.
func (m *PeerManager) GetInboundPeers() []*Peer {
	peers := make([]*Peer, 0, atomic.LoadInt64(&m.inboundCount))

	m.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if peer.Direction() == Inbound {
			peers = append(peers, peer)
		}
		return true
	})

	return peers
}

// GetOutboundPeers는 아웃바운드 피어만 반환합니다.
func (m *PeerManager) GetOutboundPeers() []*Peer {
	peers := make([]*Peer, 0, atomic.LoadInt64(&m.outboundCount))

	m.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if peer.Direction() == Outbound {
			peers = append(peers, peer)
		}
		return true
	})

	return peers
}

// ForEachPeer는 각 피어에 대해 함수를 실행합니다.
//
// [용도]
// - 모든 피어에게 브로드캐스트
// - 특정 조건의 피어 찾기
// - 피어 통계 집계
//
// [콜백 반환값]
// - true: 계속 순회
// - false: 순회 중단
func (m *PeerManager) ForEachPeer(fn func(peer *Peer) bool) {
	m.peers.Range(func(key, value interface{}) bool {
		return fn(value.(*Peer))
	})
}

// =============================================================================
// 통계
// =============================================================================

// Count는 총 피어 수를 반환합니다.
func (m *PeerManager) Count() int {
	return int(atomic.LoadInt64(&m.totalCount))
}

// InboundCount는 인바운드 피어 수를 반환합니다.
func (m *PeerManager) InboundCount() int {
	return int(atomic.LoadInt64(&m.inboundCount))
}

// OutboundCount는 아웃바운드 피어 수를 반환합니다.
func (m *PeerManager) OutboundCount() int {
	return int(atomic.LoadInt64(&m.outboundCount))
}

// IsFull은 피어 수가 최대에 도달했는지 확인합니다.
func (m *PeerManager) IsFull() bool {
	return atomic.LoadInt64(&m.totalCount) >= int64(m.config.MaxPeers)
}

// CanAcceptInbound는 인바운드 연결을 받을 수 있는지 확인합니다.
func (m *PeerManager) CanAcceptInbound() bool {
	return atomic.LoadInt64(&m.inboundCount) < int64(m.config.MaxInboundPeers)
}

// CanDialOutbound는 아웃바운드 연결을 할 수 있는지 확인합니다.
func (m *PeerManager) CanDialOutbound() bool {
	return atomic.LoadInt64(&m.outboundCount) < int64(m.config.MaxOutboundPeers)
}

// =============================================================================
// 설정
// =============================================================================

// SetEventHandler는 피어 이벤트 핸들러를 설정합니다.
func (m *PeerManager) SetEventHandler(handler PeerEventHandler) {
	m.mu.Lock()
	m.eventHandler = handler
	m.mu.Unlock()
}

// Config는 현재 설정을 반환합니다.
func (m *PeerManager) Config() ManagerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// =============================================================================
// 정리
// =============================================================================

// Close는 모든 피어 연결을 닫고 정리합니다.
func (m *PeerManager) Close() error {
	var lastErr error

	// 모든 피어 연결 닫기
	m.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if err := peer.Close(); err != nil {
			lastErr = err
		}
		return true
	})

	return lastErr
}
