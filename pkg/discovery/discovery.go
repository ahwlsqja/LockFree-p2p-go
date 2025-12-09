// Package discovery는 P2P 네트워크에서 피어를 찾는 메커니즘을 제공합니다.
//
// [피어 디스커버리란?]
// P2P 네트워크에 새로 참여할 때, 다른 노드들의 주소를 알아내는 과정입니다.
// 중앙 서버 없이 분산된 네트워크를 유지하려면 이 과정이 필수적입니다.
//
// [디스커버리 방식들]
// 1. 시드 노드 (Seed Node)
//    - 잘 알려진 노드 목록을 하드코딩
//    - 부트스트랩용으로 가장 간단
//    - 단점: 시드 노드가 죽으면 새 노드 참여 어려움
//
// 2. 가십 프로토콜 (Gossip Protocol)
//    - 피어들끼리 서로 알고 있는 피어 목록 교환
//    - "야, 나 이런 애들 알아" → "오 나도 이런 애들 알아"
//    - 바이러스처럼 정보가 퍼져나감
//
// 3. DHT (Distributed Hash Table)
//    - Kademlia 같은 알고리즘 사용
//    - 노드 ID 기반으로 구조화된 탐색
//    - BitTorrent, Ethereum 등에서 사용
//
// 4. mDNS (Multicast DNS)
//    - 로컬 네트워크에서 피어 찾기
//    - 개발/테스트 환경에 유용
package discovery

import (
	"context"
	"net"
	"time"
)

// =============================================================================
// 피어 정보
// =============================================================================

// PeerInfo는 발견된 피어의 정보를 담습니다.
//
// [왜 별도 구조체가 필요한가?]
// - peer.Peer는 이미 연결된 피어를 나타냄
// - PeerInfo는 "알고는 있지만 아직 연결 안 한" 피어 정보
// - 연결 시도 전에 필터링, 우선순위 정하기 등에 사용
type PeerInfo struct {
	// ID는 피어의 고유 식별자입니다.
	// 아직 연결 전이라 모를 수도 있음 (빈 문자열)
	ID string

	// Addr은 피어의 네트워크 주소입니다.
	// 예: "192.168.1.100:3000", "node1.example.com:3000"
	Addr string

	// Source는 이 피어를 어떻게 알게 되었는지 나타냅니다.
	// 예: "seed", "gossip", "dht", "mdns"
	Source string

	// LastSeen은 이 피어 정보가 마지막으로 확인된 시간입니다.
	// 오래된 정보는 신뢰도가 낮음
	LastSeen time.Time

	// Attempts는 연결 시도 횟수입니다.
	// 너무 많이 실패하면 블랙리스트에 올릴 수 있음
	Attempts int

	// LastAttempt는 마지막 연결 시도 시간입니다.
	// 재시도 간격 조절에 사용
	LastAttempt time.Time
}

// TCPAddr는 PeerInfo의 주소를 net.TCPAddr로 변환합니다.
//
// [왜 필요한가?]
// net.Dial()은 문자열 주소를 받지만,
// 일부 저수준 API는 net.TCPAddr 구조체를 요구함
func (p *PeerInfo) TCPAddr() (*net.TCPAddr, error) {
	return net.ResolveTCPAddr("tcp", p.Addr)
}

// IsStale은 피어 정보가 오래되었는지 확인합니다.
//
// [파라미터]
// - maxAge: 이 시간보다 오래되면 stale로 판단
//
// [사용 예]
// if info.IsStale(1 * time.Hour) {
//     // 1시간 넘게 확인 안 된 피어, 신뢰도 낮음
// }
func (p *PeerInfo) IsStale(maxAge time.Duration) bool {
	return time.Since(p.LastSeen) > maxAge
}

// ShouldRetry는 연결 재시도를 해야 하는지 확인합니다.
//
// [백오프 전략]
// 실패할수록 재시도 간격을 늘림 (지수 백오프)
// - 1회 실패: 1분 후 재시도
// - 2회 실패: 2분 후 재시도
// - 3회 실패: 4분 후 재시도
// - ...
// - 최대 1시간까지
//
// [왜 이렇게 하나?]
// - 일시적 장애면 금방 복구됨
// - 영구적 장애면 계속 시도해봤자 낭비
// - 네트워크 부하도 줄임
func (p *PeerInfo) ShouldRetry(baseInterval time.Duration, maxAttempts int) bool {
	// 최대 시도 횟수 초과
	if p.Attempts >= maxAttempts {
		return false
	}

	// 아직 시도한 적 없음
	if p.LastAttempt.IsZero() {
		return true
	}

	// 지수 백오프 계산
	// 2^attempts * baseInterval, 최대 1시간
	backoff := baseInterval * time.Duration(1<<uint(p.Attempts))
	maxBackoff := 1 * time.Hour
	if backoff > maxBackoff {
		backoff = maxBackoff
	}

	return time.Since(p.LastAttempt) >= backoff
}

// =============================================================================
// 디스커버리 인터페이스
// =============================================================================

