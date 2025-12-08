// Package node는 P2P 네트워크 노드의 핵심 로직을 구현합니다.
// 노드는 서버, 피어 관리, 메시지 처리를 통합하는 최상위 컴포넌트입니다.
package node

import (
	"time"

	"github.com/go-p2p-network/go-p2p/pkg/peer"
)

// =============================================================================
// Config - 노드 설정
// =============================================================================

// Config는 노드의 설정입니다.
type Config struct {
	// ListenAddr은 리스닝 주소입니다.
	//
	// [형식]
	// - ":3000": 모든 인터페이스의 3000번 포트
	// - "127.0.0.1:3000": localhost만
	// - "0.0.0.0:3000": 모든 IPv4 인터페이스
	//
	// [보안 고려사항]
	// - 공개 노드: "0.0.0.0:port" 또는 ":port"
	// - 로컬 테스트: "127.0.0.1:port"
	ListenAddr string

	// Seeds는 시드 노드 주소 목록입니다.
	//
	// [시드 노드란?]
	// - 네트워크에 처음 연결할 때 사용하는 알려진 노드
	// - 부트스트랩(bootstrap) 역할
	// - 시드로부터 다른 피어 목록을 받아 네트워크 참여
	//
	// [형식]
	// - "seed1.example.com:3000"
	// - "192.168.1.100:3000"
	Seeds []string

	// MaxPeers는 최대 피어 연결 수입니다.
	MaxPeers int

	// MaxInboundPeers는 최대 인바운드 피어 수입니다.
	MaxInboundPeers int

	// MaxOutboundPeers는 최대 아웃바운드 피어 수입니다.
	MaxOutboundPeers int

	// UserAgent는 클라이언트 식별자입니다.
	// 예: "go-p2p/1.0.0"
	UserAgent string

	// DialTimeout은 아웃바운드 연결 타임아웃입니다.
	DialTimeout time.Duration

	// HandshakeTimeout은 핸드셰이크 타임아웃입니다.
	//
	// [왜 별도 타임아웃?]
	// - 연결은 됐지만 핸드셰이크 응답이 없는 경우
	// - 악의적 노드가 연결만 열고 핸드셰이크 안 함 → 리소스 점유
	HandshakeTimeout time.Duration

	// PingInterval은 Ping 전송 간격입니다.
	//
	// [연결 유지 메커니즘]
	// - 주기적으로 Ping 전송
	// - 응답 없으면 연결 끊김으로 판단
	// - NAT 테이블 유지에도 도움
	PingInterval time.Duration

	// PingTimeout은 Pong 응답 대기 타임아웃입니다.
	PingTimeout time.Duration

	// DiscoveryInterval은 피어 디스커버리 간격입니다.
	//
	// [디스커버리 프로세스]
	// - 주기적으로 GetPeers 메시지 전송
	// - 새 피어 목록 수신 시 연결 시도
	// - 피어 수가 충분하면 간격 늘림
	DiscoveryInterval time.Duration
}

// DefaultConfig는 기본 노드 설정입니다.
//
// [설정값 근거]
// - MaxPeers: 50 - 일반적인 블록체인 노드 수준
// - DialTimeout: 10초 - 네트워크 상태에 따라 조절
// - PingInterval: 30초 - 너무 짧으면 트래픽 낭비, 너무 길면 죽은 연결 감지 늦음
var DefaultConfig = Config{
	ListenAddr:        ":3000",
	Seeds:             nil,
	MaxPeers:          50,
	MaxInboundPeers:   25,
	MaxOutboundPeers:  25,
	UserAgent:         "go-p2p/1.0.0",
	DialTimeout:       10 * time.Second,
	HandshakeTimeout:  5 * time.Second,
	PingInterval:      30 * time.Second,
	PingTimeout:       10 * time.Second,
	DiscoveryInterval: 30 * time.Second,
}

// Validate는 설정의 유효성을 검사합니다.
func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		c.ListenAddr = DefaultConfig.ListenAddr
	}
	if c.MaxPeers <= 0 {
		c.MaxPeers = DefaultConfig.MaxPeers
	}
	if c.MaxInboundPeers <= 0 {
		c.MaxInboundPeers = DefaultConfig.MaxInboundPeers
	}
	if c.MaxOutboundPeers <= 0 {
		c.MaxOutboundPeers = DefaultConfig.MaxOutboundPeers
	}
	if c.UserAgent == "" {
		c.UserAgent = DefaultConfig.UserAgent
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = DefaultConfig.DialTimeout
	}
	if c.HandshakeTimeout <= 0 {
		c.HandshakeTimeout = DefaultConfig.HandshakeTimeout
	}
	if c.PingInterval <= 0 {
		c.PingInterval = DefaultConfig.PingInterval
	}
	if c.PingTimeout <= 0 {
		c.PingTimeout = DefaultConfig.PingTimeout
	}
	if c.DiscoveryInterval <= 0 {
		c.DiscoveryInterval = DefaultConfig.DiscoveryInterval
	}

	// 피어 수 제한 합이 MaxPeers를 초과하지 않도록
	if c.MaxInboundPeers+c.MaxOutboundPeers > c.MaxPeers {
		c.MaxInboundPeers = c.MaxPeers / 2
		c.MaxOutboundPeers = c.MaxPeers - c.MaxInboundPeers
	}

	return nil
}

// ToPeerManagerConfig는 PeerManager 설정으로 변환합니다.
func (c *Config) ToPeerManagerConfig() peer.ManagerConfig {
	return peer.ManagerConfig{
		MaxPeers:         c.MaxPeers,
		MaxInboundPeers:  c.MaxInboundPeers,
		MaxOutboundPeers: c.MaxOutboundPeers,
	}
}
