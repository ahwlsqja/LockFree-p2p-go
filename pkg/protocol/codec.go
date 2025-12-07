package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

// =============================================================================
// Codec - 메시지 인코딩/디코딩
// =============================================================================

// Encoder는 메시지를 Writer에 인코딩합니다.
//
// [역할]
// - Message 구조체 → 바이트 스트림
// - 헤더 + 페이로드 형식으로 직렬화
//
// [사용 예]
//
//	encoder := NewEncoder(conn)
//	err := encoder.Encode(message)
type Encoder struct {
	w io.Writer
}

// NewEncoder는 새 Encoder를 생성합니다.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w}
}

// Encode는 메시지를 Writer에 씁니다.
//
// [쓰기 순서]
// 1. 헤더 13바이트 (Magic, Type, Length, Checksum)
// 2. 페이로드 (가변 길이)
//
// [에러 처리]
// - 쓰기 실패 시 부분적으로 쓰였을 수 있음
// - 에러 발생 시 연결을 닫는 것이 안전
func (e *Encoder) Encode(msg *Message) error {
	// 헤더 버퍼
	header := make([]byte, HeaderSize)

	// Magic
	binary.BigEndian.PutUint32(header[0:4], MagicNumber)

	// Type
	header[4] = byte(msg.Type)

	// Length
	binary.BigEndian.PutUint32(header[5:9], uint32(len(msg.Payload)))

	// Checksum
	binary.BigEndian.PutUint32(header[9:13], msg.Checksum)

	// 헤더 쓰기
	if _, err := e.w.Write(header); err != nil {
		return fmt.Errorf("헤더 쓰기 실패: %w", err)
	}

	// 페이로드 쓰기 (있는 경우)
	if len(msg.Payload) > 0 {
		if _, err := e.w.Write(msg.Payload); err != nil {
			return fmt.Errorf("페이로드 쓰기 실패: %w", err)
		}
	}

	return nil
}

// =============================================================================
// Decoder - 메시지 디코딩
// =============================================================================

// Decoder는 Reader에서 메시지를 디코딩합니다.
//
// [역할]
// - 바이트 스트림 → Message 구조체
// - 헤더 파싱 → 페이로드 읽기 → 검증
//
// [보안 고려사항]
// - 최대 페이로드 크기 검사 (DoS 방지)
// - 체크섬 검증 (데이터 무결성)
// - 매직 넘버 검사 (프로토콜 확인)
type Decoder struct {
	r io.Reader
}

// NewDecoder는 새 Decoder를 생성합니다.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r}
}

// Decode는 Reader에서 메시지를 읽습니다.
//
// [읽기 과정]
// 1. 헤더 13바이트 읽기
// 2. 매직 넘버 검증
// 3. 길이 검증 (최대 크기 초과 여부)
// 4. 페이로드 읽기
// 5. 체크섬 검증
//
// [블로킹 동작]
// - 데이터가 충분히 올 때까지 블로킹
// - 타임아웃은 Reader 레벨에서 설정해야 함
func (d *Decoder) Decode() (*Message, error) {
	// 1. 헤더 읽기
	//
	// [io.ReadFull을 쓰는 이유]
	// - Read()는 요청한 것보다 적게 읽을 수 있음
	// - ReadFull은 정확히 요청한 바이트 수를 읽음
	header := make([]byte, HeaderSize)
	if _, err := io.ReadFull(d.r, header); err != nil {
		if err == io.EOF {
			return nil, err // 연결 종료
		}
		return nil, fmt.Errorf("헤더 읽기 실패: %w", err)
	}

	// 2. 매직 넘버 검증
	//
	// [왜 가장 먼저 검사하나?]
	// - 잘못된 프로토콜/포트 연결을 빠르게 감지
	// - 나머지 데이터를 읽기 전에 거부 가능
	magic := binary.BigEndian.Uint32(header[0:4])
	if magic != MagicNumber {
		return nil, fmt.Errorf("잘못된 매직 넘버: expected %x, got %x", MagicNumber, magic)
	}

	// 3. 필드 파싱
	msgType := MessageType(header[4])
	length := binary.BigEndian.Uint32(header[5:9])
	checksum := binary.BigEndian.Uint32(header[9:13])

	// 4. 길이 검증
	//
	// [DoS 방지]
	// 악의적인 피어가 length = 4GB로 보내면
	// 4GB 버퍼 할당 시도 → 메모리 고갈
	// 따라서 최대 크기를 미리 검사
	if length > MaxPayloadSize {
		return nil, fmt.Errorf("페이로드가 너무 큼: %d > %d", length, MaxPayloadSize)
	}

	// 5. 페이로드 읽기
	var payload []byte
	if length > 0 {
		payload = make([]byte, length)
		if _, err := io.ReadFull(d.r, payload); err != nil {
			return nil, fmt.Errorf("페이로드 읽기 실패: %w", err)
		}
	}

	// 6. 메시지 생성 및 검증
	msg := &Message{
		Type:     msgType,
		Payload:  payload,
		Checksum: checksum,
	}

	if err := msg.Validate(); err != nil {
		return nil, fmt.Errorf("메시지 검증 실패: %w", err)
	}

	return msg, nil
}