// Discovery는 피어 디스커버리 메커니즘의 인터페이스입니다.
//
// [인터페이스를 쓰는 이유]
// - 여러 디스커버리 방식을 같은 방식으로 사용 가능
// - 테스트 시 Mock 구현 가능
// - 런타임에 디스커버리 방식 교체 가능
//
// [구현체들]
// - SeedDiscovery: 시드 노드 기반 (가장 간단)
// - GossipDiscovery: 가십 프로토콜 기반
// - DHTDiscovery: DHT 기반 (TODO)
type Discovery interface {
	// Start는 디스커버리를 시작합니다.
	//
	// [동작]
	// - 백그라운드 고루틴 시작
	// - 주기적으로 피어 탐색
	// - 찾은 피어는 내부 저장소에 추가
	Start() error

	// Stop은 디스커버리를 중지합니다.
	//
	// [동작]
	// - 백그라운드 고루틴 종료
	// - 진행 중인 탐색 취소
	// - 리소스 정리
	Stop() error

	// FindPeers는 새로운 피어를 탐색합니다.
	//
	// [파라미터]
	// - ctx: 취소/타임아웃 제어용
	// - limit: 최대 반환할 피어 수 (0이면 제한 없음)
	//
	// [반환]
	// - 발견된 피어 정보 슬라이스
	// - 에러 (네트워크 문제 등)
	//
	// [주의]
	// 이미 알고 있는 피어는 제외하고 새로운 것만 반환할 수도 있고,
	// 구현에 따라 모든 알려진 피어를 반환할 수도 있음
	FindPeers(ctx context.Context, limit int) ([]*PeerInfo, error)

	// Advertise는 자신을 네트워크에 알립니다.
	//
	// [동작]
	// - 다른 노드들이 나를 찾을 수 있게 함
	// - 가십이면 내 정보를 퍼뜨림
	// - DHT면 라우팅 테이블에 등록
	//
	// [파라미터]
	// - ctx: 취소/타임아웃 제어용
	Advertise(ctx context.Context) error

	// AddPeer는 수동으로 피어를 추가합니다.
	//
	// [사용 케이스]
	// - 연결된 피어가 알려준 다른 피어 정보 추가
	// - 외부에서 피어 정보를 받았을 때
	AddPeer(info *PeerInfo)

	// RemovePeer는 피어를 목록에서 제거합니다.
	//
	// [사용 케이스]
	// - 연결 실패가 너무 많은 피어
	// - 악의적인 피어로 판단됨
	// - 네트워크에서 영구 이탈한 피어
	RemovePeer(addr string)

	// GetPeers는 알고 있는 모든 피어 목록을 반환합니다.
	GetPeers() []*PeerInfo

	// PeerCount는 알고 있는 피어 수를 반환합니다.
	PeerCount() int
}

// =============================================================================
// 이벤트 시스템
// =============================================================================

// EventType은 디스커버리 이벤트 종류입니다.
type EventType int

const (
	// EventPeerDiscovered는 새 피어가 발견되었을 때
	EventPeerDiscovered EventType = iota

	// EventPeerRemoved는 피어가 목록에서 제거되었을 때
	EventPeerRemoved

	// EventPeerUpdated는 피어 정보가 업데이트되었을 때
	EventPeerUpdated
)

// Event는 디스커버리 이벤트입니다.
//
// [이벤트 기반 설계의 장점]
// - 디스커버리와 연결 로직 분리
// - 비동기 처리 가능
// - 여러 리스너가 같은 이벤트 처리 가능
type Event struct {
	Type EventType
	Peer *PeerInfo
}

// EventHandler는 디스커버리 이벤트를 처리하는 함수입니다.
type EventHandler func(Event)

// =============================================================================
// 설정
// =============================================================================

// Config는 디스커버리 설정입니다.
type Config struct {
	// DiscoveryInterval은 피어 탐색 주기입니다.
	// 기본값: 30초
	//
	// [트레이드오프]
	// - 짧으면: 빠르게 피어 발견, but 네트워크 부하 증가
	// - 길면: 느리게 피어 발견, but 네트워크 부하 감소
	DiscoveryInterval time.Duration

	// MaxPeers는 저장할 최대 피어 수입니다.
	// 기본값: 1000
	//
	// [왜 제한하나?]
	// - 메모리 사용량 제한
	// - 오래된/죽은 피어 정보 정리
	MaxPeers int

	// PeerTTL은 피어 정보의 유효 기간입니다.
	// 기본값: 24시간
	//
	// [동작]
	// LastSeen이 이 시간보다 오래되면 자동 제거
	PeerTTL time.Duration

	// RetryBaseInterval은 재시도 기본 간격입니다.
	// 기본값: 1분
	RetryBaseInterval time.Duration

	// MaxRetryAttempts는 최대 재시도 횟수입니다.
	// 기본값: 10
	MaxRetryAttempts int
}

// DefaultConfig는 기본 디스커버리 설정입니다.
var DefaultConfig = Config{
	DiscoveryInterval: 30 * time.Second,
	MaxPeers:          1000,
	PeerTTL:           24 * time.Hour,
	RetryBaseInterval: 1 * time.Minute,
	MaxRetryAttempts:  10,
}
