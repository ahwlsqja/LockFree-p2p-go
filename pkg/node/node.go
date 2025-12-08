package node

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-p2p-network/go-p2p/pkg/peer"
	"github.com/go-p2p-network/go-p2p/pkg/protocol"
	"github.com/go-p2p-network/go-p2p/pkg/transport"
)

// =============================================================================
// Node - P2P 네트워크 노드
// =============================================================================

// Node는 P2P 네트워크의 노드입니다.
//
// [노드의 역할]
// 1. 서버: 다른 노드로부터 연결 수신 (인바운드)
// 2. 클라이언트: 다른 노드에 연결 (아웃바운드)
// 3. 피어 관리: 연결된 피어들 관리
// 4. 메시지 처리: 수신된 메시지 처리 및 라우팅
//
// [생명주기]
// New() → Start() → [Running] → Stop()
//
// [동시성]
// - 여러 고루틴이 동시에 동작
// - 각 피어별 읽기 고루틴
// - 서버 Accept 고루틴
// - 디스커버리 고루틴
// - Ping 고루틴
type Node struct {
	// config는 노드 설정입니다.
	config Config

	// id는 노드의 고유 식별자입니다.
	id peer.ID

	// server는 TCP 서버입니다.
	server *transport.TCPServer

	// peerManager는 피어 연결을 관리합니다.
	peerManager *peer.PeerManager

	// nonce는 핸드셰이크에 사용되는 랜덤 값입니다.
	// 자기 자신에게 연결하는 것을 감지하는 데 사용
	nonce uint64

	// listenPort는 실제 리스닝 포트입니다.
	// ":0"으로 시작하면 OS가 할당한 포트
	listenPort uint16

	// handlers는 메시지 타입별 핸들러입니다.
	handlers   map[protocol.MessageType]MessageHandler
	handlersMu sync.RWMutex

	// ctx와 cancel은 노드 종료를 제어합니다.
	//
	// [Context 사용 이유]
	// - 여러 고루틴에 종료 신호를 전파
	// - cancel() 호출 시 모든 ctx.Done() 채널이 닫힘
	// - 각 고루틴은 select로 종료 신호 감지
	ctx    context.Context
	cancel context.CancelFunc

	// wg는 모든 고루틴 종료를 대기합니다.
	//
	// [WaitGroup 사용 이유]
	// - Stop()에서 모든 고루틴이 종료될 때까지 대기
	// - 고루틴 시작 시 Add(1), 종료 시 Done()
	wg sync.WaitGroup

	// running은 노드 실행 상태입니다.
	running   bool
	runningMu sync.RWMutex
}

// MessageHandler는 메시지 처리 함수 타입입니다.
//
// [파라미터]
// - p: 메시지를 보낸 피어
// - msg: 수신된 메시지
//
// [반환값]
// - error: 처리 실패 시 (연결 종료 등)
type MessageHandler func(p *peer.Peer, msg *protocol.Message) error

// New는 새로운 노드를 생성합니다.
//
// [초기화되는 것들]
// 1. 노드 ID 생성 (랜덤)
// 2. Nonce 생성 (랜덤)
// 3. PeerManager 생성
// 4. 기본 메시지 핸들러 등록
func New(config Config) (*Node, error) {
	// 설정 검증 및 기본값 적용
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("설정 검증 실패: %w", err)
	}

	// 노드 ID 생성
	id, err := peer.GenerateID()
	if err != nil {
		return nil, fmt.Errorf("노드 ID 생성 실패: %w", err)
	}

	// Nonce 생성
	var nonceBuf [8]byte
	if _, err := rand.Read(nonceBuf[:]); err != nil {
		return nil, fmt.Errorf("nonce 생성 실패: %w", err)
	}
	nonce := binary.BigEndian.Uint64(nonceBuf[:])

	// PeerManager 생성
	peerManager := peer.NewPeerManager(config.ToPeerManagerConfig())

	node := &Node{
		config:      config,
		id:          id,
		peerManager: peerManager,
		nonce:       nonce,
		handlers:    make(map[protocol.MessageType]MessageHandler),
	}

	// 기본 메시지 핸들러 등록
	node.registerDefaultHandlers()

	return node, nil
}

// registerDefaultHandlers는 기본 메시지 핸들러를 등록합니다.
func (n *Node) registerDefaultHandlers() {
	n.handlers[protocol.MsgPing] = n.handlePing
	n.handlers[protocol.MsgPong] = n.handlePong
	n.handlers[protocol.MsgGetPeers] = n.handleGetPeers
	n.handlers[protocol.MsgPeers] = n.handlePeers
	n.handlers[protocol.MsgDisconnect] = n.handleDisconnect
}

