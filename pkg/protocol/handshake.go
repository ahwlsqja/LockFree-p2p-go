package protocol

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// =============================================================================
// Handshake - 연결 초기화 프로토콜
// =============================================================================

// HandshakePayload는 핸드셰이크 메시지의 페이로드입니다.
//
// [핸드셰이크 흐름]
// 1. A→B: Handshake (A의 정보)
// 2. B→A: HandshakeAck (B의 정보, 연결 수락 여부)
//
// [교환되는 정보]
// - 프로토콜 버전: 호환성 확인
// - 노드 ID: 피어 식별 (중복 연결 방지)
// - 리스닝 주소: 다른 피어에게 공유할 수 있는 주소
// - User-Agent: 클라이언트 식별 (디버깅용)
type HandshakePayload struct {
	// Version은 프로토콜 버전입니다.
	Version uint32

	// NodeID는 노드의 고유 식별자입니다 (16바이트).
	NodeID [16]byte

	// ListenPort는 리스닝 포트입니다.
	//
	// [왜 전체 주소가 아니라 포트만?]
	// - IP 주소는 연결에서 이미 알 수 있음 (RemoteAddr)
	// - 하지만 연결한 포트 ≠ 리스닝 포트 (임시 포트 사용)
	// - 포트만 알려주면 IP + 포트로 전체 주소 구성 가능
	ListenPort uint16

	// UserAgent는 클라이언트 식별자입니다.
	// 예: "go-p2p/1.0.0"
	UserAgent string

	// Nonce는 랜덤 값입니다.
	//
	// [용도]
	// - 자기 자신에게 연결하는 것 감지
	// - A가 B에 연결했는데 B가 A의 IP:Port를 듣고 연결 시도
	// - Nonce가 같으면 같은 노드
	Nonce uint64
}

// Encode는 HandshakePayload를 바이트 슬라이스로 인코딩합니다.
//
// [바이트 레이아웃]
// - Version: 4바이트
// - NodeID: 16바이트
// - ListenPort: 2바이트
// - Nonce: 8바이트
// - UserAgent: 2바이트 길이 + 문자열
//
// 총 고정 부분: 30바이트 + UserAgent 가변
func (h *HandshakePayload) Encode() []byte {
	// 고정 크기 계산
	fixedSize := 4 + 16 + 2 + 8 // Version + NodeID + ListenPort + Nonce
	userAgentBytes := []byte(h.UserAgent)
	totalSize := fixedSize + 2 + len(userAgentBytes) // +2는 UserAgent 길이

	buf := make([]byte, totalSize)
	offset := 0

	// Version
	binary.BigEndian.PutUint32(buf[offset:], h.Version)
	offset += 4

	// NodeID
	copy(buf[offset:], h.NodeID[:])
	offset += 16

	// ListenPort
	binary.BigEndian.PutUint16(buf[offset:], h.ListenPort)
	offset += 2

	// Nonce
	binary.BigEndian.PutUint64(buf[offset:], h.Nonce)
	offset += 8

	// UserAgent (길이 prefix)
	binary.BigEndian.PutUint16(buf[offset:], uint16(len(userAgentBytes)))
	offset += 2
	copy(buf[offset:], userAgentBytes)

	return buf
}

// DecodeHandshakePayload는 바이트 슬라이스에서 HandshakePayload를 디코딩합니다.
func DecodeHandshakePayload(data []byte) (*HandshakePayload, error) {
	// 최소 크기 검사 (고정 부분 + UserAgent 길이 필드)
	minSize := 4 + 16 + 2 + 8 + 2 // 32바이트
	if len(data) < minSize {
		return nil, fmt.Errorf("핸드셰이크 데이터 부족: %d < %d", len(data), minSize)
	}

	h := &HandshakePayload{}
	offset := 0

	// Version
	h.Version = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	// NodeID
	copy(h.NodeID[:], data[offset:offset+16])
	offset += 16

	// ListenPort
	h.ListenPort = binary.BigEndian.Uint16(data[offset:])
	offset += 2

	// Nonce
	h.Nonce = binary.BigEndian.Uint64(data[offset:])
	offset += 8

	// UserAgent
	userAgentLen := binary.BigEndian.Uint16(data[offset:])
	offset += 2

	if len(data) < offset+int(userAgentLen) {
		return nil, fmt.Errorf("UserAgent 데이터 부족")
	}
	h.UserAgent = string(data[offset : offset+int(userAgentLen)])

	return h, nil
}

// =============================================================================
// HandshakeAck - 핸드셰이크 응답
// =============================================================================

// HandshakeAckPayload는 핸드셰이크 응답 페이로드입니다.
type HandshakeAckPayload struct {
	// Accepted는 연결 수락 여부입니다.
	Accepted bool

	// Reason은 거부 시 이유입니다.
	// Accepted가 true면 빈 문자열
	Reason string

	// Version은 응답하는 노드의 프로토콜 버전입니다.
	Version uint32

	// NodeID는 응답하는 노드의 ID입니다.
	NodeID [16]byte

	// ListenPort는 응답하는 노드의 리스닝 포트입니다.
	ListenPort uint16

	// UserAgent는 응답하는 노드의 클라이언트 식별자입니다.
	UserAgent string

	// Nonce는 응답하는 노드의 랜덤 값입니다.
	Nonce uint64
}

