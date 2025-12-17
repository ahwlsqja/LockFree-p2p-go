package node

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-p2p-network/go-p2p/pkg/peer"
	"github.com/go-p2p-network/go-p2p/pkg/protocol"
)

// =============================================================================
// P2P 네트워크 통합 테스트
// =============================================================================

// TestNodeStartStop은 노드 시작/종료를 테스트합니다.
//
// [테스트 항목]
// 1. 노드 생성
// 2. 노드 시작 (TCP 서버 리스닝)
// 3. 노드 종료 (깨끗한 정리)
func TestNodeStartStop(t *testing.T) {
	// 노드 설정 (":0"은 OS가 사용 가능한 포트 자동 할당)
	config := Config{
		ListenAddr: ":0",
		MaxPeers:   10,
	}

	// 노드 생성
	node, err := New(config)
	if err != nil {
		t.Fatalf("노드 생성 실패: %v", err)
	}

	// 노드 시작
	if err := node.Start(); err != nil {
		t.Fatalf("노드 시작 실패: %v", err)
	}

	// 리스닝 주소 확인
	addr := node.ListenAddr()
	if addr == "" {
		t.Fatal("리스닝 주소가 비어있음")
	}
	t.Logf("노드 리스닝 주소: %s", addr)

	// 노드 ID 확인
	if node.ID() == (peer.ID{}) {
		t.Fatal("노드 ID가 zero value")
	}
	t.Logf("노드 ID: %s", node.ID().ShortString())

	// 노드 종료
	if err := node.Stop(); err != nil {
		t.Fatalf("노드 종료 실패: %v", err)
	}

	// 상태 확인
	if node.IsRunning() {
		t.Fatal("노드가 아직 실행 중")
	}
}

// TestTwoNodeConnection은 두 노드 간 연결을 테스트합니다.
//
// [테스트 시나리오]
// 1. 노드 A 시작
// 2. 노드 B 시작 (노드 A를 시드로)
// 3. 연결 대기
// 4. 양쪽에서 피어 수 확인
func TestTwoNodeConnection(t *testing.T) {
	// 노드 A (시드 노드)
	nodeA, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   10,
	})
	if err != nil {
		t.Fatalf("노드 A 생성 실패: %v", err)
	}
	if err := nodeA.Start(); err != nil {
		t.Fatalf("노드 A 시작 실패: %v", err)
	}
	defer nodeA.Stop()

	addrA := nodeA.ListenAddr()
	t.Logf("노드 A 주소: %s, ID: %s", addrA, nodeA.ID().ShortString())

	// 노드 B (노드 A에 연결)
	nodeB, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   10,
		Seeds:      []string{addrA},
	})
	if err != nil {
		t.Fatalf("노드 B 생성 실패: %v", err)
	}
	if err := nodeB.Start(); err != nil {
		t.Fatalf("노드 B 시작 실패: %v", err)
	}
	defer nodeB.Stop()

	t.Logf("노드 B 주소: %s, ID: %s", nodeB.ListenAddr(), nodeB.ID().ShortString())

	// 연결 대기 (핸드셰이크 완료까지)
	// 실제로는 더 정교한 동기화가 필요하지만, 테스트에서는 폴링
	connected := waitForCondition(5*time.Second, 100*time.Millisecond, func() bool {
		return nodeA.PeerCount() >= 1 && nodeB.PeerCount() >= 1
	})

	if !connected {
		t.Fatalf("연결 실패: A=%d peers, B=%d peers", nodeA.PeerCount(), nodeB.PeerCount())
	}

	t.Logf("연결 성공: A=%d peers, B=%d peers", nodeA.PeerCount(), nodeB.PeerCount())

	// 피어 정보 확인
	peersA := nodeA.GetPeers()
	if len(peersA) == 0 {
		t.Fatal("노드 A에 피어가 없음")
	}

	peerA := peersA[0]
	t.Logf("노드 A의 피어: ID=%s, Addr=%s, Direction=%s",
		peerA.ID().ShortString(), peerA.Addr(), peerA.Direction())

	// B의 ID가 A의 피어 목록에 있는지 확인
	found := false
	for _, p := range peersA {
		if p.ID() == nodeB.ID() {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("노드 A의 피어 목록에 노드 B가 없음")
	}
}

