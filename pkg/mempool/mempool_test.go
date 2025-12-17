package mempool

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Mempool 통합 테스트
// =============================================================================

// TestMempoolAddGet은 트랜잭션 추가/조회를 테스트합니다.
func TestMempoolAddGet(t *testing.T) {
	mp := New(nil) // 기본 설정

	// 트랜잭션 생성
	tx := createTestTx(1, 100) // nonce=1, gasPrice=100

	// 추가
	if err := mp.Add(tx); err != nil {
		t.Fatalf("트랜잭션 추가 실패: %v", err)
	}

	// 크기 확인
	if mp.Size() != 1 {
		t.Errorf("크기 불일치: got %d, want 1", mp.Size())
	}

	// 조회
	got, exists := mp.Get(tx.Hash)
	if !exists {
		t.Fatal("트랜잭션을 찾을 수 없음")
	}
	if got.Hash != tx.Hash {
		t.Errorf("해시 불일치: got %s, want %s", got.Hash.ShortString(), tx.Hash.ShortString())
	}

	// Has 확인
	if !mp.Has(tx.Hash) {
		t.Error("Has()가 false 반환")
	}
}

// TestMempoolDuplicateAdd는 중복 추가를 테스트합니다.
func TestMempoolDuplicateAdd(t *testing.T) {
	mp := New(nil)

	tx := createTestTx(1, 100)

	// 첫 번째 추가
	if err := mp.Add(tx); err != nil {
		t.Fatalf("첫 번째 추가 실패: %v", err)
	}

	// 중복 추가 시도
	err := mp.Add(tx)
	if err != ErrTxAlreadyExists {
		t.Errorf("중복 추가 에러 불일치: got %v, want ErrTxAlreadyExists", err)
	}

	// 크기는 여전히 1
	if mp.Size() != 1 {
		t.Errorf("크기 불일치: got %d, want 1", mp.Size())
	}
}

// TestMempoolRemove는 트랜잭션 제거를 테스트합니다.
func TestMempoolRemove(t *testing.T) {
	mp := New(nil)

	tx := createTestTx(1, 100)
	mp.Add(tx)

	// 제거
	if err := mp.Remove(tx.Hash); err != nil {
		t.Fatalf("제거 실패: %v", err)
	}

	// 크기 확인
	if mp.Size() != 0 {
		t.Errorf("크기 불일치: got %d, want 0", mp.Size())
	}

	// 조회 불가
	if mp.Has(tx.Hash) {
		t.Error("제거된 트랜잭션이 여전히 존재")
	}

	// 없는 트랜잭션 제거
	err := mp.Remove(tx.Hash)
	if err != ErrTxNotFound {
		t.Errorf("없는 트랜잭션 제거 에러 불일치: got %v, want ErrTxNotFound", err)
	}
}

// TestMempoolPriority는 우선순위 정렬을 테스트합니다.
func TestMempoolPriority(t *testing.T) {
	mp := New(nil)

	// 낮은 우선순위부터 추가
	tx1 := createTestTx(1, 10)  // gasPrice=10 (낮음)
	tx2 := createTestTx(2, 100) // gasPrice=100 (높음)
	tx3 := createTestTx(3, 50)  // gasPrice=50 (중간)

	mp.Add(tx1)
	mp.Add(tx2)
	mp.Add(tx3)

	// 우선순위 순으로 가져오기
	pending := mp.GetPending(3)

	if len(pending) != 3 {
		t.Fatalf("GetPending 길이 불일치: got %d, want 3", len(pending))
	}

	// 높은 가스 가격 순으로 정렬되어야 함
	if pending[0].GasPrice != 100 {
		t.Errorf("첫 번째 트랜잭션 가스가격: got %d, want 100", pending[0].GasPrice)
	}
	if pending[1].GasPrice != 50 {
		t.Errorf("두 번째 트랜잭션 가스가격: got %d, want 50", pending[1].GasPrice)
	}
	if pending[2].GasPrice != 10 {
		t.Errorf("세 번째 트랜잭션 가스가격: got %d, want 10", pending[2].GasPrice)
	}
}

