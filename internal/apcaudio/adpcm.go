package apcaudio

// Ported from the telnetvision door (door/adpcm.go, commit e7493ab).

import "encoding/binary"

// IMA ADPCM (Intel/DVI, WAVE_FORMAT_IMA_ADPCM 0x0011) and G.711 u-law
// (WAVE_FORMAT_MULAW 0x0007) WAV writers. Both are formats every libsndfile
// decodes with no optional codec libraries, so a SyncTERM caller that plays
// PCM WAV plays these too (the door still asks first, see probeFormats).
// IMA ADPCM is 4 bits per sample (a quarter of 16-bit PCM), u-law 8 bits.
//
// Each audio chunk is its own file, so the encoder state restarts per chunk;
// IMA blocks carry their own predictor header, so that costs nothing audible.

var imaStepTable = [89]int32{
	7, 8, 9, 10, 11, 12, 13, 14, 16, 17, 19, 21, 23, 25, 28, 31, 34, 37, 41, 45,
	50, 55, 60, 66, 73, 80, 88, 97, 107, 118, 130, 143, 157, 173, 190, 209, 230,
	253, 279, 307, 337, 371, 408, 449, 494, 544, 598, 658, 724, 796, 876, 963,
	1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066, 2272, 2499, 2749, 3024, 3327,
	3660, 4026, 4428, 4871, 5358, 5894, 6484, 7132, 7845, 8630, 9493, 10442,
	11487, 12635, 13899, 15289, 16818, 18500, 20350, 22385, 24623, 27086, 29794,
	32767,
}

var imaIndexTable = [16]int32{-1, -1, -1, -1, 2, 4, 6, 8, -1, -1, -1, -1, 2, 4, 6, 8}

type imaState struct {
	pred  int32
	index int32
}

// encode returns the 4-bit code for sample s and advances the decoder model
// (the encoder tracks exactly what the decoder will reconstruct).
func (st *imaState) encode(s int32) byte {
	step := imaStepTable[st.index]
	diff := s - st.pred
	var code byte
	if diff < 0 {
		code = 8
		diff = -diff
	}
	vpdiff := step >> 3
	if diff >= step {
		code |= 4
		diff -= step
		vpdiff += step
	}
	step >>= 1
	if diff >= step {
		code |= 2
		diff -= step
		vpdiff += step
	}
	step >>= 1
	if diff >= step {
		code |= 1
		vpdiff += step
	}
	if code&8 != 0 {
		st.pred -= vpdiff
	} else {
		st.pred += vpdiff
	}
	if st.pred > 32767 {
		st.pred = 32767
	} else if st.pred < -32768 {
		st.pred = -32768
	}
	st.index += imaIndexTable[code]
	if st.index < 0 {
		st.index = 0
	} else if st.index > 88 {
		st.index = 88
	}
	return code
}

// imaSamplesPerBlock picks the block length for a chunk of `frames` sample
// frames. A WAV IMA block holds 1 header sample plus a multiple of 8, and
// the chunk's tail block is padded to full length. The decoder plays the
// padding, so pick the length that wastes the fewest samples: an exact
// divisor of the chunk where one exists (1764 frames, 22050 Hz x 80 ms, is
// 4 x 441), otherwise the block size with the smallest pad.
func imaSamplesPerBlock(frames int) int {
	best, bestPad := 505, 1<<30
	for spb := 2041; spb >= 121; spb -= 8 {
		blocks := (frames + spb - 1) / spb
		pad := blocks*spb - frames
		if pad < bestPad {
			best, bestPad = spb, pad
		}
		if pad == 0 {
			break
		}
	}
	return best
}

// imaStartIndex picks the step-table index whose step best matches the mean
// sample-to-sample change over the first few samples.
func imaStartIndex(sample func(int) int32, frames int) int32 {
	n := 16
	if frames-1 < n {
		n = frames - 1
	}
	if n <= 0 {
		return 0
	}
	var sum int64
	for i := 0; i < n; i++ {
		d := int64(sample(i+1) - sample(i))
		if d < 0 {
			d = -d
		}
		sum += d
	}
	mean := int32(sum / int64(n))
	idx := int32(0)
	for idx < 88 && imaStepTable[idx] < mean {
		idx++
	}
	return idx
}