// =============================================================================
// 페이로드 인코딩 헬퍼
// =============================================================================

// EncodeString은 문자열을 바이트 슬라이스로 인코딩합니다.
//
// [형식]
// [2바이트 길이][문자열 바이트]
//
// [왜 2바이트 길이인가?]
// - 대부분의 문자열은 65535바이트 이하
// - 4바이트는 과도함
// - 1바이트는 255자로 부족할 수 있음
func EncodeString(s string) []byte {
	data := []byte(s)
	buf := make([]byte, 2+len(data))
	binary.BigEndian.PutUint16(buf[0:2], uint16(len(data)))
	copy(buf[2:], data)
	return buf
}

// DecodeString은 바이트 슬라이스에서 문자열을 디코딩합니다.
//
// [반환]
// - string: 디코딩된 문자열
// - int: 소비된 바이트 수 (2 + 문자열 길이)
// - error: 디코딩 실패 시
func DecodeString(data []byte) (string, int, error) {
	if len(data) < 2 {
		return "", 0, fmt.Errorf("문자열 길이 부족: %d < 2", len(data))
	}

	length := binary.BigEndian.Uint16(data[0:2])
	if len(data) < int(2+length) {
		return "", 0, fmt.Errorf("문자열 데이터 부족: %d < %d", len(data), 2+length)
	}

	return string(data[2 : 2+length]), int(2 + length), nil
}

// EncodeUint32은 uint32를 바이트 슬라이스로 인코딩합니다.
func EncodeUint32(v uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, v)
	return buf
}

// DecodeUint32는 바이트 슬라이스에서 uint32를 디코딩합니다.
func DecodeUint32(data []byte) (uint32, error) {
	if len(data) < 4 {
		return 0, fmt.Errorf("uint32 데이터 부족: %d < 4", len(data))
	}
	return binary.BigEndian.Uint32(data), nil
}

// EncodeUint64는 uint64를 바이트 슬라이스로 인코딩합니다.
func EncodeUint64(v uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)
	return buf
}

// DecodeUint64는 바이트 슬라이스에서 uint64를 디코딩합니다.
func DecodeUint64(data []byte) (uint64, error) {
	if len(data) < 8 {
		return 0, fmt.Errorf("uint64 데이터 부족: %d < 8", len(data))
	}
	return binary.BigEndian.Uint64(data), nil
}

// EncodeBytes는 바이트 슬라이스를 길이 prefix와 함께 인코딩합니다.
//
// [형식]
// [4바이트 길이][데이터]
func EncodeBytes(data []byte) []byte {
	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(buf[0:4], uint32(len(data)))
	copy(buf[4:], data)
	return buf
}

// DecodeBytes는 바이트 슬라이스를 디코딩합니다.
func DecodeBytes(data []byte) ([]byte, int, error) {
	if len(data) < 4 {
		return nil, 0, fmt.Errorf("바이트 길이 부족: %d < 4", len(data))
	}

	length := binary.BigEndian.Uint32(data[0:4])
	if len(data) < int(4+length) {
		return nil, 0, fmt.Errorf("바이트 데이터 부족: %d < %d", len(data), 4+length)
	}

	result := make([]byte, length)
	copy(result, data[4:4+length])
	return result, int(4 + length), nil
}