// TestMempoolPopHighestPriority는 Pop을 테스트합니다.
func TestMempoolPopHighestPriority(t *testing.T) {
	mp := New(nil)

	tx1 := createTestTx(1, 10)
	tx2 := createTestTx(2, 100)

	mp.Add(tx1)
	mp.Add(tx2)

	// 높은 우선순위 먼저 Pop
	popped := mp.PopHighestPriority()
	if popped == nil {
		t.Fatal("Pop이 nil 반환")
	}
	if popped.GasPrice != 100 {
		t.Errorf("Pop된 트랜잭션 가스가격: got %d, want 100", popped.GasPrice)
	}

	// 크기 감소 확인
	if mp.Size() != 1 {
		t.Errorf("크기 불일치: got %d, want 1", mp.Size())
	}

	// 두 번째 Pop
	popped = mp.PopHighestPriority()
	if popped.GasPrice != 10 {
		t.Errorf("두 번째 Pop 가스가격: got %d, want 10", popped.GasPrice)
	}

	// 빈 상태에서 Pop
	popped = mp.PopHighestPriority()
	if popped != nil {
		t.Error("빈 mempool에서 Pop이 nil이 아님")
	}
}

// TestMempoolCapacity는 용량 제한을 테스트합니다.
func TestMempoolCapacity(t *testing.T) {
	config := &Config{
		MaxSize:     3,
		MaxTxSize:   32 * 1024,
		MinGasPrice: 1,
		TxTTL:       time.Hour,
	}
	mp := New(config)

	// 3개 추가 (동일한 가격)
	for i := 1; i <= 3; i++ {
		tx := createTestTx(uint64(i), 50) // 모두 gasPrice=50
		if err := mp.Add(tx); err != nil {
			t.Fatalf("트랜잭션 %d 추가 실패: %v", i, err)
		}
	}

	if mp.Size() != 3 {
		t.Fatalf("초기 크기 불일치: got %d, want 3", mp.Size())
	}

	// 4번째 (높은 가격) 추가 시도 - 낮은 것 대체해야 함
	txHigh := createTestTx(5, 100) // gasPrice=100 (가장 높음)
	err := mp.Add(txHigh)
	if err != nil {
		t.Fatalf("높은 가격 트랜잭션 추가 실패: %v", err)
	}

	// 크기는 여전히 3
	if mp.Size() != 3 {
		t.Errorf("크기 불일치: got %d, want 3", mp.Size())
	}

	// 가장 높은 것 확인
	peek := mp.PeekHighestPriority()
	if peek == nil || peek.GasPrice != 100 {
		t.Error("최고 우선순위 트랜잭션이 아님")
	}

	// 낮은 가격으로 추가 시도 - 실패해야 함
	txLow := createTestTx(6, 1) // gasPrice=1 (가장 낮음)
	err = mp.Add(txLow)
	if err != ErrMempoolFull {
		t.Errorf("용량 초과 에러 불일치: got %v, want ErrMempoolFull", err)
	}
}

// TestMempoolRemoveBatch는 배치 제거를 테스트합니다.
func TestMempoolRemoveBatch(t *testing.T) {
	mp := New(nil)

	// 10개 추가
	hashes := make([]TxHash, 10)
	for i := 0; i < 10; i++ {
		tx := createTestTx(uint64(i), 100)
		mp.Add(tx)
		hashes[i] = tx.Hash
	}

	// 5개 제거
	removed := mp.RemoveBatch(hashes[:5])
	if removed != 5 {
		t.Errorf("제거 개수 불일치: got %d, want 5", removed)
	}

	// 크기 확인
	if mp.Size() != 5 {
		t.Errorf("크기 불일치: got %d, want 5", mp.Size())
	}

	// 나머지 5개 제거
	removed = mp.RemoveBatch(hashes[5:])
	if removed != 5 {
		t.Errorf("두 번째 제거 개수 불일치: got %d, want 5", removed)
	}

	// 빈 상태
	if mp.Size() != 0 {
		t.Errorf("최종 크기 불일치: got %d, want 0", mp.Size())
	}
}