// encodeWAVIMA converts interleaved S16LE PCM to an IMA ADPCM WAV.
func encodeWAVIMA(pcm []byte, rate, channels int) []byte {
	if channels < 1 {
		channels = 1
	}
	frames := len(pcm) / (2 * channels)
	spb := imaSamplesPerBlock(frames)
	blockAlign := 4*channels + (spb-1)/2*channels
	blocks := (frames + spb - 1) / spb
	if blocks == 0 {
		blocks = 1
	}
	sample := func(f, c int) int32 {
		if f >= frames { // tail padding: hold the last sample (no click)
			f = frames - 1
		}
		if f < 0 {
			return 0
		}
		i := (f*channels + c) * 2
		return int32(int16(binary.LittleEndian.Uint16(pcm[i:])))
	}
	data := make([]byte, 0, blocks*blockAlign)
	states := make([]imaState, channels)
	// Each chunk is a separate file, so there is no state from the last one;
	// starting at the smallest step would spend the chunk's first few ms
	// catching up (heard as a faint buzz at the chunk rate). Seed the step
	// from how fast the opening samples move instead. The index travels in
	// the block header, so any starting value decodes correctly.
	for c := 0; c < channels; c++ {
		states[c].index = imaStartIndex(func(f int) int32 { return sample(f, c) }, frames)
	}
	for b := 0; b < blocks; b++ {
		f0 := b * spb
		for c := 0; c < channels; c++ {
			s0 := sample(f0, c)
			states[c].pred = s0 // index carries over between blocks
			data = binary.LittleEndian.AppendUint16(data, uint16(int16(s0)))
			data = append(data, byte(states[c].index), 0)
		}
		// Then 4-byte words, 8 samples each, channels interleaved per word.
		for k := 1; k < spb; k += 8 {
			for c := 0; c < channels; c++ {
				var w [4]byte
				for j := 0; j < 8; j++ {
					code := states[c].encode(sample(f0+k+j, c))
					if j&1 == 0 {
						w[j/2] = code
					} else {
						w[j/2] |= code << 4
					}
				}
				data = append(data, w[:]...)
			}
		}
	}
	byteRate := rate * blockAlign / spb
	fmtChunk := make([]byte, 0, 20)
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, 0x0011)
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, uint16(channels))
	fmtChunk = binary.LittleEndian.AppendUint32(fmtChunk, uint32(rate))
	fmtChunk = binary.LittleEndian.AppendUint32(fmtChunk, uint32(byteRate))
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, uint16(blockAlign))
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, 4) // bits per sample
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, 2) // cbSize
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, uint16(spb))
	// fact = the real frame count, so a reader that honours it skips the pad.
	fact := binary.LittleEndian.AppendUint32(nil, uint32(frames))
	return riffWAVE(fmtChunk, fact, data)
}

// encodeWAVULaw converts interleaved S16LE PCM to G.711 u-law WAV, 8 bits
// per sample with no block structure (so no padding, exact duration).
func encodeWAVULaw(pcm []byte, rate, channels int) []byte {
	if channels < 1 {
		channels = 1
	}
	n := len(pcm) / 2
	data := make([]byte, n)
	for i := 0; i < n; i++ {
		data[i] = linearToULaw(int16(binary.LittleEndian.Uint16(pcm[i*2:])))
	}
	fmtChunk := make([]byte, 0, 18)
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, 0x0007)
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, uint16(channels))
	fmtChunk = binary.LittleEndian.AppendUint32(fmtChunk, uint32(rate))
	fmtChunk = binary.LittleEndian.AppendUint32(fmtChunk, uint32(rate*channels))
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, uint16(channels))
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, 8)
	fmtChunk = binary.LittleEndian.AppendUint16(fmtChunk, 0) // cbSize
	fact := binary.LittleEndian.AppendUint32(nil, uint32(n/channels))
	return riffWAVE(fmtChunk, fact, data)
}

func linearToULaw(s int16) byte {
	const bias, clip = 0x84, 32635
	v := int32(s)
	sign := byte(0)
	if v < 0 {
		v = -v
		sign = 0x80
	}
	if v > clip {
		v = clip
	}
	v += bias
	exp := byte(7)
	for mask := int32(0x4000); v&mask == 0 && exp > 0; mask >>= 1 {
		exp--
	}
	mant := byte((v >> (exp + 3)) & 0x0F)
	return ^(sign | exp<<4 | mant)
}

func riffWAVE(fmtChunk, fact, data []byte) []byte {
	size := 4 + 8 + len(fmtChunk) + 8 + len(fact) + 8 + len(data)
	v := make([]byte, 0, 8+size)
	v = append(v, "RIFF"...)
	v = binary.LittleEndian.AppendUint32(v, uint32(size))
	v = append(v, "WAVE"...)
	v = append(v, "fmt "...)
	v = binary.LittleEndian.AppendUint32(v, uint32(len(fmtChunk)))
	v = append(v, fmtChunk...)
	v = append(v, "fact"...)
	v = binary.LittleEndian.AppendUint32(v, uint32(len(fact)))
	v = append(v, fact...)
	v = append(v, "data"...)
	v = binary.LittleEndian.AppendUint32(v, uint32(len(data)))
	v = append(v, data...)
	return v
}

// wavPCM16 extracts the interleaved S16LE samples from a canonical PCM WAV
// (what the producer sends as codec=wav). ok is false for anything else —
// FLAC, compressed WAV, 8-bit — and the caller forwards the file untouched.
func wavPCM16(file []byte) (pcm []byte, rate, channels int, ok bool) {
	if len(file) < 12 || string(file[0:4]) != "RIFF" || string(file[8:12]) != "WAVE" {
		return nil, 0, 0, false
	}
	p := 12
	gotFmt := false
	for p+8 <= len(file) {
		id := string(file[p : p+4])
		n := int(binary.LittleEndian.Uint32(file[p+4:]))
		body := p + 8
		if n < 0 || body+n > len(file) {
			n = len(file) - body // tolerate a streaming-style oversize length
		}
		switch id {
		case "fmt ":
			if n < 16 {
				return nil, 0, 0, false
			}
			f := file[body:]
			if binary.LittleEndian.Uint16(f[0:]) != 1 || binary.LittleEndian.Uint16(f[14:]) != 16 {
				return nil, 0, 0, false
			}
			channels = int(binary.LittleEndian.Uint16(f[2:]))
			rate = int(binary.LittleEndian.Uint32(f[4:]))
			gotFmt = true
		case "data":
			if !gotFmt || channels < 1 {
				return nil, 0, 0, false
			}
			d := file[body : body+n]
			return d[:len(d)/(2*channels)*(2*channels)], rate, channels, true
		}
		p = body + n + n&1
	}
	return nil, 0, 0, false
}
