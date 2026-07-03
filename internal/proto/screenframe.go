package proto

import "encoding/binary"

// chunkMarker is a first byte that never collides with a FrameKind value
// (those are small uint8s starting at 1), so a receiver can tell a
// screen-frame chunk apart from a regular EncodeFrame message by
// inspecting byte 0 before attempting to parse either format.
const chunkMarker = 0xFE

// screenFrameChunkHeaderSize is [marker(1)][seq(4)][index(2)][total(2)].
const screenFrameChunkHeaderSize = 9

// EncodeScreenFrameChunks splits a JPEG-encoded screen frame into
// DataChannel-safe chunks. WebRTC data channels cap individual message
// size (commonly 256KB or less depending on the negotiated SCTP
// capabilities), and a real desktop capture routinely exceeds that even
// at moderate JPEG quality — a single dense 1080p frame can be several
// hundred KB. Each returned []byte is a complete, independent
// DataChannel message; the receiver reassembles them by frame sequence
// number via ScreenFrameReassembler before JPEG-decoding.
//
// maxChunkSize <= 0 defaults to 16KB, a conservative size that stays
// well under every WebRTC implementation's message limit we know of.
func EncodeScreenFrameChunks(seq uint32, jpegData []byte, maxChunkSize int) [][]byte {
	if maxChunkSize <= 0 {
		maxChunkSize = 16 * 1024
	}

	total := (len(jpegData) + maxChunkSize - 1) / maxChunkSize
	if total == 0 {
		total = 1
	}

	chunks := make([][]byte, 0, total)
	for i := 0; i < total; i++ {
		start := i * maxChunkSize
		end := start + maxChunkSize
		if end > len(jpegData) {
			end = len(jpegData)
		}
		payload := jpegData[start:end]

		buf := make([]byte, screenFrameChunkHeaderSize+len(payload))
		buf[0] = chunkMarker
		binary.BigEndian.PutUint32(buf[1:5], seq)
		binary.BigEndian.PutUint16(buf[5:7], uint16(i))
		binary.BigEndian.PutUint16(buf[7:9], uint16(total))
		copy(buf[screenFrameChunkHeaderSize:], payload)
		chunks = append(chunks, buf)
	}
	return chunks
}

// IsScreenFrameChunk reports whether data is a chunk produced by
// EncodeScreenFrameChunks, and if so parses its header.
func IsScreenFrameChunk(data []byte) (seq uint32, index, total uint16, payload []byte, ok bool) {
	if len(data) < screenFrameChunkHeaderSize || data[0] != chunkMarker {
		return 0, 0, 0, nil, false
	}
	seq = binary.BigEndian.Uint32(data[1:5])
	index = binary.BigEndian.Uint16(data[5:7])
	total = binary.BigEndian.Uint16(data[7:9])
	return seq, index, total, data[screenFrameChunkHeaderSize:], true
}

// ScreenFrameReassembler collects chunks for one in-flight frame and
// reports the complete JPEG once every chunk for its sequence number has
// arrived. Chunks for a new sequence number discard any incomplete
// previous frame — screen streaming only cares about the latest frame,
// so a dropped/incomplete one just gets skipped rather than retried.
// Zero value is ready to use.
type ScreenFrameReassembler struct {
	seq     uint32
	total   uint16
	got     uint16
	parts   [][]byte
	started bool
}

func (r *ScreenFrameReassembler) Add(seq uint32, index, total uint16, payload []byte) (complete []byte, done bool) {
	if !r.started || seq != r.seq {
		r.seq = seq
		r.total = total
		r.parts = make([][]byte, total)
		r.got = 0
		r.started = true
	}

	if int(index) >= len(r.parts) || r.parts[index] != nil {
		return nil, false // out-of-range or duplicate chunk; ignore
	}
	r.parts[index] = payload
	r.got++

	if r.got < r.total {
		return nil, false
	}

	size := 0
	for _, p := range r.parts {
		size += len(p)
	}
	out := make([]byte, 0, size)
	for _, p := range r.parts {
		out = append(out, p...)
	}
	r.started = false
	return out, true
}