// =============================================================================
// 노드 시작/종료
// =============================================================================

// Start는 노드를 시작합니다.
//
// [시작되는 것들]
// 1. TCP 서버 시작 (Listen)
// 2. Accept 루프 고루틴
// 3. 시드 노드 연결
// 4. 디스커버리 고루틴
// 5. Ping 루프 고루틴
func (n *Node) Start() error {
	n.runningMu.Lock()
	if n.running {
		n.runningMu.Unlock()
		return fmt.Errorf("노드가 이미 실행 중입니다")
	}
	n.running = true
	n.runningMu.Unlock()

	// Context 생성
	n.ctx, n.cancel = context.WithCancel(context.Background())

	// TCP 서버 시작
	n.server = transport.NewTCPServer(n.config.ListenAddr)
	if err := n.server.Start(); err != nil {
		return fmt.Errorf("TCP 서버 시작 실패: %w", err)
	}

	// 실제 리스닝 포트 저장
	addr := n.server.Addr().String()
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	n.listenPort = uint16(port)

	log.Printf("노드 시작: ID=%s, 주소=%s", n.id.ShortString(), addr)

	// Accept 루프 시작
	n.wg.Add(1)
	go n.acceptLoop()

	// 시드 노드 연결
	for _, seed := range n.config.Seeds {
		go n.connectToPeer(seed)
	}

	// 디스커버리 루프 시작
	n.wg.Add(1)
	go n.discoveryLoop()

	// Ping 루프 시작
	n.wg.Add(1)
	go n.pingLoop()

	return nil
}

// Stop은 노드를 종료합니다.
//
// [종료 순서]
// 1. Context 취소 (모든 고루틴에 종료 신호)
// 2. 서버 종료 (Accept 루프 종료)
// 3. 모든 피어 연결 종료
// 4. 모든 고루틴 종료 대기
func (n *Node) Stop() error {
	n.runningMu.Lock()
	if !n.running {
		n.runningMu.Unlock()
		return nil
	}
	n.running = false
	n.runningMu.Unlock()

	log.Printf("노드 종료 중...")

	// 1. 모든 고루틴에 종료 신호
	if n.cancel != nil {
		n.cancel()
	}

	// 2. 서버 종료
	if n.server != nil {
		n.server.Close()
	}

	// 3. 모든 피어 연결 종료
	if n.peerManager != nil {
		n.peerManager.Close()
	}

	// 4. 모든 고루틴 종료 대기
	n.wg.Wait()

	log.Printf("노드 종료 완료")
	return nil
}

// IsRunning은 노드가 실행 중인지 반환합니다.
func (n *Node) IsRunning() bool {
	n.runningMu.RLock()
	defer n.runningMu.RUnlock()
	return n.running
}

// =============================================================================
// Accept 루프 - 인바운드 연결 처리
// =============================================================================

// acceptLoop는 새 연결을 수락하는 루프입니다.
//
// [동작 방식]
// 1. Accept()로 새 연결 대기 (블로킹)
// 2. 연결 수락 시 별도 고루틴에서 핸드셰이크
// 3. 핸드셰이크 성공 시 피어 등록 및 읽기 루프 시작
// 4. Context 취소 또는 서버 종료 시 루프 종료
func (n *Node) acceptLoop() {
	defer n.wg.Done()

	for {
		// Context 확인
		select {
		case <-n.ctx.Done():
			return
		default:
		}

		// 새 연결 수락
		conn, err := n.server.Accept()
		if err != nil {
			// 서버가 종료된 경우
			select {
			case <-n.ctx.Done():
				return
			default:
				log.Printf("연결 수락 실패: %v", err)
				continue
			}
		}

		// 인바운드 제한 확인
		if !n.peerManager.CanAcceptInbound() {
			log.Printf("인바운드 피어 수 초과, 연결 거부: %s", conn.RemoteAddr())
			conn.Close()
			continue
		}

		// 별도 고루틴에서 핸드셰이크 처리
		go n.handleInboundConnection(conn)
	}
}