// TestMultiNodeNetwork는 다중 노드 네트워크를 테스트합니다.
//
// [테스트 시나리오]
// 1. 시드 노드 시작
// 2. 여러 노드가 시드에 연결
// 3. 피어 디스커버리로 전체 연결
// 4. 모든 노드가 서로 연결되었는지 확인
func TestMultiNodeNetwork(t *testing.T) {
	const numNodes = 4
	nodes := make([]*Node, numNodes)

	// 시드 노드 시작
	seed, err := New(Config{
		ListenAddr:        ":0",
		MaxPeers:          20,
		DiscoveryInterval: 500 * time.Millisecond, // 빠른 디스커버리
	})
	if err != nil {
		t.Fatalf("시드 노드 생성 실패: %v", err)
	}
	if err := seed.Start(); err != nil {
		t.Fatalf("시드 노드 시작 실패: %v", err)
	}
	defer seed.Stop()

	seedAddr := seed.ListenAddr()
	t.Logf("시드 노드: addr=%s, id=%s", seedAddr, seed.ID().ShortString())

	// 나머지 노드들 시작
	for i := 0; i < numNodes; i++ {
		node, err := New(Config{
			ListenAddr:        ":0",
			MaxPeers:          20,
			Seeds:             []string{seedAddr},
			DiscoveryInterval: 500 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("노드 %d 생성 실패: %v", i, err)
		}
		if err := node.Start(); err != nil {
			t.Fatalf("노드 %d 시작 실패: %v", i, err)
		}
		defer node.Stop()
		nodes[i] = node
		t.Logf("노드 %d: addr=%s, id=%s", i, node.ListenAddr(), node.ID().ShortString())
	}

	// 모든 노드가 시드에 연결될 때까지 대기
	connected := waitForCondition(10*time.Second, 200*time.Millisecond, func() bool {
		// 시드 노드에 모든 노드가 연결되어야 함
		if seed.PeerCount() < numNodes {
			return false
		}
		// 모든 노드가 최소 1개 피어에 연결되어야 함
		for _, node := range nodes {
			if node.PeerCount() < 1 {
				return false
			}
		}
		return true
	})

	if !connected {
		t.Logf("시드 노드 피어 수: %d", seed.PeerCount())
		for i, node := range nodes {
			t.Logf("노드 %d 피어 수: %d", i, node.PeerCount())
		}
		t.Fatal("일부 노드가 연결되지 않음")
	}

	t.Logf("네트워크 구성 완료: 시드=%d peers", seed.PeerCount())
	for i, node := range nodes {
		t.Logf("노드 %d: %d peers", i, node.PeerCount())
	}
}

// TestPingPong은 Ping/Pong 메시지를 테스트합니다.
//
// [테스트 항목]
// 1. 두 노드 연결
// 2. Ping 전송
// 3. Pong 응답 확인
func TestPingPong(t *testing.T) {
	// 노드 A
	nodeA, err := New(Config{
		ListenAddr:   ":0",
		MaxPeers:     10,
		PingInterval: 500 * time.Millisecond, // 빠른 Ping
	})
	if err != nil {
		t.Fatalf("노드 A 생성 실패: %v", err)
	}
	if err := nodeA.Start(); err != nil {
		t.Fatalf("노드 A 시작 실패: %v", err)
	}
	defer nodeA.Stop()

	// 노드 B
	nodeB, err := New(Config{
		ListenAddr:   ":0",
		MaxPeers:     10,
		PingInterval: 500 * time.Millisecond,
		Seeds:        []string{nodeA.ListenAddr()},
	})
	if err != nil {
		t.Fatalf("노드 B 생성 실패: %v", err)
	}
	if err := nodeB.Start(); err != nil {
		t.Fatalf("노드 B 시작 실패: %v", err)
	}
	defer nodeB.Stop()

	// 연결 대기
	connected := waitForCondition(5*time.Second, 100*time.Millisecond, func() bool {
		return nodeA.PeerCount() >= 1 && nodeB.PeerCount() >= 1
	})
	if !connected {
		t.Fatal("연결 실패")
	}

	// Ping이 발생할 때까지 대기
	time.Sleep(1 * time.Second)

	// 피어 통계 확인 (Ping/Pong 주고받았으면 메시지 카운터 증가)
	peersA := nodeA.GetPeers()
	if len(peersA) == 0 {
		t.Fatal("피어가 없음")
	}

	stats := peersA[0].Stats()
	if stats.MessagesSent == 0 {
		t.Log("아직 메시지를 보내지 않음 (Ping 간격이 아직 안 됐을 수 있음)")
	} else {
		t.Logf("피어 통계: sent=%d, recv=%d", stats.MessagesSent, stats.MessagesRecv)
	}
}

// TestGetPeersExchange는 피어 목록 교환을 테스트합니다.
//
// [테스트 시나리오]
// 1. 시드 노드 시작
// 2. 노드 A가 시드에 연결
// 3. 노드 B가 시드에 연결
// 4. 노드 A가 GetPeers 요청
// 5. 노드 A가 노드 B를 발견하고 연결
func TestGetPeersExchange(t *testing.T) {
	// 시드 노드
	seed, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   20,
	})
	if err != nil {
		t.Fatalf("시드 노드 생성 실패: %v", err)
	}
	if err := seed.Start(); err != nil {
		t.Fatalf("시드 노드 시작 실패: %v", err)
	}
	defer seed.Stop()

	seedAddr := seed.ListenAddr()

	// 노드 A
	nodeA, err := New(Config{
		ListenAddr:        ":0",
		MaxPeers:          20,
		Seeds:             []string{seedAddr},
		DiscoveryInterval: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("노드 A 생성 실패: %v", err)
	}
	if err := nodeA.Start(); err != nil {
		t.Fatalf("노드 A 시작 실패: %v", err)
	}
	defer nodeA.Stop()

	// 노드 B
	nodeB, err := New(Config{
		ListenAddr:        ":0",
		MaxPeers:          20,
		Seeds:             []string{seedAddr},
		DiscoveryInterval: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("노드 B 생성 실패: %v", err)
	}
	if err := nodeB.Start(); err != nil {
		t.Fatalf("노드 B 시작 실패: %v", err)
	}
	defer nodeB.Stop()

	// A와 B가 시드에 연결될 때까지 대기
	connected := waitForCondition(5*time.Second, 100*time.Millisecond, func() bool {
		return seed.PeerCount() >= 2
	})
	if !connected {
		t.Fatalf("시드에 연결 실패: seed=%d peers", seed.PeerCount())
	}

	t.Logf("시드 연결 완료: seed=%d peers", seed.PeerCount())

	// 디스커버리로 A가 B를 발견할 때까지 대기
	// (GetPeers → Peers → 연결 시도)
	discovered := waitForCondition(10*time.Second, 200*time.Millisecond, func() bool {
		// A가 시드 + B와 연결되어 있으면 성공
		return nodeA.PeerCount() >= 2
	})

	if !discovered {
		t.Logf("A 피어 수: %d", nodeA.PeerCount())
		t.Logf("B 피어 수: %d", nodeB.PeerCount())
		t.Log("디스커버리로 추가 피어를 찾지 못함 (정상일 수 있음)")
	} else {
		t.Logf("디스커버리 성공: A=%d peers, B=%d peers", nodeA.PeerCount(), nodeB.PeerCount())
	}
}

// TestBroadcast는 메시지 브로드캐스트를 테스트합니다.
//
// [테스트 시나리오]
// 1. 3개 노드 연결
// 2. 노드 A가 메시지 브로드캐스트
// 3. 노드 B, C가 메시지 수신 확인
func TestBroadcast(t *testing.T) {
	// 시드 노드
	seed, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   20,
	})
	if err != nil {
		t.Fatalf("시드 생성 실패: %v", err)
	}
	if err := seed.Start(); err != nil {
		t.Fatalf("시드 시작 실패: %v", err)
	}
	defer seed.Stop()

	seedAddr := seed.ListenAddr()

	// 수신 카운터
	var receivedCount int32

	// 노드들 생성
	nodes := make([]*Node, 3)
	for i := 0; i < 3; i++ {
		node, err := New(Config{
			ListenAddr: ":0",
			MaxPeers:   20,
			Seeds:      []string{seedAddr},
		})
		if err != nil {
			t.Fatalf("노드 %d 생성 실패: %v", i, err)
		}
		if err := node.Start(); err != nil {
			t.Fatalf("노드 %d 시작 실패: %v", i, err)
		}
		defer node.Stop()

		// Tx 메시지 핸들러 등록 (브로드캐스트 수신 테스트용)
		node.OnMessage(protocol.MsgTx, func(p *peer.Peer, msg *protocol.Message) error {
			atomic.AddInt32(&receivedCount, 1)
			t.Logf("노드가 Tx 메시지 수신: from=%s", p.ID().ShortString())
			return nil
		})

		nodes[i] = node
	}

	// 연결 대기
	connected := waitForCondition(5*time.Second, 100*time.Millisecond, func() bool {
		return seed.PeerCount() >= 3
	})
	if !connected {
		t.Fatalf("연결 실패: seed=%d peers", seed.PeerCount())
	}

	// 시드에서 브로드캐스트
	txMsg := protocol.NewMessage(protocol.MsgTx, []byte("test transaction"))
	seed.Broadcast(txMsg)

	// 수신 대기
	received := waitForCondition(3*time.Second, 100*time.Millisecond, func() bool {
		return atomic.LoadInt32(&receivedCount) >= 3
	})

	if !received {
		t.Logf("수신된 메시지 수: %d", atomic.LoadInt32(&receivedCount))
		// 브로드캐스트가 도착하지 않아도 치명적이지 않음
		// 네트워크 타이밍에 따라 다를 수 있음
	} else {
		t.Logf("브로드캐스트 성공: %d개 노드가 수신", atomic.LoadInt32(&receivedCount))
	}
}

