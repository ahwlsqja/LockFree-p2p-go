// Package main은 P2P 노드의 실행 진입점입니다.
//
// [실행 방법]
//
//	# 노드 1 실행 (포트 3000)
//	go run cmd/node/main.go --port 3000
//
//	# 노드 2 실행 (노드 1에 연결)
//	go run cmd/node/main.go --port 3001 --seed localhost:3000
//
//	# 노드 3 실행 (노드 1에 연결)
//	go run cmd/node/main.go --port 3002 --seed localhost:3000
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-p2p-network/go-p2p/pkg/node"
)

// =============================================================================
// 커맨드라인 플래그
// =============================================================================

// 커맨드라인 플래그들
//
// [flag 패키지 동작]
// - flag.Parse() 호출 시 os.Args 파싱
// - 각 플래그 변수에 값 저장
// - 미지정 시 기본값 사용
var (
	// port는 리스닝 포트입니다.
	// 예: --port 3000
	port = flag.Int("port", 3000, "리스닝 포트")

	// seeds는 시드 노드 주소들입니다.
	// 쉼표로 구분된 목록
	// 예: --seed localhost:3000,localhost:3001
	seeds = flag.String("seed", "", "시드 노드 주소 (쉼표 구분)")

	// maxPeers는 최대 피어 수입니다.
	maxPeers = flag.Int("max-peers", 50, "최대 피어 연결 수")

	// userAgent는 클라이언트 식별자입니다.
	userAgent = flag.String("user-agent", "go-p2p/1.0.0", "클라이언트 식별자")

	// verbose는 상세 로그 출력 여부입니다.
	verbose = flag.Bool("verbose", false, "상세 로그 출력")
)

func main() {
	// 플래그 파싱
	flag.Parse()

	// 로거 설정
	//
	// [log 패키지 플래그]
	// - Ldate: 날짜 (2009/01/23)
	// - Ltime: 시간 (01:23:23)
	// - Lmicroseconds: 마이크로초 (01:23:23.123123)
	// - Lshortfile: 파일명:라인 (file.go:123)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	if *verbose {
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	}

	// 시드 목록 파싱
	var seedList []string
	if *seeds != "" {
		seedList = strings.Split(*seeds, ",")
		for i, s := range seedList {
			seedList[i] = strings.TrimSpace(s)
		}
	}

	// 노드 설정
	config := node.Config{
		ListenAddr:        fmt.Sprintf(":%d", *port),
		Seeds:             seedList,
		MaxPeers:          *maxPeers,
		MaxInboundPeers:   *maxPeers / 2,
		MaxOutboundPeers:  *maxPeers / 2,
		UserAgent:         *userAgent,
		DialTimeout:       10 * time.Second,
		HandshakeTimeout:  5 * time.Second,
		PingInterval:      30 * time.Second,
		PingTimeout:       10 * time.Second,
		DiscoveryInterval: 30 * time.Second,
	}

	// 노드 생성
	n, err := node.New(config)
	if err != nil {
		log.Fatalf("노드 생성 실패: %v", err)
	}

	// 노드 시작
	if err := n.Start(); err != nil {
		log.Fatalf("노드 시작 실패: %v", err)
	}

	log.Printf("====================================")
	log.Printf("P2P 노드 실행 중")
	log.Printf("노드 ID: %s", n.ID().ShortString())
	log.Printf("주소: %s", n.ListenAddr())
	if len(seedList) > 0 {
		log.Printf("시드: %v", seedList)
	}
	log.Printf("====================================")

	// 상태 출력 고루틴
	go statusLoop(n)

	// 시그널 대기
	//
	// [시그널 처리]
	// - SIGINT: Ctrl+C
	// - SIGTERM: kill 명령 (k8s 등에서 사용)
	//
	// [처리 방식]
	// 1. signal.Notify로 채널에 시그널 전달 요청
	// 2. 채널에서 시그널 대기
	// 3. 시그널 수신 시 graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 시그널 대기
	sig := <-sigCh
	log.Printf("시그널 수신: %v, 종료 중...", sig)

	// 노드 종료
	if err := n.Stop(); err != nil {
		log.Printf("노드 종료 중 에러: %v", err)
	}

	log.Printf("노드 종료됨")
}

// statusLoop는 주기적으로 노드 상태를 출력합니다.
func statusLoop(n *node.Node) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if !n.IsRunning() {
			return
		}

		peers := n.GetPeers()
		if len(peers) == 0 {
			log.Printf("[상태] 연결된 피어 없음")
		} else {
			log.Printf("[상태] 연결된 피어: %d", len(peers))
			for _, p := range peers {
				stats := p.Stats()
				log.Printf("  - %s (%s, %s) sent=%d recv=%d",
					p.ID().ShortString(),
					p.Addr(),
					p.Direction(),
					stats.MessagesSent,
					stats.MessagesRecv)
			}
		}
	}
}