// handleInboundConnection은 인바운드 연결을 처리합니다.
func (n *Node) handleInboundConnection(conn *transport.Connection) {
	// 핸드셰이크 타임아웃 설정
	conn.SetReadTimeout(n.config.HandshakeTimeout)
	conn.SetWriteTimeout(n.config.HandshakeTimeout)

	// 피어 객체 생성
	p := peer.NewPeer(conn, peer.Inbound)

	// 핸드셰이크 수신 대기
	if err := n.receiveHandshake(p); err != nil {
		log.Printf("인바운드 핸드셰이크 실패 (%s): %v", conn.RemoteAddr(), err)
		conn.Close()
		return
	}

	// 핸드셰이크 응답 전송
	if err := n.sendHandshakeAck(p, true, ""); err != nil {
		log.Printf("핸드셰이크 응답 전송 실패: %v", err)
		conn.Close()
		return
	}

	// 타임아웃 원복
	conn.SetReadTimeout(transport.DefaultReadTimeout)
	conn.SetWriteTimeout(transport.DefaultWriteTimeout)

	// 피어 등록 및 읽기 루프 시작
	n.startPeer(p)
}

// =============================================================================
// 아웃바운드 연결
// =============================================================================

// connectToPeer는 피어에 연결을 시도합니다.
func (n *Node) connectToPeer(addr string) {
	// 이미 연결된 주소인지 확인
	if n.peerManager.HasPeerByAddr(addr) {
		return
	}

	// 아웃바운드 제한 확인
	if !n.peerManager.CanDialOutbound() {
		return
	}

	log.Printf("피어 연결 시도: %s", addr)

	// TCP 연결
	conn, err := transport.Dial(addr, transport.DialConfig{
		Timeout: n.config.DialTimeout,
	})
	if err != nil {
		log.Printf("피어 연결 실패 (%s): %v", addr, err)
		return
	}

	// 핸드셰이크 타임아웃 설정
	conn.SetReadTimeout(n.config.HandshakeTimeout)
	conn.SetWriteTimeout(n.config.HandshakeTimeout)

	// 피어 객체 생성
	p := peer.NewPeer(conn, peer.Outbound)
	p.SetAddr(addr)

	// 핸드셰이크 전송
	if err := n.sendHandshake(p); err != nil {
		log.Printf("핸드셰이크 전송 실패 (%s): %v", addr, err)
		conn.Close()
		return
	}

	// 핸드셰이크 응답 수신
	if err := n.receiveHandshakeAck(p); err != nil {
		log.Printf("핸드셰이크 응답 수신 실패 (%s): %v", addr, err)
		conn.Close()
		return
	}

	// 타임아웃 원복
	conn.SetReadTimeout(transport.DefaultReadTimeout)
	conn.SetWriteTimeout(transport.DefaultWriteTimeout)

	// 피어 등록 및 읽기 루프 시작
	n.startPeer(p)
}

// =============================================================================
// 핸드셰이크
// =============================================================================

// sendHandshake는 핸드셰이크 메시지를 전송합니다.
func (n *Node) sendHandshake(p *peer.Peer) error {
	payload := &protocol.HandshakePayload{
		Version:    protocol.ProtocolVersion,
		NodeID:     n.id,
		ListenPort: n.listenPort,
		UserAgent:  n.config.UserAgent,
		Nonce:      n.nonce,
	}

	msg := protocol.NewHandshakeMessage(payload)
	data := msg.Encode()

	return p.WriteMessage(data)
}

// receiveHandshake는 핸드셰이크 메시지를 수신하고 처리합니다.
func (n *Node) receiveHandshake(p *peer.Peer) error {
	// 메시지 수신
	data, err := p.ReadMessage()
	if err != nil {
		return fmt.Errorf("메시지 수신 실패: %w", err)
	}

	// 메시지 디코딩
	decoder := protocol.NewDecoder(newBytesReader(data))
	msg, err := decoder.Decode()
	if err != nil {
		return fmt.Errorf("메시지 디코딩 실패: %w", err)
	}

	// 메시지 타입 확인
	if msg.Type != protocol.MsgHandshake {
		return fmt.Errorf("예상치 못한 메시지 타입: %s", msg.Type)
	}

	// 페이로드 디코딩
	payload, err := protocol.DecodeHandshakePayload(msg.Payload)
	if err != nil {
		return fmt.Errorf("핸드셰이크 페이로드 디코딩 실패: %w", err)
	}

	// 자기 자신 연결 감지
	if payload.Nonce == n.nonce {
		return fmt.Errorf("자기 자신에게 연결 시도 감지")
	}

	// 버전 호환성 확인
	if payload.Version != protocol.ProtocolVersion {
		return fmt.Errorf("프로토콜 버전 불일치: %d != %d", payload.Version, protocol.ProtocolVersion)
	}

	// 이미 연결된 노드인지 확인
	var nodeID peer.ID
	copy(nodeID[:], payload.NodeID[:])
	if n.peerManager.HasPeer(nodeID) {
		return fmt.Errorf("이미 연결된 노드: %s", nodeID.ShortString())
	}

	// 피어 정보 설정
	p.SetID(nodeID)
	p.SetVersion(payload.Version)
	p.SetUserAgent(payload.UserAgent)

	// 리스닝 주소 설정 (IP + ListenPort)
	remoteAddr := p.RemoteAddr()
	host, _, _ := net.SplitHostPort(remoteAddr)
	p.SetAddr(fmt.Sprintf("%s:%d", host, payload.ListenPort))

	return nil
}