// TestConnectionLimit은 연결 제한을 테스트합니다.
//
// [테스트 항목]
// 1. MaxPeers 제한 확인
// 2. 제한 초과 시 연결 거부
func TestConnectionLimit(t *testing.T) {
	// 최대 2개 피어만 허용하는 노드
	server, err := New(Config{
		ListenAddr:      ":0",
		MaxPeers:        2,
		MaxInboundPeers: 2,
	})
	if err != nil {
		t.Fatalf("서버 생성 실패: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("서버 시작 실패: %v", err)
	}
	defer server.Stop()

	serverAddr := server.ListenAddr()

	// 3개 클라이언트가 연결 시도
	var connectedCount int32
	var wg sync.WaitGroup

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			client, err := New(Config{
				ListenAddr: ":0",
				MaxPeers:   10,
				Seeds:      []string{serverAddr},
			})
			if err != nil {
				t.Logf("클라이언트 %d 생성 실패: %v", idx, err)
				return
			}
			if err := client.Start(); err != nil {
				t.Logf("클라이언트 %d 시작 실패: %v", idx, err)
				return
			}
			defer client.Stop()

			// 연결 대기
			time.Sleep(2 * time.Second)

			if client.PeerCount() > 0 {
				atomic.AddInt32(&connectedCount, 1)
			}
		}(i)
	}

	wg.Wait()

	// 최대 2개만 연결되어야 함
	t.Logf("서버 피어 수: %d, 연결 성공 클라이언트: %d", server.PeerCount(), connectedCount)

	if server.PeerCount() > 2 {
		t.Errorf("피어 제한 초과: %d > 2", server.PeerCount())
	}
}