// Encode는 HandshakeAckPayload를 바이트 슬라이스로 인코딩합니다.
func (h *HandshakeAckPayload) Encode() []byte {
	// 크기 계산
	reasonBytes := []byte(h.Reason)
	userAgentBytes := []byte(h.UserAgent)

	// Accepted(1) + ReasonLen(2) + Reason + Version(4) + NodeID(16) + ListenPort(2) + Nonce(8) + UALen(2) + UA
	totalSize := 1 + 2 + len(reasonBytes) + 4 + 16 + 2 + 8 + 2 + len(userAgentBytes)

	buf := make([]byte, totalSize)
	offset := 0

	// Accepted
	if h.Accepted {
		buf[offset] = 1
	} else {
		buf[offset] = 0
	}
	offset++

	// Reason
	binary.BigEndian.PutUint16(buf[offset:], uint16(len(reasonBytes)))
	offset += 2
	copy(buf[offset:], reasonBytes)
	offset += len(reasonBytes)

	// Version
	binary.BigEndian.PutUint32(buf[offset:], h.Version)
	offset += 4

	// NodeID
	copy(buf[offset:], h.NodeID[:])
	offset += 16

	// ListenPort
	binary.BigEndian.PutUint16(buf[offset:], h.ListenPort)
	offset += 2

	// Nonce
	binary.BigEndian.PutUint64(buf[offset:], h.Nonce)
	offset += 8

	// UserAgent
	binary.BigEndian.PutUint16(buf[offset:], uint16(len(userAgentBytes)))
	offset += 2
	copy(buf[offset:], userAgentBytes)

	return buf
}

// DecodeHandshakeAckPayload는 바이트 슬라이스에서 HandshakeAckPayload를 디코딩합니다.
func DecodeHandshakeAckPayload(data []byte) (*HandshakeAckPayload, error) {
	// 최소 크기: Accepted(1) + ReasonLen(2) + Version(4) + NodeID(16) + ListenPort(2) + Nonce(8) + UALen(2) = 35
	minSize := 35
	if len(data) < minSize {
		return nil, fmt.Errorf("핸드셰이크 응답 데이터 부족: %d < %d", len(data), minSize)
	}

	h := &HandshakeAckPayload{}
	offset := 0

	// Accepted
	h.Accepted = data[offset] == 1
	offset++

	// Reason
	reasonLen := binary.BigEndian.Uint16(data[offset:])
	offset += 2
	if len(data) < offset+int(reasonLen) {
		return nil, fmt.Errorf("Reason 데이터 부족")
	}
	h.Reason = string(data[offset : offset+int(reasonLen)])
	offset += int(reasonLen)

	// Version
	if len(data) < offset+4 {
		return nil, fmt.Errorf("Version 데이터 부족")
	}
	h.Version = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	// NodeID
	if len(data) < offset+16 {
		return nil, fmt.Errorf("NodeID 데이터 부족")
	}
	copy(h.NodeID[:], data[offset:offset+16])
	offset += 16

	// ListenPort
	if len(data) < offset+2 {
		return nil, fmt.Errorf("ListenPort 데이터 부족")
	}
	h.ListenPort = binary.BigEndian.Uint16(data[offset:])
	offset += 2

	// Nonce
	if len(data) < offset+8 {
		return nil, fmt.Errorf("Nonce 데이터 부족")
	}
	h.Nonce = binary.BigEndian.Uint64(data[offset:])
	offset += 8

	// UserAgent
	if len(data) < offset+2 {
		return nil, fmt.Errorf("UserAgent 길이 데이터 부족")
	}
	userAgentLen := binary.BigEndian.Uint16(data[offset:])
	offset += 2
	if len(data) < offset+int(userAgentLen) {
		return nil, fmt.Errorf("UserAgent 데이터 부족")
	}
	h.UserAgent = string(data[offset : offset+int(userAgentLen)])

	return h, nil
}

// =============================================================================
// 메시지 생성 헬퍼
// =============================================================================

// NewHandshakeMessage는 핸드셰이크 메시지를 생성합니다.
func NewHandshakeMessage(payload *HandshakePayload) *Message {
	data := payload.Encode()
	return &Message{
		Type:     MsgHandshake,
		Payload:  data,
		Checksum: crc32.ChecksumIEEE(data),
	}
}

// NewHandshakeAckMessage는 핸드셰이크 응답 메시지를 생성합니다.
func NewHandshakeAckMessage(payload *HandshakeAckPayload) *Message {
	data := payload.Encode()
	return &Message{
		Type:     MsgHandshakeAck,
		Payload:  data,
		Checksum: crc32.ChecksumIEEE(data),
	}
}