// sendHandshakeAck는 핸드셰이크 응답을 전송합니다.
func (n *Node) sendHandshakeAck(p *peer.Peer, accepted bool, reason string) error {
	payload := &protocol.HandshakeAckPayload{
		Accepted:   accepted,
		Reason:     reason,
		Version:    protocol.ProtocolVersion,
		NodeID:     n.id,
		ListenPort: n.listenPort,
		UserAgent:  n.config.UserAgent,
		Nonce:      n.nonce,
	}

	msg := protocol.NewHandshakeAckMessage(payload)
	data := msg.Encode()

	return p.WriteMessage(data)
}

// receiveHandshakeAck는 핸드셰이크 응답을 수신합니다.
func (n *Node) receiveHandshakeAck(p *peer.Peer) error {
	// 메시지 수신
	data, err := p.ReadMessage()
	if err != nil {
		return fmt.Errorf("메시지 수신 실패: %w", err)
	}

	// 메시지 디코딩
	decoder := protocol.NewDecoder(newBytesReader(data))
	msg, err := decoder.Decode()
	if err != nil {
		return fmt.Errorf("메시지 디코딩 실패: %w", err)
	}

	// 메시지 타입 확인
	if msg.Type != protocol.MsgHandshakeAck {
		return fmt.Errorf("예상치 못한 메시지 타입: %s", msg.Type)
	}

	// 페이로드 디코딩
	payload, err := protocol.DecodeHandshakeAckPayload(msg.Payload)
	if err != nil {
		return fmt.Errorf("핸드셰이크 응답 페이로드 디코딩 실패: %w", err)
	}

	// 수락 여부 확인
	if !payload.Accepted {
		return fmt.Errorf("연결 거부됨: %s", payload.Reason)
	}

	// 자기 자신 연결 감지
	if payload.Nonce == n.nonce {
		return fmt.Errorf("자기 자신에게 연결 시도 감지")
	}

	// 피어 정보 설정
	var nodeID peer.ID
	copy(nodeID[:], payload.NodeID[:])
	p.SetID(nodeID)
	p.SetVersion(payload.Version)
	p.SetUserAgent(payload.UserAgent)

	return nil
}

// =============================================================================
// 피어 관리
// =============================================================================

// startPeer는 피어를 등록하고 메시지 읽기 루프를 시작합니다.
func (n *Node) startPeer(p *peer.Peer) {
	// 피어 상태 변경
	p.SetState(peer.StateConnected)
	p.SetConnectedAt(time.Now())

	// 피어 매니저에 등록
	if err := n.peerManager.AddPeer(p); err != nil {
		log.Printf("피어 등록 실패: %v", err)
		p.Close()
		return
	}

	log.Printf("피어 연결됨: %s (addr=%s, direction=%s)",
		p.ID().ShortString(), p.Addr(), p.Direction())

	// 읽기 루프 시작
	n.wg.Add(1)
	go n.readLoop(p)
}

// readLoop는 피어로부터 메시지를 읽는 루프입니다.
//
// [동작 방식]
// 1. 메시지 수신 대기
// 2. 메시지 디코딩
// 3. 핸들러 호출
// 4. 에러 발생 또는 Context 취소 시 종료
func (n *Node) readLoop(p *peer.Peer) {
	defer n.wg.Done()
	defer n.removePeer(p)

	for {
		// Context 확인
		select {
		case <-n.ctx.Done():
			return
		default:
		}

		// 메시지 수신
		data, err := p.ReadMessage()
		if err != nil {
			// 연결 종료 또는 에러
			if !p.IsClosed() {
				log.Printf("메시지 수신 실패 (%s): %v", p.ID().ShortString(), err)
			}
			return
		}

		// 메시지 디코딩
		decoder := protocol.NewDecoder(newBytesReader(data))
		msg, err := decoder.Decode()
		if err != nil {
			log.Printf("메시지 디코딩 실패 (%s): %v", p.ID().ShortString(), err)
			continue
		}

		// 핸들러 호출
		n.handleMessage(p, msg)
	}
}