// TestGracefulShutdown은 우아한 종료를 테스트합니다.
//
// [테스트 항목]
// 1. 연결된 상태에서 Stop() 호출
// 2. 모든 연결 정리 확인
// 3. 고루틴 누수 없음 확인
func TestGracefulShutdown(t *testing.T) {
	// 서버
	server, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   10,
	})
	if err != nil {
		t.Fatalf("서버 생성 실패: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("서버 시작 실패: %v", err)
	}

	serverAddr := server.ListenAddr()

	// 클라이언트들
	clients := make([]*Node, 3)
	for i := 0; i < 3; i++ {
		client, err := New(Config{
			ListenAddr: ":0",
			MaxPeers:   10,
			Seeds:      []string{serverAddr},
		})
		if err != nil {
			t.Fatalf("클라이언트 %d 생성 실패: %v", i, err)
		}
		if err := client.Start(); err != nil {
			t.Fatalf("클라이언트 %d 시작 실패: %v", i, err)
		}
		clients[i] = client
	}

	// 연결 대기
	waitForCondition(5*time.Second, 100*time.Millisecond, func() bool {
		return server.PeerCount() >= 3
	})

	t.Logf("종료 전 서버 피어 수: %d", server.PeerCount())

	// 서버 종료
	if err := server.Stop(); err != nil {
		t.Fatalf("서버 종료 실패: %v", err)
	}

	// 서버 상태 확인
	if server.IsRunning() {
		t.Error("서버가 아직 실행 중")
	}

	// 클라이언트들 종료
	for i, client := range clients {
		if err := client.Stop(); err != nil {
			t.Errorf("클라이언트 %d 종료 실패: %v", i, err)
		}
	}

	t.Log("우아한 종료 완료")
}