// TestMempoolGetByAddress는 주소별 조회를 테스트합니다.
func TestMempoolGetByAddress(t *testing.T) {
	mp := New(nil)

	// 같은 주소에서 여러 트랜잭션
	addr1 := createAddress(1)
	addr2 := createAddress(2)

	tx1 := createTestTxWithAddr(addr1, 1, 100)
	tx2 := createTestTxWithAddr(addr1, 2, 100)
	tx3 := createTestTxWithAddr(addr2, 1, 100)

	mp.Add(tx1)
	mp.Add(tx2)
	mp.Add(tx3)

	// addr1의 트랜잭션
	txs := mp.GetByAddress(addr1)
	if len(txs) != 2 {
		t.Errorf("addr1 트랜잭션 개수: got %d, want 2", len(txs))
	}

	// addr2의 트랜잭션
	txs = mp.GetByAddress(addr2)
	if len(txs) != 1 {
		t.Errorf("addr2 트랜잭션 개수: got %d, want 1", len(txs))
	}
}

// TestMempoolGetNonce는 nonce 조회를 테스트합니다.
func TestMempoolGetNonce(t *testing.T) {
	mp := New(nil)

	addr := createAddress(1)

	// nonce 0, 1, 2 추가
	mp.Add(createTestTxWithAddr(addr, 0, 100))
	mp.Add(createTestTxWithAddr(addr, 1, 100))
	mp.Add(createTestTxWithAddr(addr, 2, 100))

	// 다음 예상 nonce는 3
	nonce := mp.GetNonce(addr)
	if nonce != 3 {
		t.Errorf("nonce 불일치: got %d, want 3", nonce)
	}

	// 없는 주소
	unknownAddr := createAddress(99)
	nonce = mp.GetNonce(unknownAddr)
	if nonce != 0 {
		t.Errorf("없는 주소 nonce: got %d, want 0", nonce)
	}
}

// TestMempoolStats는 통계를 테스트합니다.
func TestMempoolStats(t *testing.T) {
	config := &Config{
		MaxSize:     100,
		MaxTxSize:   32 * 1024,
		MinGasPrice: 1,
		TxTTL:       time.Hour,
	}
	mp := New(config)

	// 다양한 가스 가격의 트랜잭션 추가
	mp.Add(createTestTx(1, 10))
	mp.Add(createTestTx(2, 50))
	mp.Add(createTestTx(3, 100))

	stats := mp.GetStats()

	if stats.Size != 3 {
		t.Errorf("Size 불일치: got %d, want 3", stats.Size)
	}
	if stats.MaxSize != 100 {
		t.Errorf("MaxSize 불일치: got %d, want 100", stats.MaxSize)
	}
	if stats.MinGasPrice != 10 {
		t.Errorf("MinGasPrice 불일치: got %d, want 10", stats.MinGasPrice)
	}
	if stats.MaxGasPrice != 100 {
		t.Errorf("MaxGasPrice 불일치: got %d, want 100", stats.MaxGasPrice)
	}
}