// removePeer는 피어를 제거합니다.
func (n *Node) removePeer(p *peer.Peer) {
	if p == nil {
		return
	}

	// 피어 매니저에서 제거
	n.peerManager.RemovePeer(p.ID())

	// 연결 닫기
	p.Close()

	log.Printf("피어 연결 해제됨: %s", p.ID().ShortString())
}

// =============================================================================
// 메시지 처리
// =============================================================================

// handleMessage는 메시지를 적절한 핸들러로 전달합니다.
func (n *Node) handleMessage(p *peer.Peer, msg *protocol.Message) {
	n.handlersMu.RLock()
	handler, ok := n.handlers[msg.Type]
	n.handlersMu.RUnlock()

	if !ok {
		log.Printf("알 수 없는 메시지 타입 (%s): %s", p.ID().ShortString(), msg.Type)
		return
	}

	if err := handler(p, msg); err != nil {
		log.Printf("메시지 처리 실패 (%s, %s): %v", p.ID().ShortString(), msg.Type, err)
	}
}

// OnMessage는 메시지 타입에 대한 커스텀 핸들러를 등록합니다.
func (n *Node) OnMessage(msgType protocol.MessageType, handler MessageHandler) {
	n.handlersMu.Lock()
	n.handlers[msgType] = handler
	n.handlersMu.Unlock()
}

// =============================================================================
// 기본 메시지 핸들러
// =============================================================================

// handlePing은 Ping 메시지를 처리합니다.
func (n *Node) handlePing(p *peer.Peer, msg *protocol.Message) error {
	payload, err := protocol.DecodePingPayload(msg.Payload)
	if err != nil {
		return err
	}

	// Pong 응답
	pongMsg := protocol.NewPongMessage(payload.Nonce)
	return p.WriteMessage(pongMsg.Encode())
}

// handlePong은 Pong 메시지를 처리합니다.
func (n *Node) handlePong(p *peer.Peer, msg *protocol.Message) error {
	// Pong 수신 시 lastSeen 갱신 (ReadMessage에서 이미 됨)
	// 추가 처리가 필요하면 여기서
	return nil
}

// handleGetPeers는 GetPeers 메시지를 처리합니다.
func (n *Node) handleGetPeers(p *peer.Peer, msg *protocol.Message) error {
	// 연결된 피어 목록 수집
	peers := n.peerManager.GetConnectedPeers()

	// PeerInfo 목록 생성 (요청한 피어 제외)
	peerInfos := make([]protocol.PeerInfo, 0, len(peers))
	for _, connPeer := range peers {
		// 요청한 피어는 제외
		if connPeer.ID() == p.ID() {
			continue
		}

		// 주소 파싱
		addr := connPeer.Addr()
		if addr == "" {
			continue
		}

		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}

		// IP 파싱
		ip := net.ParseIP(host)
		if ip == nil {
			continue
		}

		// IPv4 또는 IPv6
		var ipBytes []byte
		if ip4 := ip.To4(); ip4 != nil {
			ipBytes = ip4
		} else {
			ipBytes = ip.To16()
		}

		peerInfos = append(peerInfos, protocol.PeerInfo{
			NodeID: connPeer.ID(),
			IP:     ipBytes,
			Port:   uint16(port),
		})
	}

	// Peers 메시지 전송
	peersMsg := protocol.NewPeersMessage(peerInfos)
	return p.WriteMessage(peersMsg.Encode())
}

// handlePeers는 Peers 메시지를 처리합니다.
func (n *Node) handlePeers(p *peer.Peer, msg *protocol.Message) error {
	payload, err := protocol.DecodePeersPayload(msg.Payload)
	if err != nil {
		return err
	}

	// 받은 피어들에게 연결 시도
	for _, peerInfo := range payload.Peers {
		// IP를 문자열로 변환
		var host string
		if len(peerInfo.IP) == 4 {
			host = net.IP(peerInfo.IP).String()
		} else if len(peerInfo.IP) == 16 {
			host = net.IP(peerInfo.IP).String()
		} else {
			continue
		}

		addr := fmt.Sprintf("%s:%d", host, peerInfo.Port)

		// 자기 자신은 제외
		if strings.Contains(addr, fmt.Sprintf(":%d", n.listenPort)) {
			// 더 정확한 체크가 필요할 수 있음
			continue
		}

		// 이미 연결된 피어는 제외
		if n.peerManager.HasPeerByAddr(addr) {
			continue
		}

		// 비동기로 연결 시도
		go n.connectToPeer(addr)
	}

	return nil
}