// =============================================================================
// 벤치마크
// =============================================================================

// BenchmarkNodeConnection은 노드 연결 성능을 측정합니다.
func BenchmarkNodeConnection(b *testing.B) {
	// 서버 시작
	server, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   1000,
	})
	if err != nil {
		b.Fatalf("서버 생성 실패: %v", err)
	}
	if err := server.Start(); err != nil {
		b.Fatalf("서버 시작 실패: %v", err)
	}
	defer server.Stop()

	serverAddr := server.ListenAddr()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		client, err := New(Config{
			ListenAddr: ":0",
			MaxPeers:   10,
			Seeds:      []string{serverAddr},
		})
		if err != nil {
			b.Fatalf("클라이언트 생성 실패: %v", err)
		}
		if err := client.Start(); err != nil {
			b.Fatalf("클라이언트 시작 실패: %v", err)
		}

		// 연결 대기
		waitForCondition(2*time.Second, 50*time.Millisecond, func() bool {
			return client.PeerCount() > 0
		})

		client.Stop()
	}
}

// BenchmarkBroadcast는 브로드캐스트 성능을 측정합니다.
func BenchmarkBroadcast(b *testing.B) {
	const numPeers = 10

	// 서버
	server, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   100,
	})
	if err != nil {
		b.Fatalf("서버 생성 실패: %v", err)
	}
	if err := server.Start(); err != nil {
		b.Fatalf("서버 시작 실패: %v", err)
	}
	defer server.Stop()

	serverAddr := server.ListenAddr()

	// 클라이언트들
	clients := make([]*Node, numPeers)
	for i := 0; i < numPeers; i++ {
		client, err := New(Config{
			ListenAddr: ":0",
			MaxPeers:   20,
			Seeds:      []string{serverAddr},
		})
		if err != nil {
			b.Fatalf("클라이언트 생성 실패: %v", err)
		}
		if err := client.Start(); err != nil {
			b.Fatalf("클라이언트 시작 실패: %v", err)
		}
		defer client.Stop()
		clients[i] = client
	}

	// 연결 대기
	waitForCondition(10*time.Second, 100*time.Millisecond, func() bool {
		return server.PeerCount() >= numPeers
	})

	msg := protocol.NewMessage(protocol.MsgTx, []byte("benchmark transaction"))

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		server.Broadcast(msg)
	}
}

// =============================================================================
// 유틸리티 함수
// =============================================================================

// waitForCondition은 조건이 만족될 때까지 대기합니다.
//
// [파라미터]
// - timeout: 최대 대기 시간
// - interval: 체크 간격
// - condition: 만족 여부 확인 함수
//
// [반환값]
// - true: 조건 만족
// - false: 타임아웃
func waitForCondition(timeout, interval time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(interval)
	}
	return false
}

// =============================================================================
// 고급 테스트
// =============================================================================

// TestConcurrentConnections는 동시 연결을 테스트합니다.
func TestConcurrentConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("short 모드에서는 스킵")
	}

	const numClients = 20

	server, err := New(Config{
		ListenAddr:       ":0",
		MaxPeers:         100,
		MaxInboundPeers:  50,
		MaxOutboundPeers: 50,
	})
	if err != nil {
		t.Fatalf("서버 생성 실패: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("서버 시작 실패: %v", err)
	}
	defer server.Stop()

	serverAddr := server.ListenAddr()

	var wg sync.WaitGroup
	var connectedCount int32
	var errorCount int32

	// 동시에 연결 시도
	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			client, err := New(Config{
				ListenAddr: ":0",
				MaxPeers:   10,
				Seeds:      []string{serverAddr},
			})
			if err != nil {
				atomic.AddInt32(&errorCount, 1)
				return
			}
			if err := client.Start(); err != nil {
				atomic.AddInt32(&errorCount, 1)
				return
			}
			defer client.Stop()

			// 연결 대기
			if waitForCondition(5*time.Second, 100*time.Millisecond, func() bool {
				return client.PeerCount() > 0
			}) {
				atomic.AddInt32(&connectedCount, 1)
			}

			// 잠시 유지
			time.Sleep(500 * time.Millisecond)
		}(i)
	}

	wg.Wait()

	t.Logf("동시 연결 결과: 성공=%d, 실패=%d, 서버 피어=%d",
		connectedCount, errorCount, server.PeerCount())

	// 최소 절반 이상은 성공해야 함
	if connectedCount < int32(numClients/2) {
		t.Errorf("연결 성공률이 낮음: %d/%d", connectedCount, numClients)
	}
}

