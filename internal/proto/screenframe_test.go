package proto

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestEncodeScreenFrameChunks_SmallFrameFitsOneChunk(t *testing.T) {
	data := []byte("tiny jpeg bytes")
	chunks := EncodeScreenFrameChunks(1, data, 1024)
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}

	seq, index, total, payload, ok := IsScreenFrameChunk(chunks[0])
	if !ok {
		t.Fatal("IsScreenFrameChunk() ok = false, want true")
	}
	if seq != 1 || index != 0 || total != 1 {
		t.Fatalf("seq/index/total = %d/%d/%d, want 1/0/1", seq, index, total)
	}
	if !bytes.Equal(payload, data) {
		t.Fatalf("payload = %v, want %v", payload, data)
	}
}

func TestEncodeScreenFrameChunks_SplitsLargeFrame(t *testing.T) {
	data := bytes.Repeat([]byte{0xAB}, 50000)
	chunks := EncodeScreenFrameChunks(7, data, 16*1024)

	wantChunks := 4 // ceil(50000 / 16384) = 4
	if len(chunks) != wantChunks {
		t.Fatalf("len(chunks) = %d, want %d", len(chunks), wantChunks)
	}

	for i, c := range chunks {
		if len(c) > 9+16*1024 {
			t.Fatalf("chunk %d size %d exceeds maxChunkSize+header", i, len(c))
		}
	}
}

func TestScreenFrameReassembler_RoundTrip(t *testing.T) {
	original := make([]byte, 100000)
	rand.New(rand.NewSource(42)).Read(original)

	chunks := EncodeScreenFrameChunks(5, original, 16*1024)

	var r ScreenFrameReassembler
	var got []byte
	for _, c := range chunks {
		seq, index, total, payload, ok := IsScreenFrameChunk(c)
		if !ok {
			t.Fatalf("IsScreenFrameChunk failed on chunk")
		}
		complete, done := r.Add(seq, index, total, payload)
		if done {
			got = complete
		}
	}

	if got == nil {
		t.Fatal("reassembler never reported completion")
	}
	if !bytes.Equal(got, original) {
		t.Fatal("reassembled bytes don't match original")
	}
}

func TestScreenFrameReassembler_NewerFrameDiscardsIncompletePrevious(t *testing.T) {
	frame1 := EncodeScreenFrameChunks(1, bytes.Repeat([]byte{0x01}, 40000), 16*1024) // 3 chunks
	frame2 := EncodeScreenFrameChunks(2, []byte("small complete frame"), 16*1024)    // 1 chunk

	var r ScreenFrameReassembler

	// only feed the first chunk of frame1, then switch to frame2 entirely
	seq, index, total, payload, _ := IsScreenFrameChunk(frame1[0])
	if _, done := r.Add(seq, index, total, payload); done {
		t.Fatal("should not be done after 1 of 3 chunks")
	}

	seq, index, total, payload, _ = IsScreenFrameChunk(frame2[0])
	complete, done := r.Add(seq, index, total, payload)
	if !done {
		t.Fatal("frame2's single chunk should complete immediately")
	}
	if string(complete) != "small complete frame" {
		t.Fatalf("complete = %q, want %q", complete, "small complete frame")
	}
}

func TestIsScreenFrameChunk_RejectsNonChunkData(t *testing.T) {
	regular, _ := EncodeFrame(InputEvent{Kind: InputMouseMove})
	if _, _, _, _, ok := IsScreenFrameChunk(regular); ok {
		t.Fatal("IsScreenFrameChunk should reject a regular EncodeFrame message")
	}
}