// handleDisconnect는 Disconnect 메시지를 처리합니다.
func (n *Node) handleDisconnect(p *peer.Peer, msg *protocol.Message) error {
	payload, err := protocol.DecodeDisconnectPayload(msg.Payload)
	if err != nil {
		return err
	}

	log.Printf("피어가 연결 종료 요청 (%s): %s - %s",
		p.ID().ShortString(), payload.Reason, payload.Message)

	// 피어 제거 (readLoop에서 처리됨)
	return fmt.Errorf("disconnect received")
}

// =============================================================================
// 백그라운드 루프
// =============================================================================

// discoveryLoop는 주기적으로 피어 디스커버리를 수행합니다.
func (n *Node) discoveryLoop() {
	defer n.wg.Done()

	ticker := time.NewTicker(n.config.DiscoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.discoverPeers()
		}
	}
}

// discoverPeers는 연결된 피어들에게 GetPeers 메시지를 전송합니다.
func (n *Node) discoverPeers() {
	// 피어 수가 충분하면 스킵
	if n.peerManager.IsFull() {
		return
	}

	peers := n.peerManager.GetConnectedPeers()
	if len(peers) == 0 {
		return
	}

	// 랜덤한 피어에게 GetPeers 전송 (모든 피어에게 보내면 트래픽 낭비)
	msg := protocol.NewGetPeersMessage(20)
	for _, p := range peers {
		if err := p.WriteMessage(msg.Encode()); err != nil {
			log.Printf("GetPeers 전송 실패 (%s): %v", p.ID().ShortString(), err)
		}
		break // 하나만 전송
	}
}

// pingLoop는 주기적으로 Ping을 전송합니다.
func (n *Node) pingLoop() {
	defer n.wg.Done()

	ticker := time.NewTicker(n.config.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.pingAllPeers()
		}
	}
}

// pingAllPeers는 모든 피어에게 Ping을 전송합니다.
func (n *Node) pingAllPeers() {
	var nonceBuf [8]byte
	rand.Read(nonceBuf[:])
	nonce := binary.BigEndian.Uint64(nonceBuf[:])

	msg := protocol.NewPingMessage(nonce)

	n.peerManager.ForEachPeer(func(p *peer.Peer) bool {
		if p.IsConnected() {
			if err := p.WriteMessage(msg.Encode()); err != nil {
				log.Printf("Ping 전송 실패 (%s): %v", p.ID().ShortString(), err)
			}
		}
		return true
	})
}

// =============================================================================
// 유틸리티
// =============================================================================

// ID는 노드 ID를 반환합니다.
func (n *Node) ID() peer.ID {
	return n.id
}

// ListenAddr은 리스닝 주소를 반환합니다.
func (n *Node) ListenAddr() string {
	if n.server == nil {
		return ""
	}
	return n.server.Addr().String()
}

// PeerCount는 연결된 피어 수를 반환합니다.
func (n *Node) PeerCount() int {
	return n.peerManager.Count()
}

// GetPeers는 연결된 피어 목록을 반환합니다.
func (n *Node) GetPeers() []*peer.Peer {
	return n.peerManager.GetAllPeers()
}

// Broadcast는 모든 연결된 피어에게 메시지를 전송합니다.
func (n *Node) Broadcast(msg *protocol.Message) {
	data := msg.Encode()

	n.peerManager.ForEachPeer(func(p *peer.Peer) bool {
		if p.IsConnected() {
			if err := p.WriteMessage(data); err != nil {
				log.Printf("브로드캐스트 실패 (%s): %v", p.ID().ShortString(), err)
			}
		}
		return true
	})
}

// =============================================================================
// bytesReader - bytes.Reader 대체
// =============================================================================

// bytesReader는 바이트 슬라이스에서 읽기 위한 간단한 Reader입니다.
type bytesReader struct {
	data []byte
	pos  int
}

func newBytesReader(data []byte) *bytesReader {
	return &bytesReader{data: data, pos: 0}
}

func (r *bytesReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, fmt.Errorf("EOF")
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