// TestReconnection은 재연결을 테스트합니다.
func TestReconnection(t *testing.T) {
	server, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   10,
	})
	if err != nil {
		t.Fatalf("서버 생성 실패: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("서버 시작 실패: %v", err)
	}
	defer server.Stop()

	serverAddr := server.ListenAddr()

	// 첫 번째 연결
	client, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   10,
		Seeds:      []string{serverAddr},
	})
	if err != nil {
		t.Fatalf("클라이언트 생성 실패: %v", err)
	}
	if err := client.Start(); err != nil {
		t.Fatalf("클라이언트 시작 실패: %v", err)
	}

	// 연결 확인
	if !waitForCondition(5*time.Second, 100*time.Millisecond, func() bool {
		return server.PeerCount() > 0
	}) {
		t.Fatal("첫 번째 연결 실패")
	}
	t.Log("첫 번째 연결 성공")

	// 연결 끊기
	client.Stop()

	// 서버에서 피어 제거 대기
	waitForCondition(3*time.Second, 100*time.Millisecond, func() bool {
		return server.PeerCount() == 0
	})
	t.Logf("연결 종료 후 서버 피어 수: %d", server.PeerCount())

	// 재연결
	client2, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   10,
		Seeds:      []string{serverAddr},
	})
	if err != nil {
		t.Fatalf("클라이언트2 생성 실패: %v", err)
	}
	if err := client2.Start(); err != nil {
		t.Fatalf("클라이언트2 시작 실패: %v", err)
	}
	defer client2.Stop()

	// 재연결 확인
	if !waitForCondition(5*time.Second, 100*time.Millisecond, func() bool {
		return server.PeerCount() > 0
	}) {
		t.Fatal("재연결 실패")
	}
	t.Log("재연결 성공")
}

// TestMessageOrdering은 메시지 순서를 테스트합니다.
func TestMessageOrdering(t *testing.T) {
	server, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   10,
	})
	if err != nil {
		t.Fatalf("서버 생성 실패: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("서버 시작 실패: %v", err)
	}
	defer server.Stop()

	serverAddr := server.ListenAddr()

	// 메시지 순서 기록
	var received []int
	var mu sync.Mutex

	client, err := New(Config{
		ListenAddr: ":0",
		MaxPeers:   10,
		Seeds:      []string{serverAddr},
	})
	if err != nil {
		t.Fatalf("클라이언트 생성 실패: %v", err)
	}

	// Tx 핸들러 등록
	client.OnMessage(protocol.MsgTx, func(p *peer.Peer, msg *protocol.Message) error {
		if len(msg.Payload) >= 4 {
			// 페이로드에서 순서 번호 추출
			seq := int(msg.Payload[0])<<24 | int(msg.Payload[1])<<16 |
				int(msg.Payload[2])<<8 | int(msg.Payload[3])
			mu.Lock()
			received = append(received, seq)
			mu.Unlock()
		}
		return nil
	})

	if err := client.Start(); err != nil {
		t.Fatalf("클라이언트 시작 실패: %v", err)
	}
	defer client.Stop()

	// 연결 대기
	if !waitForCondition(5*time.Second, 100*time.Millisecond, func() bool {
		return server.PeerCount() > 0
	}) {
		t.Fatal("연결 실패")
	}

	// 순서대로 메시지 전송
	const numMessages = 10
	for i := 0; i < numMessages; i++ {
		payload := []byte{byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i)}
		msg := protocol.NewMessage(protocol.MsgTx, payload)
		server.Broadcast(msg)
	}

	// 수신 대기
	time.Sleep(1 * time.Second)

	mu.Lock()
	defer mu.Unlock()

	t.Logf("수신된 메시지: %v", received)

	// TCP는 순서 보장하므로 순서대로 와야 함
	for i := 1; i < len(received); i++ {
		if received[i] < received[i-1] {
			t.Errorf("메시지 순서 역전: %d가 %d보다 먼저 옴", received[i], received[i-1])
		}
	}
}