// TestMempoolConcurrency는 동시성 안전성을 테스트합니다.
func TestMempoolConcurrency(t *testing.T) {
	mp := New(&Config{
		MaxSize:     10000,
		MaxTxSize:   32 * 1024,
		MinGasPrice: 1,
		TxTTL:       time.Hour,
	})

	const numGoroutines = 100
	const txPerGoroutine = 100

	var wg sync.WaitGroup
	var addCount int64
	var getCount int64

	// 동시에 추가/조회
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			for j := 0; j < txPerGoroutine; j++ {
				tx := createTestTx(uint64(idx*txPerGoroutine+j), 100)

				// 추가
				if err := mp.Add(tx); err == nil {
					atomic.AddInt64(&addCount, 1)
				}

				// 조회
				if mp.Has(tx.Hash) {
					atomic.AddInt64(&getCount, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	t.Logf("동시성 테스트: 추가 성공=%d, 조회 성공=%d, 최종 크기=%d",
		addCount, getCount, mp.Size())

	// 추가된 만큼 크기가 맞아야 함
	if int64(mp.Size()) != addCount {
		t.Errorf("크기 불일치: got %d, want %d", mp.Size(), addCount)
	}
}

// TestMempoolValidation은 유효성 검사를 테스트합니다.
func TestMempoolValidation(t *testing.T) {
	// 가스 가격 테스트 (크기는 충분히 큼)
	mpGas := New(&Config{
		MaxSize:     100,
		MaxTxSize:   32 * 1024, // 충분히 큰 크기
		MinGasPrice: 10,
		TxTTL:       time.Hour,
	})

	// 가스 가격이 너무 낮음
	txLowGas := createTestTx(1, 5) // gasPrice=5 < minGasPrice=10
	err := mpGas.Add(txLowGas)
	if err != ErrLowGasPrice {
		t.Errorf("낮은 가스 가격 에러: got %v, want ErrLowGasPrice", err)
	}

	// 크기 테스트 (별도 mempool)
	mpSize := New(&Config{
		MaxSize:     100,
		MaxTxSize:   100, // 작은 크기 제한
		MinGasPrice: 1,
		TxTTL:       time.Hour,
	})

	// 크기가 너무 큼
	txBig := createTestTxWithData(2, 100, make([]byte, 200)) // 크기 초과
	err = mpSize.Add(txBig)
	if err == nil {
		t.Error("큰 트랜잭션이 추가됨")
	}
}

// =============================================================================
// 트랜잭션 테스트
// =============================================================================

// TestTxEncodeDecode는 직렬화/역직렬화를 테스트합니다.
func TestTxEncodeDecode(t *testing.T) {
	tx := createTestTxWithData(1, 100, []byte("hello world"))

	// 인코딩
	encoded := tx.Encode()

	// 디코딩
	decoded, err := DecodeTx(encoded)
	if err != nil {
		t.Fatalf("디코딩 실패: %v", err)
	}

	// 필드 비교
	if decoded.From != tx.From {
		t.Error("From 불일치")
	}
	if decoded.To != tx.To {
		t.Error("To 불일치")
	}
	if decoded.Value != tx.Value {
		t.Error("Value 불일치")
	}
	if decoded.Nonce != tx.Nonce {
		t.Error("Nonce 불일치")
	}
	if decoded.GasPrice != tx.GasPrice {
		t.Error("GasPrice 불일치")
	}
	if decoded.GasLimit != tx.GasLimit {
		t.Error("GasLimit 불일치")
	}
	if string(decoded.Data) != string(tx.Data) {
		t.Error("Data 불일치")
	}
	if decoded.Hash != tx.Hash {
		t.Error("Hash 불일치")
	}
}

// TestTxValidation은 트랜잭션 유효성 검사를 테스트합니다.
func TestTxValidation(t *testing.T) {
	// 정상 트랜잭션
	tx := createTestTx(1, 100)
	if err := tx.Validate(); err != nil {
		t.Errorf("정상 트랜잭션 검증 실패: %v", err)
	}

	// GasLimit이 0
	txNoGas := &Tx{
		From:      createAddress(1),
		To:        createAddress(2),
		GasLimit:  0,
		GasPrice:  100,
		Timestamp: time.Now(),
	}
	txNoGas.Hash = txNoGas.ComputeHash()
	if err := txNoGas.Validate(); err == nil {
		t.Error("GasLimit=0이 통과됨")
	}

	// From이 zero
	txNoFrom := &Tx{
		To:        createAddress(2),
		GasLimit:  100,
		GasPrice:  100,
		Timestamp: time.Now(),
	}
	txNoFrom.Hash = txNoFrom.ComputeHash()
	if err := txNoFrom.Validate(); err == nil {
		t.Error("From=zero가 통과됨")
	}
}

// =============================================================================
// 벤치마크
// =============================================================================

// BenchmarkMempoolAdd는 추가 성능을 측정합니다.
func BenchmarkMempoolAdd(b *testing.B) {
	mp := New(&Config{
		MaxSize:     100000,
		MaxTxSize:   32 * 1024,
		MinGasPrice: 1,
		TxTTL:       time.Hour,
	})

	txs := make([]*Tx, b.N)
	for i := 0; i < b.N; i++ {
		txs[i] = createTestTx(uint64(i), 100)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		mp.Add(txs[i])
	}
}

// BenchmarkMempoolGet은 조회 성능을 측정합니다.
func BenchmarkMempoolGet(b *testing.B) {
	mp := New(&Config{
		MaxSize:     100000,
		MaxTxSize:   32 * 1024,
		MinGasPrice: 1,
		TxTTL:       time.Hour,
	})

	// 10000개 트랜잭션 추가
	hashes := make([]TxHash, 10000)
	for i := 0; i < 10000; i++ {
		tx := createTestTx(uint64(i), 100)
		mp.Add(tx)
		hashes[i] = tx.Hash
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		mp.Get(hashes[i%10000])
	}
}

// BenchmarkMempoolGetPending은 우선순위 조회 성능을 측정합니다.
func BenchmarkMempoolGetPending(b *testing.B) {
	mp := New(&Config{
		MaxSize:     100000,
		MaxTxSize:   32 * 1024,
		MinGasPrice: 1,
		TxTTL:       time.Hour,
	})

	// 10000개 트랜잭션 추가
	for i := 0; i < 10000; i++ {
		tx := createTestTx(uint64(i), uint64(i%100+1))
		mp.Add(tx)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		mp.GetPending(100)
	}
}

// BenchmarkMempoolConcurrent는 동시성 성능을 측정합니다.
func BenchmarkMempoolConcurrent(b *testing.B) {
	mp := New(&Config{
		MaxSize:     100000,
		MaxTxSize:   32 * 1024,
		MinGasPrice: 1,
		TxTTL:       time.Hour,
	})

	// 초기 데이터
	for i := 0; i < 1000; i++ {
		tx := createTestTx(uint64(i), 100)
		mp.Add(tx)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%4 == 0 {
				// 25% 쓰기
				tx := createTestTx(uint64(1000+i), 100)
				mp.Add(tx)
			} else {
				// 75% 읽기
				mp.Size()
			}
			i++
		}
	})
}

// =============================================================================
// 헬퍼 함수
// =============================================================================

// createTestTx는 테스트용 트랜잭션을 생성합니다.
func createTestTx(nonce, gasPrice uint64) *Tx {
	return NewTx(
		createAddress(1),
		createAddress(2),
		1000,     // value
		nonce,    // nonce
		gasPrice, // gasPrice
		21000,    // gasLimit
		nil,      // data
	)
}

// createTestTxWithAddr은 지정된 주소로 트랜잭션을 생성합니다.
func createTestTxWithAddr(from Address, nonce, gasPrice uint64) *Tx {
	return NewTx(
		from,
		createAddress(99),
		1000,
		nonce,
		gasPrice,
		21000,
		nil,
	)
}

// createTestTxWithData는 데이터가 있는 트랜잭션을 생성합니다.
func createTestTxWithData(nonce, gasPrice uint64, data []byte) *Tx {
	return NewTx(
		createAddress(1),
		createAddress(2),
		1000,
		nonce,
		gasPrice,
		21000,
		data,
	)
}

// createAddress는 테스트용 주소를 생성합니다.
func createAddress(seed byte) Address {
	var addr Address
	for i := range addr {
		addr[i] = seed
	}
	return addr
}
